# ADR: Render Slack User-Group Mentions

- Status: Accepted
- Date: 2026-07-21
- Repository: `dee-el/slk`
- Target branch: `main`

## Context

Messages containing a bare Slack user-group mention currently expose wire
markup instead of readable text:

```text
mohan <!subteam^S05580444E3>
```

Slack has two user-group mention forms:

```text
<!subteam^S05580444E3>
<!subteam^S05580444E3|@engineering>
```

The labeled form can render without external metadata. The bare form contains
only a workspace-scoped user-group ID and requires `usergroups.list` metadata
to resolve its handle.

Current rendering handles users (`<@U...>`) and channels (`<#C...>`) through
workspace snapshots, but has no user-group map. `RenderSlackMarkdownWith`,
thread rendering, search flattening, notifications, and Markdown export can
therefore leak the bare token. Block Kit rich text correctly reconstructs a
user-group element as `<!subteam^ID>`, but the host renderer cannot resolve it.

The project uses browser-session credentials rather than app OAuth. Some
workspaces may reject `usergroups.list` because of scope, plan, token type, or
enterprise policy. User-group metadata must therefore be best effort and never
block workspace startup.

Message cache rows already preserve raw text and complete Block Kit JSON. Group
handles can be renamed, so resolved labels must remain render-time metadata and
must not be persisted into message text.

## Decision

Fetch workspace user-group handles into a synchronized in-memory store, carry
immutable snapshots through UI models, and resolve user-group mentions at
render time across every text surface.

Resolution precedence:

1. Embedded token label.
2. Known workspace handle by group ID.
3. Generic `@group` fallback.

Always strip one leading `@` before rendering and then add exactly one `@`.
Never show a raw `<!subteam^...>` token to users.

### Slack client API

Extend the internal Slack client abstraction with user-group listing:

```go
type SlackAPI interface {
    // existing methods
    GetUserGroupsContext(context.Context, ...slack.GetUserGroupsOption) ([]slack.UserGroup, error)
}

func (c *Client) GetUserGroups(ctx context.Context) ([]slack.UserGroup, error)
```

Use:

```go
slack.GetUserGroupsOptionIncludeDisabled(true)
slack.GetUserGroupsOptionIncludeUsers(false)
slack.GetUserGroupsOptionIncludeCount(false)
```

Disabled groups remain resolvable in historical messages. Member lists and
counts are not needed. Follow existing client retry/error conventions and honor
`slack.RateLimitedError.RetryAfter` plus context cancellation.

### Workspace store

Add a workspace-scoped synchronized store:

```go
type userGroupNameStore struct {
    mu    sync.RWMutex
    names map[string]string // group ID -> handle without leading @
}

func (s *userGroupNameStore) Get(id string) (string, bool)
func (s *userGroupNameStore) Set(id, handle string)
func (s *userGroupNameStore) Replace(map[string]string)
func (s *userGroupNameStore) Snapshot() map[string]string
```

Normalization:

- Ignore empty IDs.
- Trim whitespace from handles.
- Remove one leading `@`.
- Ignore empty normalized handles.
- `Snapshot` returns an immutable copy.
- `Replace` atomically removes stale entries.

Add `UserGroupNames *userGroupNameStore` to `WorkspaceContext`.

### Bootstrap and refresh

Workspace startup must not wait for optional user-group metadata:

1. Initialize an empty user-group store.
2. Send workspace-ready state immediately with the current snapshot.
3. On the first successful WebSocket connection, start `GetUserGroups` in a
   background goroutine with a bounded context.
4. Replace the store from returned group IDs and handles, then send
   `WorkspaceUserGroupsUpdatedMsg` for the active workspace.
5. Log failure and keep the previous snapshot; never fail or delay workspace
   readiness.

On every reconnect, run the same background refresh to repair events missed
while disconnected. A short dedupe gate prevents duplicate connection signals
from issuing concurrent requests. Keep the previous map when refresh fails.

Required Slack scope is `usergroups:read`; browser-token restrictions may still
deny it. Such denial is normal degraded operation, not fatal.

### Live events

Extend WebSocket event dispatch for:

