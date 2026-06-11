# Search: In-Channel FTS (`/`) + Workspace Search Modal (`ctrl+f`)

**Date:** 2026-06-10
**Status:** Approved
**Related:** `ModeSearch` stub (`internal/ui/mode.go:10`), `/` binding (`internal/ui/keys.go:64`)

## Problem

slk has no message search. `ModeSearch` and the `/` keybinding are declared
but unimplemented. The SQLite cache already stores full message text
(`messages.text`), and Slack's `search.messages` API can search history the
cache has never seen. Users need both: fast "find in this channel" and
authoritative "find anywhere in the workspace".

## Goals

1. `/` enters a vim-style in-channel search over the current channel's
   *cached* history, backed by an FTS5 index. Matches highlight in the
   message list; `n`/`N` jump between them.
2. `ctrl+f` opens a workspace-wide search modal backed by Slack's
   `search.messages`. Raw query pass-through, so Slack modifiers
   (`from:`, `in:`, `before:`, …) work unmodified. `Enter` on a result
   jumps to that message in its channel.
3. Jumping to a message older than loaded history loads a history window
   around its ts (closing the non-goal left by the open-links work).

## Non-Goals (v1)

- Incremental (per-keystroke) search for `/`; search executes on `Enter`.
- `/` inside the thread panel (channel message pane only).
- Search history / up-arrow recall in the `/` prompt.
- Cross-workspace search (`ctrl+f` searches the active workspace only).
- Server-search pagination beyond the first page (50 results).
- Client-side validation or completion of Slack query modifiers.
- FTS5-backed local tier for the global modal (possible follow-up).

## Design

### 1. Data layer: FTS5 index (`internal/cache/search.go`)

External-content FTS5 table so message text is not stored twice:

```sql
CREATE VIRTUAL TABLE messages_fts USING fts5(
  text,
  content='messages',
  content_rowid='rowid',
  tokenize='unicode61 remove_diacritics 2'
);
```

- `remove_diacritics 2` gives accent-insensitive matching, consistent with
  the `text.Fold` direction.
- Three triggers (AFTER INSERT / AFTER UPDATE OF text / AFTER DELETE on
  `messages`) keep the index in sync — the standard FTS5 external-content
  pattern. Triggers fire inside the same transactions as existing message
  upserts, so WAL + `busy_timeout` already cover concurrency.
- Migration in the existing additive-migration spot in
  `internal/cache/db.go`: create table + triggers if missing, then one-time
  backfill (`INSERT INTO messages_fts(rowid, text) SELECT rowid, text FROM
  messages`).

Query:

```sql
SELECT m.ts, m.text, ...
FROM messages_fts f JOIN messages m ON m.rowid = f.rowid
WHERE messages_fts MATCH ? AND m.channel_id = ? AND m.workspace_id = ?
ORDER BY m.ts DESC
```

**Query semantics:** FTS5 matches tokens, not substrings. User input is
transformed into quoted prefix terms — `foo bar` → `"foo"* "bar"*` — i.e.
"messages containing words starting with foo AND bar". Quoting all terms
also means user input is never interpreted as FTS5 operators (`OR`, `NEAR`,
parens are literals).

**Fallback:** if FTS migration/backfill fails, log and degrade to a plain
`LIKE` query against `messages` at the same call site; the UI is unaware.
Search must never block app startup.

**Verification item:** confirm `modernc.org/sqlite` ships FTS5 enabled
(smoke test in CI that creates an fts5 table).

### 2. In-channel search (`/`, `ModeSearch`, `internal/ui/mode_search.go`)

Flow:

1. `/` in normal mode enters `ModeSearch`; a `/`-prefixed prompt renders in
   the status line (same spot as `:` command mode).
2. Typing edits the query. `Enter` executes: query the FTS index for the
   current channel, store the match list (ts-ordered, newest→oldest), jump
   to the nearest match at or older than the cursor position (searching
   "backward" through history, the natural direction in a chat log).
   `Esc` cancels.
3. After `Enter`, back in normal mode with an *active search*: `n` → next
   older match, `N` → next newer. Status line shows query and position
   (`/foo  3/17`).
4. `Esc` in normal mode with an active search clears it (highlights and
   status indicator removed).
5. `n` past the oldest match wraps to the newest (vim behavior) with a
   "search wrapped" status hint.

**Highlighting:** loaded messages containing matches get the matched
word-prefixes highlighted with a theme style; the currently-selected match
gets the existing selection treatment.