```text
subteam_created
subteam_updated
```

Both carry user-group ID/handle metadata. Ignore membership-only and self
added/removed events because membership does not affect rendered labels.

`rtmEventHandler.OnUserGroupChanged`:

- Reject empty IDs.
- Reject a non-empty mismatched TeamID.
- Normalize and update canonical workspace store.
- Notify UI only for active workspace.
- Preserve disabled groups.

Add `WorkspaceUserGroupsUpdatedMsg{TeamID string}`. Active workspace reducer
reads a fresh synchronized snapshot and calls `App.SetUserGroupNames`.

### Renderer contract

Add user-group metadata to full renderer options:

```go
type RenderSlackMarkdownOpts struct {
    // existing fields
    UserGroupNames map[string]string
}
```

Recognize:

```regexp
<!subteam\^([A-Z0-9]+)(?:\|([^>]+))?>
```

Render through existing mention style. Labeled tokens win over map data, which
prevents stale metadata from overriding Slack-provided labels. Unknown IDs use
`@group`.

Keep existing compatibility entry points. Add metadata-aware variants where
signatures cannot change safely:

```go
func SlackMrkdwnToCommonMarkWith(text string, opts CommonMarkOpts) string
func FlattenMrkdwnWithUserGroups(text string, users, groups map[string]string) string
func StripSlackMarkupWithUserGroups(text string, users, groups map[string]string) string
func ThreadToMarkdownWithUserGroups(parent MessageItem, replies []MessageItem, users, channels, groups map[string]string) string
```

Existing functions call new variants with nil group maps.

### Outbound rich text

Current outbound mrkdwn tokenizer classifies `<!subteam^SID>` as a broadcast.
Change the rich-text walker so `subteam^SID` emits
`RichTextSectionUserGroupElement`; keep `here`, `channel`, and `everyone` as
broadcast elements. Preserve wire-form fallback.

### UI propagation

`App` owns a cloned active-workspace user-group snapshot and fans it out to:

- every main message window, including later split windows;
- thread parent/replies;
- threads-view previews;
- workspace search result formatting;
- notifications;
- thread Markdown export.

Main messages and thread models add equality-aware `SetUserGroupNames` methods.
Changed maps invalidate rendered caches because labels are baked into ANSI
lines. Equal maps are no-ops. Block Kit `RenderText` closures include the same
map, so rich-text user-group elements resolve identically.

Workspace switching replaces the full map. Inactive-workspace updates remain
in their workspace store and are applied on switch, preventing cross-workspace
ID leakage.

### Search, notifications, and export

Apply the same precedence and fallback everywhere:

- Search snippets: embedded label -> workspace map -> `@group`.
- Notifications: both labeled and bare forms resolve.
- Thread export/CommonMark: emit `@handle`, never wire markup.

No search index or message-cache rewrite is required.

### Persistence

No SQLite migration.

Reasons:

- Raw message text/JSON already preserves group IDs.
- Handles are mutable metadata.
- Startup list plus live events/reconnect refresh handles normal freshness.
- Failed metadata fetch still produces readable `@group`.

## File-by-File Changes

- `internal/slack/client.go`, `client_test.go`
  - Add best-effort user-group list wrapper, options, errors, retries, tests.
- `internal/slack/events.go`, `events_test.go`
  - Dispatch created/updated subteam events.
- `internal/slack/mrkdwn/walk.go`, `convert_test.go`
  - Emit rich-text user-group elements for outbound subteam tokens.
- `cmd/slk/user_groups.go`, `user_groups_test.go`
  - Add synchronized normalized workspace store.
- `cmd/slk/main.go`
  - Initialize and asynchronously refresh metadata, propagate snapshots, update event handler,
    search, notifications, and exports.
- `cmd/slk/event_handler_test.go`, `cache_render_test.go`,
  `search_items_test.go`
  - Cover live updates, cached Block Kit, and search resolution.
- `internal/ui/msgs.go`, `reducer_workspace.go`
  - Carry initial/switch/update metadata messages.
- `internal/ui/app.go`, `app_test.go`, `winmodels.go`
  - Retain and fan out cloned snapshots, including split windows.
- `internal/ui/messages/render.go`, `render_test.go`
  - Resolve styled group mentions and CommonMark output.
- `internal/ui/messages/model.go`, `model_test.go`
  - Store snapshot, invalidate cache, and feed body/Block Kit renderers.
- `internal/ui/messages/flatten.go`, `flatten_test.go`
  - Add metadata-aware search flattening.
- `internal/ui/thread/model.go`, `model_test.go`
  - Resolve parent/reply/Block Kit mentions and invalidate caches.
- `internal/ui/threadsview/model.go`, `model_test.go`
  - Resolve thread-card previews.
- `internal/notify/notifier.go`, `notifier_test.go`
  - Resolve bare group mentions in notifications.
- `internal/export/markdown.go`, `markdown_test.go`
  - Resolve group mentions in saved Markdown.

## Verification

Focused tests:

```sh
go test ./internal/slack ./internal/notify ./internal/export \
  ./internal/ui/messages ./internal/ui/thread ./internal/ui/threadsview \
  ./internal/ui ./cmd/slk
```

Required assertions:

- Known bare group renders `@handle` with mention styling.
- Labeled token wins over stale map; missing `@` is normalized.
- Unknown/empty handle renders `@group`.
- Raw token never leaks through main/thread/search/notification/export paths.
- Rich-text Block Kit user-group elements resolve.
- Group rename invalidates loaded main/thread/threads-view caches.
- Workspace switch cannot leak group handles between workspaces.
- Late split windows receive active snapshot.
- Created/updated events update only correct workspace.
- API delay/rejection never delays or prevents workspace startup.
- Cache round trip keeps ID and resolves with current metadata.
- Outbound conversion emits a user-group rich-text element.

Full release gate:

```sh
go test -race -count=1 ./...
go vet ./...
make build-macos
otool -L bin/slk | grep AppKit.framework
```

Manual smoke:

1. Open message containing `<!subteam^S05580444E3>`.
2. Confirm `@handle` in main pane, thread, threads view, search, notification,
   and exported Markdown.
3. Rename group in Slack and confirm live rerender.
4. Reconnect and confirm missed rename repairs.
5. Deny user-group API and confirm workspace loads with `@group` fallback.
6. Switch workspaces and confirm no handle leakage.

## Rollout

1. Commit ADR convention and all existing ADRs.
2. Implement directly on fork `main`.
3. Keep API fetch asynchronous/best effort and preserve prior map on failure.
4. Run focused and full release gates.
5. Complete independent review with no high/medium findings.
6. Commit implementation separately from ADR history.
7. Build/install next fork binary with commit/date metadata.
8. Push both ADR and implementation commits to `origin/main`.

## Risks and Mitigations

- Missing scope/token rejection: log and fall back to `@group`; never delay or
  fail workspace startup.
- Slack Connect foreign group ID: embedded label wins; otherwise `@group`.
- Group rename invalidates many rows: updates are rare and match existing
  user/channel metadata cache policy.
- Missed WebSocket update: reconnect list refresh repairs state.
- Disabled historical group: include disabled groups in list.
- Empty/malformed handle: normalization rejects it and renderer uses fallback.
- Cross-workspace ID collision: maps remain workspace-scoped and switch atomically.
- Concurrent list/event update: synchronized store and snapshot replacement avoid
  map races.
- Notification callback reads metadata concurrently: snapshot under store lock.
- API response includes members: explicitly disable user/member payload.

## Alternatives Rejected

- Render every bare token as `@group`: readable but loses useful identity when
  metadata is available.
- Parse ID as display name: exposes opaque Slack IDs.
- Persist handles in message rows: stale after rename and requires migration.
- Fetch group per mention: high latency/rate-limit risk and render-path I/O.
- Request member lists: unnecessary payload and privacy surface.
- Resolve only main messages: inconsistent threads/search/notifications/exports.
- Require user-group API success: would break workspaces without permission.

## Open Questions

None blocking. Default is best-effort workspace list, live created/updated event
repair, reconnect refresh, embedded-label precedence, and `@group` fallback.