**Off-screen matches:** match ts values come from the cache, so a match may
be older than the loaded window. Jumping to one uses the jump-to-ts loading
described in §4.

**No matches:** status line shows `/foo  no matches`; nothing highlighted.

### 3. Workspace search modal (`ctrl+f`, `internal/ui/searchresults/`)

New modal widget package following the channelfinder pattern:

- `ctrl+f` (new `WorkspaceSearch` binding in `KeyMap`; `?` is taken by
  help) opens the modal with a text input.
- `Enter` executes — no per-keystroke API calls (Slack rate-limits search).
  A "Searching…" spinner shows while in flight; a new `Enter` with a new
  query replaces results.
- Results list: one row per match — `#channel`, author display name,
  absolute timestamp in the configured `tsFormat` (consistent with the
  message pane; no relative-time machinery exists in slk), text snippet.
  Arrows/`ctrl+j`/`ctrl+k`/`ctrl+p`/`ctrl+n` navigate (`j`/`k` must type
  into the query input), `Enter` jumps, `Esc` closes.
- Rows render from data in the search response itself (Slack includes
  channel name and username per match), so uncached channels/users display
  fine; jumping to an uncached channel goes through the normal channel-open
  path.
- `Enter` on a result closes the modal and navigates: switch channel (the
  pending-navigation mechanism from `reducer_links.go`), load history
  around the ts (§4), select the message. Thread replies open the thread
  panel to the reply, as the permalink path does.
- First page only (`count=50`), sorted by relevance (Slack's `score`
  default). If Slack reports more, footer shows "showing 50 of N".

**API:** new `SearchMessages(query string)` on the slack client wrapping
`search.messages` with the workspace's xoxc token + `d` cookie. Raw query
string passes through untouched.

**Verification item (early checkpoint in the plan):** confirm
`search.messages` works with xoxc browser tokens against a real workspace;
fall back to the internal `search.modules.messages` endpoint if not.

### 4. Jump-to-ts history loading

The open-links work (#71) left "fetching history windows around an old
message ts" as a non-goal: `SelectByTS` only selects messages already in
the pane buffer, otherwise toasting "message is older than loaded history".
Both search features need the real thing, and the permalink path inherits
it for free:

- New capability on the messages load path: given a target ts not in the
  loaded buffer, fetch a history window around that ts
  (`conversations.history` with `latest`/`oldest` around the target,
  inclusive), splice/replace the pane buffer, then `SelectByTS`.
- Used by: `n`/`N` jumps to off-screen matches (§2), search-result jumps
  (§3), and existing permalink navigation (upgrade of the #71 toast path).
- On fetch failure: surface the existing load-error toast, keep current
  position.

## Error handling

| Failure | Behavior |
|---|---|
| `/` query has no matches | Status line `no matches`, stay in normal mode |
| FTS migration/backfill fails | Log; degrade to `LIKE` fallback |
| History fetch for off-screen jump fails | Load-error toast, keep position |
| `search.messages` API error / 429 | Error line in modal, query intact, modal stays open |
| Zero server results | "No results" placeholder row |
| Unknown modifier value (e.g. `from:@nobody`) | Slack returns zero results; no client validation |
| Jump target channel inaccessible | Normal channel-open error path; toast |

## Testing

**Cache (`internal/cache/search_test.go`):**
- Migration: fresh DB gets table + triggers; pre-existing messages are
  backfilled and searchable.
- Trigger sync: insert/edit/delete via existing cache API → FTS reflects it.
- Query transformation: `foo bar` → `"foo"* "bar"*`; operator injection
  (`foo OR bar`, quotes, parens) treated as literals.
- Accent/case insensitivity: `cafe` matches `café`.
- Channel/workspace scoping: other channels' matches excluded.
- `LIKE` fallback returns the same result shape.
- FTS5 availability smoke test (fails loudly if the driver drops it).

**UI:**
- Reducer tests for `ModeSearch` (style of existing `reducer_*_test.go`):
  enter mode, execute, `n`/`N` ordering + wrap, `Esc` clears, no-match
  state.
- `searchresults` model tests: rendering, navigation, `Enter` emits the
  jump message, error/spinner states.
- Jump-to-ts: target in buffer → direct select; target older → history
  window fetched then selected; fetch failure → toast.

**Slack client:** `SearchMessages` against a mocked HTTP server — request
shape (raw query pass-through), response parsing, error/429 mapping.

**Manual:** xoxc token compatibility with `search.messages` against a real
workspace — explicit early checkpoint in the implementation plan.
