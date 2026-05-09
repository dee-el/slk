# Channel navigation history and recency-ordered finder — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Vim-style `Ctrl+H` / `Ctrl+K` back/forward navigation through channels (per-workspace, in-memory, session-only) and order the `Ctrl+T` channel finder by most-recently-visited (per-workspace, persisted to SQLite).

**Architecture:** Both features hook into the single existing chokepoint `case ChannelSelectedMsg` in `internal/ui/app.go`. Feature 1 adds a per-workspace `navStack` on `App` that pushes/dedupes/caps on each open and walks on `Ctrl+H` / `Ctrl+K`; the `ChannelSelectedMsg` carries a new `FromHistory` flag so the handler knows to skip pushing on synthesized navigations. Feature 2 adds a `channel_visits(workspace_id, channel_id, last_visited)` table in the existing SQLite cache, plumbs `LastVisited` onto every `channelfinder.Item`, and rewrites `filter()` to sort by recency on empty query and to use recency as a tiebreaker on a query.

**Tech Stack:** Go 1.22+, SQLite (`internal/cache`), Bubbletea v2 (`charm.land/bubbletea/v2`), `charm.land/bubbles/v2/key` for keybindings.

**Spec:** `docs/superpowers/specs/2026-05-09-channel-history-and-recency-design.md`

---

## File Structure

| File | Role |
|---|---|
| `internal/cache/db.go` | Add `channel_visits` table + index in initial-schema block |
| `internal/cache/channelvisits.go` (NEW) | `RecordChannelVisit` and `GetChannelVisits` methods on `*DB` |
| `internal/cache/channelvisits_test.go` (NEW) | Round-trip, last-write-wins, multi-workspace isolation |
| `internal/cache/db_test.go` | Add `channel_visits` to the existence-check table list |
| `internal/ui/channelfinder/model.go` | Add `Item.LastVisited`, new `UpdateLastVisited`, rewritten `filter()` sort |
| `internal/ui/channelfinder/model_test.go` | New tests for empty-query recency, with-query tiebreaker, never-visited fallback, `UpdateLastVisited` behavior |
| `internal/ui/keys.go` | `NavBack` (`ctrl+h`) and `NavForward` (`ctrl+k`) bindings |
| `internal/ui/app.go` | `navStack` type, `navHistory` field, `FromHistory` field on `ChannelSelectedMsg`, push/dedupe/cap logic in `case ChannelSelectedMsg`, `navigateBack`/`navigateForward`, `handleNormalMode` cases, `ChannelLookupFunc` and `ChannelVisitRecorder` callbacks, recorder + `UpdateLastVisited` call in `case ChannelSelectedMsg` |
| `internal/ui/app_nav_test.go` (NEW) | Stack semantics: push, dedupe, forward truncation, 50-cap, per-workspace isolation, stale-skip, FromHistory flag |
| `cmd/slk/main.go` | `WorkspaceContext.LastVisitedByChannel`, populate in `connectWorkspace` from `cache.GetChannelVisits`, set `LastVisited` on each `finderItem`, set `LastVisited` on browseable items in `fetchBrowseableChannels`, set `LastVisited` on rtmEventHandler new-IM construction, bind `SetChannelVisitRecorder` and `SetChannelLookupFunc` in `wireCallbacks` |

---

## Important conventions for the engineer

- **Run from the repo root** `/home/grant/local_code/slk/` (or the worktree the orchestrator created).
- **Test runner:** `go test ./<pkg>/... -run <Name>` for targeted, `go test ./...` for full.
- **Build check:** `go build ./...` after every code change before running tests.
- **Line numbers drift.** When a step says "around line N", confirm with `grep -n` first; the surrounding code shown in the step is authoritative.
- **TDD:** every code-producing task starts with a failing test.
- **Commit after each task** (small, focused commits, no batching).
- **Existing test runner pattern in `internal/cache`:** tests use `New(":memory:")` for an in-memory DB and `defer db.Close()`. Mirror that.
- **Existing test runner pattern in `internal/ui`:** tests construct `app := NewApp()` and poke fields/methods directly; many do `app.activeTeamID = "T1"` before exercising message dispatch.

---

## Task 1: Add `channel_visits` schema

**Files:**
- Modify: `internal/cache/db.go` (initial-schema block, after `frecent_emoji`)
- Modify: `internal/cache/db_test.go` (existence-check)

The schema is purely additive — `CREATE TABLE IF NOT EXISTS` makes it
work on both fresh and existing DBs without an `addColumnIfMissing`
migration.

- [ ] **Step 1: Update the existence test to expect the new table**

Open `internal/cache/db_test.go` around line 19. The current `tables` slice is:

```go
tables := []string{"workspaces", "users", "channels", "messages", "reactions", "files"}
```

Change to:

```go
tables := []string{"workspaces", "users", "channels", "messages", "reactions", "files", "channel_visits"}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
go test ./internal/cache/ -run TestNewDB -v
```

Expected: FAIL with `table "channel_visits" does not exist`.

- [ ] **Step 3: Add the schema**

In `internal/cache/db.go`, find the `frecent_emoji` table (around line 118):

```go
	CREATE TABLE IF NOT EXISTS frecent_emoji (
		emoji TEXT PRIMARY KEY,
		use_count INTEGER NOT NULL DEFAULT 0,
		last_used INTEGER NOT NULL DEFAULT 0
	);
```

Add immediately after, before the `CREATE INDEX` block:

```go
	CREATE TABLE IF NOT EXISTS channel_visits (
		workspace_id TEXT NOT NULL,
		channel_id TEXT NOT NULL,
		last_visited INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (workspace_id, channel_id)
	);
```

And in the index block (around line 124), add:

```go
	CREATE INDEX IF NOT EXISTS idx_channel_visits_recent ON channel_visits(workspace_id, last_visited DESC);
```

- [ ] **Step 4: Run test — expect PASS**

```bash
go test ./internal/cache/ -run TestNewDB -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cache/db.go internal/cache/db_test.go
git commit -m "feat(cache): add channel_visits schema for per-channel visit recency"
```

---

## Task 2: `RecordChannelVisit` and `GetChannelVisits` cache methods

**Files:**
- Create: `internal/cache/channelvisits.go`
- Create: `internal/cache/channelvisits_test.go`

Mirrors the shape of `internal/cache/frecent.go`. Pure recency — no
`use_count` column, no decay formula.

- [ ] **Step 1: Write the failing test**

Create `internal/cache/channelvisits_test.go`:

```go
package cache

import (
	"testing"
	"time"
)

func TestRecordAndGetChannelVisit(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	defer db.Close()

	if err := db.RecordChannelVisit("T1", "C1"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := db.RecordChannelVisit("T1", "C2"); err != nil {
		t.Fatalf("record: %v", err)
	}

	visits, err := db.GetChannelVisits("T1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(visits) != 2 {
		t.Fatalf("want 2 entries, got %d", len(visits))
	}
	if visits["C1"] == 0 || visits["C2"] == 0 {
		t.Fatalf("expected non-zero last_visited, got %+v", visits)
	}
}

func TestRecordChannelVisitLastWriteWins(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	defer db.Close()

	if err := db.RecordChannelVisit("T1", "C1"); err != nil {
		t.Fatalf("record: %v", err)
	}
	first, _ := db.GetChannelVisits("T1")
	firstTS := first["C1"]

	// Sleep just over a second so the unix-second timestamp definitely advances.
	time.Sleep(1100 * time.Millisecond)

	if err := db.RecordChannelVisit("T1", "C1"); err != nil {
		t.Fatalf("record: %v", err)
	}
	second, _ := db.GetChannelVisits("T1")
	if second["C1"] <= firstTS {
		t.Fatalf("expected later timestamp on second visit; first=%d second=%d", firstTS, second["C1"])
	}
}

func TestGetChannelVisitsIsolatesWorkspaces(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	defer db.Close()

	if err := db.RecordChannelVisit("T1", "C1"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := db.RecordChannelVisit("T2", "C2"); err != nil {
		t.Fatalf("record: %v", err)
	}

	t1, _ := db.GetChannelVisits("T1")
	t2, _ := db.GetChannelVisits("T2")

	if _, ok := t1["C1"]; !ok {
		t.Errorf("expected T1 to contain C1, got %+v", t1)
	}
	if _, ok := t1["C2"]; ok {
		t.Errorf("expected T1 to NOT contain C2, got %+v", t1)
	}
	if _, ok := t2["C2"]; !ok {
		t.Errorf("expected T2 to contain C2, got %+v", t2)
	}
	if _, ok := t2["C1"]; ok {
		t.Errorf("expected T2 to NOT contain C1, got %+v", t2)
	}
}
```

- [ ] **Step 2: Run test — expect FAIL (compile error)**

```bash
go test ./internal/cache/ -run TestRecordAndGetChannelVisit -v
```

Expected: build failure — `db.RecordChannelVisit undefined`.

- [ ] **Step 3: Implement the methods**

Create `internal/cache/channelvisits.go`:

```go
package cache

import (
	"fmt"
	"time"
)

// RecordChannelVisit upserts the (workspace_id, channel_id) row to the
// current unix timestamp. Used by the App when the user navigates to a
// channel so the Ctrl+T finder can order entries by recency.
func (db *DB) RecordChannelVisit(workspaceID, channelID string) error {
	now := time.Now().Unix()
	_, err := db.conn.Exec(`
		INSERT INTO channel_visits (workspace_id, channel_id, last_visited)
		VALUES (?, ?, ?)
		ON CONFLICT(workspace_id, channel_id)
		DO UPDATE SET last_visited = excluded.last_visited`,
		workspaceID, channelID, now,
	)
	if err != nil {
		return fmt.Errorf("recording channel visit: %w", err)
	}
	return nil
}

// GetChannelVisits returns a map of channel_id -> last_visited (unix
// seconds) for the given workspace. Used at workspace-connect time to
// seed the in-memory map that the channel finder consults for sorting.
func (db *DB) GetChannelVisits(workspaceID string) (map[string]int64, error) {
	rows, err := db.conn.Query(`
		SELECT channel_id, last_visited
		FROM channel_visits
		WHERE workspace_id = ?`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying channel visits: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var channelID string
		var lastVisited int64
		if err := rows.Scan(&channelID, &lastVisited); err != nil {
			return nil, fmt.Errorf("scanning channel visit: %w", err)
		}
		out[channelID] = lastVisited
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./internal/cache/ -v
```

Expected: PASS for `TestRecordAndGetChannelVisit`, `TestRecordChannelVisitLastWriteWins`, `TestGetChannelVisitsIsolatesWorkspaces`. (The `LastWriteWins` test takes ~1.1s due to the deliberate sleep — that's expected.)

- [ ] **Step 5: Commit**

```bash
git add internal/cache/channelvisits.go internal/cache/channelvisits_test.go
git commit -m "feat(cache): RecordChannelVisit / GetChannelVisits"
```

---

## Task 3: Add `LastVisited` field to `channelfinder.Item`

**Files:**
- Modify: `internal/ui/channelfinder/model.go`

Pure data-structure change. No sort behavior change yet — that's
Task 4. This task is split out so existing tests can stay green
through the structural addition before any sort logic shifts.

- [ ] **Step 1: Run existing finder tests as a baseline**

```bash
go test ./internal/ui/channelfinder/ -v
```

Expected: all pass.

- [ ] **Step 2: Add the field**

In `internal/ui/channelfinder/model.go` around line 28:

```go
// Item represents a searchable channel/DM entry.
type Item struct {
	ID       string
	Name     string
	Type     string // channel, dm, group_dm, private
	Presence string // for DMs: active, away
	Joined   bool   // true if the user is already a member; false for browseable public channels
}
```

Change to:

```go
// Item represents a searchable channel/DM entry.
type Item struct {
	ID       string
	Name     string
	Type     string // channel, dm, group_dm, private
	Presence string // for DMs: active, away
	Joined   bool   // true if the user is already a member; false for browseable public channels
	// LastVisited is the unix timestamp (seconds) of the user's most
	// recent visit to this channel; 0 means never visited. Drives the
	// recency-based sort used by filter(): empty-query order is by
	// LastVisited DESC, and on a query LastVisited breaks ties within
	// a match tier.
	LastVisited int64
}
```

- [ ] **Step 3: Run finder tests — expect PASS (no behavior change)**

```bash
go test ./internal/ui/channelfinder/ -v
```

Expected: all pass.

- [ ] **Step 4: Verify the rest of the build**

```bash
go build ./...
```

Expected: clean. The new field has zero value across all callers.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/channelfinder/model.go
git commit -m "feat(channelfinder): add Item.LastVisited field (no sort change yet)"
```

---

## Task 4: Empty-query recency sort

**Files:**
- Modify: `internal/ui/channelfinder/model.go` (`filter()`)
- Modify: `internal/ui/channelfinder/model_test.go`

When the query is empty, sort by `LastVisited DESC`, then `typeRank
ASC`, then `Name ASC` (case-insensitive). Channels with `LastVisited
== 0` (never visited) fall to the bottom and get the existing
`typeRank`+`Name` ordering.

- [ ] **Step 1: Write the failing test**

Add to `internal/ui/channelfinder/model_test.go`:

```go
func TestFilterEmptyOrdersByRecency(t *testing.T) {
	m := New()
	m.SetItems([]Item{
		{ID: "C1", Name: "alpha", Type: "channel", LastVisited: 100},
		{ID: "C2", Name: "bravo", Type: "channel", LastVisited: 300},
		{ID: "C3", Name: "charlie", Type: "channel", LastVisited: 200},
		{ID: "C4", Name: "delta", Type: "channel", LastVisited: 0}, // never visited
		{ID: "C5", Name: "echo", Type: "channel", LastVisited: 0},  // never visited
	})
	m.Open()

	if len(m.filtered) != 5 {
		t.Fatalf("want 5 filtered, got %d", len(m.filtered))
	}
	gotOrder := []string{}
	for _, idx := range m.filtered {
		gotOrder = append(gotOrder, m.items[idx].Name)
	}
	// Visited (DESC by ts): bravo(300), charlie(200), alpha(100).
	// Never visited (typeRank+name): delta, echo.
	want := []string{"bravo", "charlie", "alpha", "delta", "echo"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Errorf("position %d: want %q, got %q (full: %v)", i, want[i], gotOrder[i], gotOrder)
		}
	}
}

func TestFilterEmptyNeverVisitedFallsBackToTypeRank(t *testing.T) {
	m := New()
	m.SetItems([]Item{
		// All never visited; must come out in DM, channel, group_dm order.
		{ID: "C1", Name: "zulu", Type: "channel", LastVisited: 0},
		{ID: "G1", Name: "alpha-group", Type: "group_dm", LastVisited: 0},
		{ID: "D1", Name: "yankee", Type: "dm", LastVisited: 0},
	})
	m.Open()

	if len(m.filtered) != 3 {
		t.Fatalf("want 3 filtered, got %d", len(m.filtered))
	}
	gotOrder := []string{}
	for _, idx := range m.filtered {
		gotOrder = append(gotOrder, m.items[idx].Name)
	}
	// typeRank: dm(0), channel(1), group_dm(2).
	want := []string{"yankee", "zulu", "alpha-group"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Errorf("position %d: want %q, got %q (full: %v)", i, want[i], gotOrder[i], gotOrder)
		}
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

```bash
go test ./internal/ui/channelfinder/ -run TestFilterEmpty -v
```

Expected: FAIL — both new tests assert ordering the current
`filter()` doesn't produce. (`TestFilterEmpty` itself still passes.)

- [ ] **Step 3: Rewrite the empty-query branch of `filter()`**

In `internal/ui/channelfinder/model.go`, find the empty-query branch
of `filter()` (around line 177):

```go
	if q == "" {
		for i := range m.items {
			m.filtered = append(m.filtered, i)
		}
		return
	}
```

Replace with:

```go
	if q == "" {
		idxs := make([]int, len(m.items))
		for i := range m.items {
			idxs[i] = i
		}
		// Insertion sort by (LastVisited DESC, typeRank ASC, name ASC).
		// Insertion sort is stable and the n is small (channel lists).
		for i := 1; i < len(idxs); i++ {
			for j := i; j > 0 && m.lessForEmptyQuery(idxs[j], idxs[j-1]); j-- {
				idxs[j-1], idxs[j] = idxs[j], idxs[j-1]
			}
		}
		m.filtered = idxs
		return
	}
```

Add the comparison helper at the bottom of the file (after
`isSeparator`):

```go
// lessForEmptyQuery reports whether item a should sort before item b
// in the empty-query view. Sort key: LastVisited DESC, typeRank ASC,
// Name ASC (case-insensitive).
func (m *Model) lessForEmptyQuery(ai, bi int) bool {
	a, b := m.items[ai], m.items[bi]
	if a.LastVisited != b.LastVisited {
		return a.LastVisited > b.LastVisited
	}
	ar, br := m.typeRank(ai), m.typeRank(bi)
	if ar != br {
		return ar < br
	}
	return strings.ToLower(a.Name) < strings.ToLower(b.Name)
}
```

- [ ] **Step 4: Run finder tests — expect PASS**

```bash
go test ./internal/ui/channelfinder/ -v
```

Expected: PASS for the new tests AND all existing tests
(`TestFilterEmpty`, etc.). The existing `TestFilterEmpty` only
asserts count (=6), which is preserved.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/channelfinder/model.go internal/ui/channelfinder/model_test.go
git commit -m "feat(channelfinder): empty-query orders by LastVisited DESC, then typeRank, then name"
```

---

## Task 5: With-query recency tiebreaker

**Files:**
- Modify: `internal/ui/channelfinder/model.go` (`filter()` and `sortByTypeRankInPlace`)
- Modify: `internal/ui/channelfinder/model_test.go`

When the query is non-empty, the existing match-tier classification
(prefix → substring → subsequence) still wins. Within a tier, the
sort key becomes `(LastVisited DESC, typeRank ASC, Name ASC)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/ui/channelfinder/model_test.go`:

```go
func TestFilterWithQueryRecencyBreaksTies(t *testing.T) {
	m := New()
	// Two prefix matches for "eng": "engineering" (older) and "engagement" (newer).
	// Recency tiebreak should put "engagement" first.
	m.SetItems([]Item{
		{ID: "C1", Name: "engineering", Type: "channel", LastVisited: 100},
		{ID: "C2", Name: "engagement", Type: "channel", LastVisited: 200},
		{ID: "C3", Name: "marketing", Type: "channel", LastVisited: 999}, // no match
	})
	m.Open()
	m.HandleKey("e")
	m.HandleKey("n")
	m.HandleKey("g")

	if len(m.filtered) < 2 {
		t.Fatalf("want at least 2 matches, got %d", len(m.filtered))
	}
	first := m.items[m.filtered[0]].Name
	second := m.items[m.filtered[1]].Name
	if first != "engagement" {
		t.Errorf("want first match to be the more recent 'engagement', got %q", first)
	}
	if second != "engineering" {
		t.Errorf("want second match to be 'engineering', got %q", second)
	}
}

func TestFilterWithQueryMatchTierStillWins(t *testing.T) {
	m := New()
	// "engagement" is a prefix match (older); "ext-engineering" is a
	// substring match (newer). Tier wins over recency: prefix first.
	m.SetItems([]Item{
		{ID: "C1", Name: "engagement", Type: "channel", LastVisited: 100},
		{ID: "C2", Name: "ext-engineering", Type: "channel", LastVisited: 999},
	})
	m.Open()
	m.HandleKey("e")
	m.HandleKey("n")
	m.HandleKey("g")

	if len(m.filtered) < 2 {
		t.Fatalf("want 2 matches, got %d", len(m.filtered))
	}
	first := m.items[m.filtered[0]].Name
	if first != "engagement" {
		t.Errorf("prefix match should beat newer substring match, got %q first", first)
	}
}
```

- [ ] **Step 2: Run tests — expect the recency-tiebreak test to FAIL**

```bash
go test ./internal/ui/channelfinder/ -run TestFilterWithQuery -v
```

Expected: `TestFilterWithQueryRecencyBreaksTies` FAILS (the existing
sort would put `engineering` first because it precedes
`engagement` in the items slice and they have the same `typeRank`).
`TestFilterWithQueryMatchTierStillWins` may already pass — it
preserves existing behavior.

- [ ] **Step 3: Update the within-tier sort**

In `internal/ui/channelfinder/model.go`, find `sortByTypeRankInPlace`
(around line 252):

```go
// sortByTypeRankInPlace stably reorders idxs so items with a smaller
// typeRank come first while preserving original order within each rank.
func (m *Model) sortByTypeRankInPlace(idxs []int) {
	for i := 1; i < len(idxs); i++ {
		for j := i; j > 0 && m.typeRank(idxs[j-1]) > m.typeRank(idxs[j]); j-- {
			idxs[j-1], idxs[j] = idxs[j], idxs[j-1]
		}
	}
}
```

Replace with:

```go
// sortByTypeRankInPlace stably reorders idxs by
// (LastVisited DESC, typeRank ASC, Name ASC). Used within a single
// match tier (prefix or substring), where the tier itself is the
// outer sort key.
func (m *Model) sortByTypeRankInPlace(idxs []int) {
	for i := 1; i < len(idxs); i++ {
		for j := i; j > 0 && m.lessForEmptyQuery(idxs[j], idxs[j-1]); j-- {
			idxs[j-1], idxs[j] = idxs[j], idxs[j-1]
		}
	}
}
```

(The function name is now slightly misleading but renaming would
require updating two call sites and the tests — leave it for a
future cleanup. The doc comment is the source of truth.)

Then update the subsequence-tier sort (around line 216) to use the
same comparator. Find:

```go
	// Stable sort subsequence matches by (typeRank asc, score desc) so
	// the tightest / most word-boundary-aligned matches come first
	// within each type-rank bucket.
	for i := 1; i < len(subsequenceMatches); i++ {
		for j := i; j > 0; j-- {
			ai := m.typeRank(subsequenceMatches[j-1].idx)
			bi := m.typeRank(subsequenceMatches[j].idx)
			if ai < bi {
				break
			}
			if ai == bi && subsequenceMatches[j-1].score >= subsequenceMatches[j].score {
				break
			}
			subsequenceMatches[j-1], subsequenceMatches[j] = subsequenceMatches[j], subsequenceMatches[j-1]
		}
	}
```

Replace with:

```go
	// Stable sort subsequence matches by
	// (LastVisited DESC, typeRank ASC, score DESC, Name ASC) so the
	// most-recent match wins within a tier, with subsequence-score
	// breaking deeper ties.
	for i := 1; i < len(subsequenceMatches); i++ {
		for j := i; j > 0; j-- {
			ai, bi := subsequenceMatches[j-1].idx, subsequenceMatches[j].idx
			a, b := m.items[ai], m.items[bi]
			if a.LastVisited != b.LastVisited {
				if a.LastVisited > b.LastVisited {
					break
				}
			} else {
				ar, br := m.typeRank(ai), m.typeRank(bi)
				if ar != br {
					if ar < br {
						break
					}
				} else if subsequenceMatches[j-1].score != subsequenceMatches[j].score {
					if subsequenceMatches[j-1].score > subsequenceMatches[j].score {
						break
					}
				} else if strings.ToLower(a.Name) <= strings.ToLower(b.Name) {
					break
				}
			}
			subsequenceMatches[j-1], subsequenceMatches[j] = subsequenceMatches[j], subsequenceMatches[j-1]
		}
	}
```

- [ ] **Step 4: Run all finder tests — expect PASS**

```bash
go test ./internal/ui/channelfinder/ -v
```

Expected: all PASS, including existing tests like `TestFilterPrefixFirst` and `TestFilterCaseInsensitive` which set `LastVisited: 0` and so still tie-break on `typeRank` + `Name` (the previous behavior).

If `TestFilterPrefixFirst` or any existing test now fails, the
likely cause is that the items in `testItems()` have ambiguous
ordering once recency is introduced — read the failing assertion
and confirm the test was relying on insertion order rather than a
specific ordering rule. If so, adjust the test to make its expected
ordering explicit; do NOT change the production sort.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/channelfinder/model.go internal/ui/channelfinder/model_test.go
git commit -m "feat(channelfinder): with-query recency tiebreaker after match tier"
```

---

## Task 6: `UpdateLastVisited` method on the finder

**Files:**
- Modify: `internal/ui/channelfinder/model.go`
- Modify: `internal/ui/channelfinder/model_test.go`

Lets the App update a single item's `LastVisited` mid-session
without rebuilding the entire `[]Item` list. Re-runs `filter()` if
the overlay is currently visible so the change is observed
immediately.

- [ ] **Step 1: Write the failing test**

Add to `internal/ui/channelfinder/model_test.go`:

```go
func TestUpdateLastVisitedMutatesAndReorders(t *testing.T) {
	m := New()
	m.SetItems([]Item{
		{ID: "C1", Name: "alpha", Type: "channel", LastVisited: 100},
		{ID: "C2", Name: "bravo", Type: "channel", LastVisited: 200},
		{ID: "C3", Name: "charlie", Type: "channel", LastVisited: 50},
	})
	m.Open()

	// Initial order: bravo(200), alpha(100), charlie(50)
	if m.items[m.filtered[0]].Name != "bravo" {
		t.Fatalf("setup: want bravo first, got %q", m.items[m.filtered[0]].Name)
	}

	// Visit charlie now (timestamp 999) — should jump to top.
	m.UpdateLastVisited("C3", 999)

	if m.items[m.filtered[0]].Name != "charlie" {
		t.Errorf("after update: want charlie first, got %q", m.items[m.filtered[0]].Name)
	}
}

func TestUpdateLastVisitedNoopForUnknownID(t *testing.T) {
	m := New()
	m.SetItems([]Item{
		{ID: "C1", Name: "alpha", Type: "channel", LastVisited: 100},
	})
	m.Open()

	// Should not panic or alter anything.
	m.UpdateLastVisited("C-NONEXISTENT", 999)

	if len(m.filtered) != 1 || m.items[0].LastVisited != 100 {
		t.Errorf("unknown id should be a no-op; items=%+v filtered=%v", m.items, m.filtered)
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL (compile error: UpdateLastVisited undefined)**

```bash
go test ./internal/ui/channelfinder/ -run TestUpdateLastVisited -v
```

Expected: build failure.

- [ ] **Step 3: Implement the method**

In `internal/ui/channelfinder/model.go`, after `MarkJoined` (around line 65), add:

```go
// UpdateLastVisited sets the LastVisited timestamp for the matching
// item, if any, and re-runs filter() if the overlay is currently
// visible so the new ordering takes effect on the next render. No-op
// for an unknown ID.
func (m *Model) UpdateLastVisited(channelID string, ts int64) {
	for i := range m.items {
		if m.items[i].ID == channelID {
			m.items[i].LastVisited = ts
			if m.visible {
				m.filter()
			}
			return
		}
	}
}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./internal/ui/channelfinder/ -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/channelfinder/model.go internal/ui/channelfinder/model_test.go
git commit -m "feat(channelfinder): UpdateLastVisited for live recency updates"
```

---

## Task 7: `WorkspaceContext.LastVisitedByChannel` + populate from cache

**Files:**
- Modify: `cmd/slk/main.go`

Hooks the cache layer into the workspace-context lifecycle. The map
is populated once at connect and mutated in-place on every visit.
The `LastVisited` field on each `finderItem` is filled from this map
in three places: the initial channel loop (`connectWorkspace`), the
browseable-channels fetch (`fetchBrowseableChannels`), and the
runtime new-IM creation in `rtmEventHandler`.

- [ ] **Step 1: Add the field to `WorkspaceContext`**

In `cmd/slk/main.go` around line 142 (end of the `WorkspaceContext` struct):

```go
	Presence   string    // "active" or "away"; "" until first fetch
	DNDEnabled bool      // true if either snooze or admin-DND is active
	DNDEndTS   time.Time // unified end timestamp; zero if not in DND
}
```

Insert before the closing brace:

```go
	// LastVisitedByChannel maps channelID -> unix-second timestamp of
	// the user's most recent visit to that channel in this workspace.
	// Populated once at connect from cache.GetChannelVisits and
	// updated on every ChannelSelectedMsg via the visit recorder.
	// Used to populate channelfinder.Item.LastVisited for sort.
	LastVisitedByChannel map[string]int64
```

So the struct ends:

```go
	Presence             string
	DNDEnabled           bool
	DNDEndTS             time.Time
	LastVisitedByChannel map[string]int64
}
```

- [ ] **Step 2: Initialize and populate in `connectWorkspace`**

In `cmd/slk/main.go` around line 1059, the `wctx := &WorkspaceContext{...}` literal:

```go
	wctx := &WorkspaceContext{
		Client:      client,
		TeamID:      client.TeamID(),
		TeamName:    token.TeamName,
		UserID:      client.UserID(),
		UserNames:         make(map[string]string),
		UserNamesByHandle: make(map[string]string),
		BotUserIDs:        make(map[string]bool),
		LastReadMap:       make(map[string]string),
		CustomEmoji: make(map[string]string),
	}
```

Add the new map to the literal:

```go
	wctx := &WorkspaceContext{
		Client:               client,
		TeamID:               client.TeamID(),
		TeamName:             token.TeamName,
		UserID:               client.UserID(),
		UserNames:            make(map[string]string),
		UserNamesByHandle:    make(map[string]string),
		BotUserIDs:           make(map[string]bool),
		LastReadMap:          make(map[string]string),
		CustomEmoji:          make(map[string]string),
		LastVisitedByChannel: make(map[string]int64),
	}
```

Then immediately after the `cachedUsers, _ := db.ListUsers(...)` block (around line 1074-1087), add:

```go
	// Seed last-visited timestamps for the channel finder's recency
	// sort. Best-effort: failure is logged and the map stays empty,
	// which means the finder uses its default order until the user
	// starts visiting channels.
	if visits, err := db.GetChannelVisits(client.TeamID()); err != nil {
		log.Printf("warning: loading channel visits for %s: %v", token.TeamName, err)
	} else {
		wctx.LastVisitedByChannel = visits
	}
```

- [ ] **Step 3: Plumb LastVisited into the joined-channels loop**

Around line 1172-1190 in `connectWorkspace`:

```go
	for _, ch := range channels {
		item, finderItem := buildChannelItem(ch, wctx, cfg, client.TeamID())
		upsertChannelInDB(db, ch, item.Type, client.TeamID())

		if ch.IsIM {
			// ... existing block ...
		}
		wctx.Channels = append(wctx.Channels, item)
		wctx.FinderItems = append(wctx.FinderItems, finderItem)
	}
```

Just before the `wctx.FinderItems = append(...)` line, add:

```go
		finderItem.LastVisited = wctx.LastVisitedByChannel[ch.ID]
```

So the tail looks like:

```go
		wctx.Channels = append(wctx.Channels, item)
		finderItem.LastVisited = wctx.LastVisitedByChannel[ch.ID]
		wctx.FinderItems = append(wctx.FinderItems, finderItem)
```

- [ ] **Step 4: Plumb LastVisited into the browseable-channels loop**

In `fetchBrowseableChannels` around line 1254:

```go
		browseable = append(browseable, channelfinder.Item{
			ID:     ch.ID,
			Name:   ch.Name,
			Type:   "channel",
			Joined: false,
		})
```

Change to:

```go
		browseable = append(browseable, channelfinder.Item{
			ID:          ch.ID,
			Name:        ch.Name,
			Type:        "channel",
			Joined:      false,
			LastVisited: wctx.LastVisitedByChannel[ch.ID],
		})
```

- [ ] **Step 5: Plumb LastVisited into the rtmEventHandler new-IM construction**

In `cmd/slk/main.go` around line 2286, the rtmEventHandler builds a new `finderItem` for runtime-created channels:

```go
	item, finderItem := buildChannelItem(ch, h.wsCtx, h.cfg, h.workspaceID)
```

A few lines later (around line 2312):

```go
		h.wsCtx.FinderItems = append(h.wsCtx.FinderItems, finderItem)
```

Insert immediately before the `append`:

```go
		finderItem.LastVisited = h.wsCtx.LastVisitedByChannel[ch.ID]
```

Use `grep -n "h.wsCtx.FinderItems = append" cmd/slk/main.go` to
locate the exact spot if the line number has drifted.

- [ ] **Step 6: Build check**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add cmd/slk/main.go
git commit -m "feat(workspace): seed LastVisitedByChannel from cache and plumb into finder items"
```

---

## Task 8: `ChannelVisitRecorder` callback on `App`

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `internal/ui/app_test.go`

Adds the callback type, field, and setter, and invokes it from
`case ChannelSelectedMsg`. Also calls
`a.channelFinder.UpdateLastVisited` locally so the finder reflects
the new recency without waiting for a reload.

- [ ] **Step 1: Write the failing test**

Add to `internal/ui/app_test.go`:

```go
func TestChannelSelectedInvokesVisitRecorder(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"

	var recorded []string
	app.SetChannelVisitRecorder(func(channelID string) {
		recorded = append(recorded, channelID)
	})

	// Dispatch a ChannelSelectedMsg. The handler should call the recorder.
	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "general", Type: "channel"})

	if len(recorded) != 1 || recorded[0] != "C1" {
		t.Errorf("want recorded=[C1], got %v", recorded)
	}
}

func TestChannelSelectedFromHistoryStillRecordsVisit(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"

	var recorded []string
	app.SetChannelVisitRecorder(func(channelID string) {
		recorded = append(recorded, channelID)
	})

	// Even when synthesized by back/forward (FromHistory: true),
	// recency must update so going back makes that channel "most recent".
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "ops", Type: "channel", FromHistory: true})

	if len(recorded) != 1 || recorded[0] != "C2" {
		t.Errorf("want recorded=[C2], got %v", recorded)
	}
}
```

The second test references `FromHistory`, which is added in Task 9.
That test will compile-fail now and pass once Task 9 lands. Comment
it out for this task (mark with `// Re-enable in Task 9` and
re-enable it as part of Task 9). Actually — simpler: defer the
second test entirely to Task 9. Remove it from this task.

So for Task 8 the test file gets only `TestChannelSelectedInvokesVisitRecorder`.

- [ ] **Step 2: Run test — expect FAIL**

```bash
go test ./internal/ui/ -run TestChannelSelectedInvokesVisitRecorder -v
```

Expected: build failure — `SetChannelVisitRecorder undefined`.

- [ ] **Step 3: Add the type, field, and setter**

In `internal/ui/app.go`, near other callback type aliases (around the top of the type-declaration cluster — search for `type PermalinkFetchFunc` or similar with `grep -n "type.*Func " internal/ui/app.go | head`), add:

```go
// ChannelVisitRecorder is invoked from case ChannelSelectedMsg to let
// main.go persist the visit (SQLite write + in-memory map update on
// the WorkspaceContext). Always called regardless of FromHistory.
type ChannelVisitRecorder func(channelID string)
```

Find the section of `App` struct holding callback fields (look for `permalinkFetchFn` around line 706, or `themeSaveFn` around line 721) and add:

```go
	channelVisitRecorder ChannelVisitRecorder
```

Add the setter near other `SetX` methods (around line 3782 where
`SetChannelLastReadFetcher` is defined):

```go
// SetChannelVisitRecorder wires the callback that persists a channel
// visit (SQLite write + WorkspaceContext map update). Called once per
// ChannelSelectedMsg.
func (a *App) SetChannelVisitRecorder(fn ChannelVisitRecorder) {
	a.channelVisitRecorder = fn
}
```

- [ ] **Step 4: Invoke from `case ChannelSelectedMsg`**

In `internal/ui/app.go` around line 1314-1370, find the
`case ChannelSelectedMsg:` body. After `a.activeChannelID = msg.ID`
(around line 1332) and before the cache-first render block, insert:

```go
		// Update local finder ordering immediately so the next Ctrl+T
		// sees this channel at the top of the recents.
		now := time.Now().Unix()
		a.channelFinder.UpdateLastVisited(msg.ID, now)
		// Persist the visit (SQLite write + WorkspaceContext map update)
		// asynchronously via main.go's recorder closure.
		if a.channelVisitRecorder != nil {
			a.channelVisitRecorder(msg.ID)
		}
```

Verify `time` is already imported at the top of the file (it is; the
existing handler uses `time.Time{}` and `time.Now()`).

- [ ] **Step 5: Run tests — expect PASS**

```bash
go test ./internal/ui/ -run TestChannelSelectedInvokesVisitRecorder -v
```

Expected: PASS.

Then run the full UI test suite to ensure nothing regressed:

```bash
go test ./internal/ui/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/app.go internal/ui/app_test.go
git commit -m "feat(ui): record channel visits and update finder recency on ChannelSelectedMsg"
```

---

## Task 9: Wire the recorder closure in `wireCallbacks`

**Files:**
- Modify: `cmd/slk/main.go`

The closure does two things: synchronous update of
`wctx.LastVisitedByChannel` (so subsequent finder rebuilds see the
fresh timestamp), and a fire-and-forget goroutine for the SQLite
write. The App separately calls `channelFinder.UpdateLastVisited`
(see Task 8), so this closure does not touch the finder.

- [ ] **Step 1: Add the binding**

In `cmd/slk/main.go`, find `wireCallbacks` (around line 609). After
the existing `app.SetChannelLastReadFetcher(...)` call (around line 614), add:

```go
		app.SetChannelVisitRecorder(func(channelID string) {
			wctx.LastVisitedByChannel[channelID] = time.Now().Unix()
			go func() {
				if err := db.RecordChannelVisit(wctx.TeamID, channelID); err != nil {
					log.Printf("warning: recording channel visit %s/%s: %v", wctx.TeamID, channelID, err)
				}
			}()
		})
```

- [ ] **Step 2: Build check**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 3: Manual smoke test (optional but recommended)**

```bash
go build -o /tmp/slk-smoke ./cmd/slk
```

If you have a configured workspace, run it briefly: open Ctrl+T,
note the order, switch to a channel, exit, restart, open Ctrl+T
again — the visited channel should appear at the top.

Skip if the test workspace isn't set up; the feature is also
exercised by the existing unit tests at this point.

- [ ] **Step 4: Commit**

```bash
git add cmd/slk/main.go
git commit -m "feat(workspace): wire ChannelVisitRecorder closure for SQLite + in-memory update"
```

---

## Task 10: `NavBack` / `NavForward` keymap entries

**Files:**
- Modify: `internal/ui/keys.go`

Add the two new bindings. No behavior wired yet — that's Task 13.

- [ ] **Step 1: Add fields to `KeyMap`**

In `internal/ui/keys.go` around line 42, the `ToggleSection` line ends the struct:

```go
	ToggleSection       key.Binding
}
```

Insert before the closing brace:

```go
	NavBack             key.Binding
	NavForward          key.Binding
```

- [ ] **Step 2: Initialize them in `DefaultKeyMap`**

In `internal/ui/keys.go` around line 82, the `ToggleSection` initializer:

```go
		ToggleSection:       key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle section")),
	}
}
```

Insert before the closing brace of the literal:

```go
		NavBack:             key.NewBinding(key.WithKeys("ctrl+h"), key.WithHelp("ctrl+h", "navigate back")),
		NavForward:          key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "navigate forward")),
```

- [ ] **Step 3: Build check + run UI tests**

```bash
go build ./...
go test ./internal/ui/...
```

Expected: clean and all green. The new bindings are unused so no
behavior changes.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/keys.go
git commit -m "feat(ui/keys): add NavBack (ctrl+h) and NavForward (ctrl+k) bindings"
```

---

## Task 11: Add `FromHistory` field to `ChannelSelectedMsg`

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `internal/ui/app_test.go`

Add the field. All existing emit sites omit it (zero value `false`),
so behavior is preserved. Re-enable the deferred test from Task 8.

- [ ] **Step 1: Add the field**

In `internal/ui/app.go` around line 80:

```go
	ChannelSelectedMsg struct {
		ID   string
		Name string
		// Type is the channel type ("channel", "private", "dm",
		// "group_dm"); used to render a type-aware glyph in the
		// message-pane header and status bar. May be empty when
		// callers don't yet know the type — the UI then falls
		// back to a default `#` glyph.
		Type string
	}
```

Add the `FromHistory` field:

```go
	ChannelSelectedMsg struct {
		ID   string
		Name string
		// Type is the channel type ("channel", "private", "dm",
		// "group_dm"); used to render a type-aware glyph in the
		// message-pane header and status bar. May be empty when
		// callers don't yet know the type — the UI then falls
		// back to a default `#` glyph.
		Type string
		// FromHistory marks navigations synthesized by Ctrl+H /
		// Ctrl+K. The case ChannelSelectedMsg handler suppresses
		// pushing onto navHistory when this is true so back/forward
		// walks don't grow the stack on every step. Visit recording
		// is unaffected — going back to a channel still updates its
		// last-visited timestamp.
		FromHistory bool
	}
```

- [ ] **Step 2: Add the deferred Task-8 test**

Add to `internal/ui/app_test.go`:

```go
func TestChannelSelectedFromHistoryStillRecordsVisit(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"

	var recorded []string
	app.SetChannelVisitRecorder(func(channelID string) {
		recorded = append(recorded, channelID)
	})

	// Even when synthesized by back/forward (FromHistory: true),
	// recency must update so going back makes that channel "most recent".
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "ops", Type: "channel", FromHistory: true})

	if len(recorded) != 1 || recorded[0] != "C2" {
		t.Errorf("want recorded=[C2], got %v", recorded)
	}
}
```

- [ ] **Step 3: Build + run tests — expect PASS**

```bash
go build ./...
go test ./internal/ui/ -run TestChannelSelectedFromHistoryStillRecordsVisit -v
```

Expected: PASS.

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/app.go internal/ui/app_test.go
git commit -m "feat(ui): add ChannelSelectedMsg.FromHistory flag"
```

---

## Task 12: `navStack` type, `navHistory` field, push/dedupe/cap logic

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `internal/ui/app_test.go`

Adds the per-workspace stack, hooks the push into
`case ChannelSelectedMsg` when `!msg.FromHistory`, and verifies the
edge cases. No back/forward navigation yet — that's Task 14.

- [ ] **Step 1: Write the failing tests**

Add to `internal/ui/app_test.go`. These tests poke
`app.navHistory` directly (it's a private field, but the test is in
the same package).

```go
func TestNavStackPushOnChannelSelected(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C3", Name: "c", Type: "channel"})

	stack := app.navHistory["T1"]
	if stack == nil {
		t.Fatal("expected nav stack for T1 to exist")
	}
	want := []string{"C1", "C2", "C3"}
	if !reflect.DeepEqual(stack.entries, want) {
		t.Errorf("entries: want %v, got %v", want, stack.entries)
	}
	if stack.cursor != 2 {
		t.Errorf("cursor: want 2, got %d", stack.cursor)
	}
}

func TestNavStackDedupesConsecutive(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"}) // re-select same
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})

	stack := app.navHistory["T1"]
	want := []string{"C1", "C2"}
	if !reflect.DeepEqual(stack.entries, want) {
		t.Errorf("entries: want %v, got %v", want, stack.entries)
	}
	if stack.cursor != 1 {
		t.Errorf("cursor: want 1, got %d", stack.cursor)
	}
}

func TestNavStackForwardTruncationOnNewVisit(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"

	// Build A, B, C; back to B (simulated by directly manipulating cursor);
	// then visit D — C should be dropped.
	_, _ = app.Update(ChannelSelectedMsg{ID: "A", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "B", Name: "b", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C", Name: "c", Type: "channel"})

	// Simulate a back step (cursor moves but entries don't change).
	app.navHistory["T1"].cursor = 1

	_, _ = app.Update(ChannelSelectedMsg{ID: "D", Name: "d", Type: "channel"})

	stack := app.navHistory["T1"]
	want := []string{"A", "B", "D"}
	if !reflect.DeepEqual(stack.entries, want) {
		t.Errorf("entries: want %v, got %v", want, stack.entries)
	}
	if stack.cursor != 2 {
		t.Errorf("cursor: want 2, got %d", stack.cursor)
	}
}

func TestNavStackCapAt50EvictsOldest(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"

	for i := 0; i < 60; i++ {
		_, _ = app.Update(ChannelSelectedMsg{ID: fmt.Sprintf("C%d", i), Name: "x", Type: "channel"})
	}
	stack := app.navHistory["T1"]
	if len(stack.entries) != 50 {
		t.Errorf("len: want 50, got %d", len(stack.entries))
	}
	if stack.entries[0] != "C10" {
		t.Errorf("oldest after eviction: want C10, got %q", stack.entries[0])
	}
	if stack.entries[49] != "C59" {
		t.Errorf("newest: want C59, got %q", stack.entries[49])
	}
	if stack.cursor != 49 {
		t.Errorf("cursor: want 49, got %d", stack.cursor)
	}
}

func TestNavStackPerWorkspaceIsolation(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"
	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})

	app.activeTeamID = "T2"
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})

	t1 := app.navHistory["T1"]
	t2 := app.navHistory["T2"]
	if t1 == nil || t2 == nil {
		t.Fatalf("expected both stacks to exist; t1=%v t2=%v", t1, t2)
	}
	if !reflect.DeepEqual(t1.entries, []string{"C1"}) {
		t.Errorf("T1: want [C1], got %v", t1.entries)
	}
	if !reflect.DeepEqual(t2.entries, []string{"C2"}) {
		t.Errorf("T2: want [C2], got %v", t2.entries)
	}
}

func TestNavStackFromHistoryDoesNotPush(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})

	// FromHistory navigation should NOT grow the stack.
	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel", FromHistory: true})

	stack := app.navHistory["T1"]
	if !reflect.DeepEqual(stack.entries, []string{"C1", "C2"}) {
		t.Errorf("entries should be unchanged; got %v", stack.entries)
	}
	if stack.cursor != 1 {
		t.Errorf("cursor should be unchanged at 1, got %d", stack.cursor)
	}
}
```

`reflect` and `fmt` are already imported at the top of `app_test.go`.

- [ ] **Step 2: Run tests — expect FAIL**

```bash
go test ./internal/ui/ -run TestNavStack -v
```

Expected: build failure — `app.navHistory undefined`.

- [ ] **Step 3: Add the `navStack` type and `navHistory` field**

In `internal/ui/app.go`, near the `lastChannelByTeam` declaration (around line 711-718), add the type and field. The exact placement: declare the type just before the `App` struct (search for `type App struct` — around line 591), and add the field inside `App` near `lastChannelByTeam`.

First, find a quiet spot to declare the type. A good location is
just before `type editState struct` (around line 60). Add:

```go
// navStack is a per-workspace browser-style back/forward history of
// channel IDs. cursor points at the current entry; len(entries)==0
// is the empty state with cursor==-1.
type navStack struct {
	entries []string
	cursor  int
}

// navStackMax caps the per-workspace history at 50 entries. When a
// push would exceed the cap, the oldest entry is dropped and the
// cursor is shifted accordingly.
const navStackMax = 50
```

Then in the `App` struct, find `lastChannelByTeam` (around line 718) and immediately after it add:

```go
	// navHistory holds the per-workspace ctrl+h / ctrl+k browser-style
	// jump list. Lazy-initialized on first push for each team. Cleared
	// only when slk exits — the stack is session-only by design.
	navHistory map[string]*navStack
```

In `NewApp` (around line 836-871), the existing initializer adds maps explicitly. Find a place in the `&App{...}` literal to add:

```go
		navHistory:           make(map[string]*navStack),
```

Place it near `lastSelfSendByChannel` or another `make()` initializer in the same literal so it's stylistically grouped.

If the `&App{...}` literal does not currently initialize
`lastChannelByTeam`, search the file for `lastChannelByTeam = make`
to see how it is set up and place `navHistory` similarly. (At time
of writing `lastChannelByTeam` is initialized lazily on first
write, but for `navHistory` we initialize eagerly so tests can
read `app.navHistory["T1"]` without nil-checks.)

- [ ] **Step 4: Add the push helper**

Add to `internal/ui/app.go` near other small helpers (the file
contains many private methods; a reasonable spot is just above
`func (a *App) handleNormalMode`):

```go
// pushNavHistory appends channelID onto the team's navigation stack.
// Behavior:
//   - Lazy-creates the stack on first push.
//   - Dedupes consecutive: a no-op if entries[cursor] == channelID.
//   - Truncates the forward path: cursor < len-1 entries beyond cursor
//     are dropped (browser-style "new visit kills forward history").
//   - Caps at navStackMax: drops oldest entries and shifts cursor.
func (a *App) pushNavHistory(teamID, channelID string) {
	if teamID == "" || channelID == "" {
		return
	}
	stack, ok := a.navHistory[teamID]
	if !ok {
		stack = &navStack{cursor: -1}
		a.navHistory[teamID] = stack
	}
	if stack.cursor >= 0 && stack.cursor < len(stack.entries) && stack.entries[stack.cursor] == channelID {
		return
	}
	if stack.cursor < len(stack.entries)-1 {
		stack.entries = stack.entries[:stack.cursor+1]
	}
	stack.entries = append(stack.entries, channelID)
	stack.cursor = len(stack.entries) - 1
	if len(stack.entries) > navStackMax {
		drop := len(stack.entries) - navStackMax
		stack.entries = stack.entries[drop:]
		stack.cursor -= drop
	}
}
```

- [ ] **Step 5: Hook the push into `case ChannelSelectedMsg`**

In `internal/ui/app.go`, find `case ChannelSelectedMsg:` (around line 1314). Right after the visit-recorder block added in Task 8 (`a.channelVisitRecorder(msg.ID)`), add:

```go
		if !msg.FromHistory {
			a.pushNavHistory(a.activeTeamID, msg.ID)
		}
```

- [ ] **Step 6: Run tests — expect PASS**

```bash
go test ./internal/ui/ -run TestNavStack -v
```

Expected: PASS for all six new tests.

```bash
go test ./...
```

Expected: full suite passes.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/app.go internal/ui/app_test.go
git commit -m "feat(ui): per-workspace channel nav stack with dedupe, truncation, and 50-cap"
```

---

## Task 13: `ChannelLookupFunc` callback

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `cmd/slk/main.go`

Lets `navigateBack` / `navigateForward` (Task 14) probe whether a
historical channel ID is still present in the current workspace.

- [ ] **Step 1: Add the type, field, and setter**

In `internal/ui/app.go`, near the `ChannelVisitRecorder` declaration from Task 8, add:

```go
// ChannelLookupFunc returns metadata for a channel that the App has
// in its navigation history. Used by navigateBack / navigateForward
// to skip stale entries (channels the user has left, archived, or
// kicked from). Returns ok=false when the channel is no longer
// available in the active workspace.
type ChannelLookupFunc func(channelID string) (name, channelType string, ok bool)
```

In the `App` struct, near `channelVisitRecorder`:

```go
	channelLookup ChannelLookupFunc
```

Setter, near `SetChannelVisitRecorder`:

```go
// SetChannelLookupFunc wires the callback used by navigateBack /
// navigateForward to validate channel IDs from the history stack
// before re-opening them. Stale IDs (return ok=false) are silently
// dropped and skipped.
func (a *App) SetChannelLookupFunc(fn ChannelLookupFunc) {
	a.channelLookup = fn
}
```

- [ ] **Step 2: Wire the closure in `wireCallbacks` (cmd/slk/main.go)**

In `cmd/slk/main.go` around line 614, after the
`SetChannelVisitRecorder` call from Task 9, add:

```go
		app.SetChannelLookupFunc(func(channelID string) (string, string, bool) {
			// Sidebar (joined channels + Slack-native sections).
			for _, ch := range wctx.Channels {
				if ch.ID == channelID {
					return ch.Name, ch.Type, true
				}
			}
			// Finder items (joined + browseable). Covers DMs/group DMs
			// that aren't in the sidebar pre-conversation, and any
			// browseable public channels.
			for _, it := range wctx.FinderItems {
				if it.ID == channelID {
					return it.Name, it.Type, true
				}
			}
			return "", "", false
		})
```

- [ ] **Step 3: Build check + run tests**

```bash
go build ./...
go test ./...
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/app.go cmd/slk/main.go
git commit -m "feat(ui): ChannelLookupFunc callback for nav-stack stale-skip"
```

---

## Task 14: Implement `navigateBack` and `navigateForward`

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `internal/ui/app_test.go`

Two `(*App)` methods. Each walks the stack one step, skips and drops
stale entries, and emits a `ChannelSelectedMsg{FromHistory: true}`
when a valid target is found. Silent no-op at boundaries.

- [ ] **Step 1: Write the failing tests**

Add to `internal/ui/app_test.go`:

```go
func TestNavigateBackEmitsChannelSelectedMsg(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"
	app.SetChannelLookupFunc(func(channelID string) (string, string, bool) {
		return channelID + "-name", "channel", true
	})

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})

	cmd := app.navigateBack()
	if cmd == nil {
		t.Fatal("navigateBack returned nil cmd; expected ChannelSelectedMsg dispatch")
	}
	got := cmd()
	cs, ok := got.(ChannelSelectedMsg)
	if !ok {
		t.Fatalf("want ChannelSelectedMsg, got %T", got)
	}
	if cs.ID != "C1" {
		t.Errorf("want ID=C1, got %q", cs.ID)
	}
	if !cs.FromHistory {
		t.Error("FromHistory must be true on synthesized navigation")
	}
	if app.navHistory["T1"].cursor != 0 {
		t.Errorf("cursor: want 0, got %d", app.navHistory["T1"].cursor)
	}
}

func TestNavigateForwardEmitsChannelSelectedMsg(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"
	app.SetChannelLookupFunc(func(channelID string) (string, string, bool) {
		return channelID + "-name", "channel", true
	})

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})
	app.navHistory["T1"].cursor = 0 // simulate one back

	cmd := app.navigateForward()
	if cmd == nil {
		t.Fatal("navigateForward returned nil cmd")
	}
	got := cmd()
	cs, ok := got.(ChannelSelectedMsg)
	if !ok {
		t.Fatalf("want ChannelSelectedMsg, got %T", got)
	}
	if cs.ID != "C2" {
		t.Errorf("want ID=C2, got %q", cs.ID)
	}
	if !cs.FromHistory {
		t.Error("FromHistory must be true on synthesized navigation")
	}
	if app.navHistory["T1"].cursor != 1 {
		t.Errorf("cursor: want 1, got %d", app.navHistory["T1"].cursor)
	}
}

func TestNavigateBackAtStartIsNoop(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"
	app.SetChannelLookupFunc(func(channelID string) (string, string, bool) {
		return channelID, "channel", true
	})

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})

	cmd := app.navigateBack()
	if cmd != nil {
		t.Errorf("expected nil at boundary, got non-nil cmd")
	}
}

func TestNavigateForwardAtEndIsNoop(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"
	app.SetChannelLookupFunc(func(channelID string) (string, string, bool) {
		return channelID, "channel", true
	})

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})

	cmd := app.navigateForward()
	if cmd != nil {
		t.Errorf("expected nil at end of stack, got non-nil cmd")
	}
}

func TestNavigateBackSkipsStaleAndDropsThem(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"
	// Lookup says "C2 is gone, others valid".
	app.SetChannelLookupFunc(func(channelID string) (string, string, bool) {
		if channelID == "C2" {
			return "", "", false
		}
		return channelID, "channel", true
	})

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C3", Name: "c", Type: "channel"})

	// Cursor at C3 (index 2). Back should skip C2 and land on C1.
	cmd := app.navigateBack()
	if cmd == nil {
		t.Fatal("navigateBack returned nil cmd")
	}
	got := cmd()
	cs, ok := got.(ChannelSelectedMsg)
	if !ok {
		t.Fatalf("want ChannelSelectedMsg, got %T", got)
	}
	if cs.ID != "C1" {
		t.Errorf("want ID=C1 (skipping stale C2), got %q", cs.ID)
	}
	// C2 must have been dropped from entries.
	stack := app.navHistory["T1"]
	for _, id := range stack.entries {
		if id == "C2" {
			t.Errorf("stale C2 should have been dropped from entries; got %v", stack.entries)
		}
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

```bash
go test ./internal/ui/ -run TestNavigate -v
```

Expected: build failure — `app.navigateBack` / `navigateForward` undefined.

- [ ] **Step 3: Implement `navigateBack` and `navigateForward`**

In `internal/ui/app.go` near `pushNavHistory`, add:

```go
// navigateBack walks the per-workspace history stack one step
// backward, skipping any channel IDs that no longer resolve via
// channelLookup (and dropping them from the stack). Returns a tea.Cmd
// that synthesizes a ChannelSelectedMsg{FromHistory: true} for the
// new target, or nil if there's no valid earlier entry.
func (a *App) navigateBack() tea.Cmd {
	return a.walkNav(-1)
}

// navigateForward is the symmetric opposite of navigateBack.
func (a *App) navigateForward() tea.Cmd {
	return a.walkNav(+1)
}

// walkNav implements the shared logic for navigateBack / navigateForward.
// step must be -1 or +1.
func (a *App) walkNav(step int) tea.Cmd {
	stack, ok := a.navHistory[a.activeTeamID]
	if !ok || stack.cursor < 0 {
		return nil
	}

	// Walk in `step` direction looking for the first valid entry.
	// As we go, accumulate stale indices to drop afterwards.
	var stale []int
	idx := stack.cursor + step
	var (
		foundID    string
		foundName  string
		foundType  string
		foundIndex = -1
	)
	for idx >= 0 && idx < len(stack.entries) {
		id := stack.entries[idx]
		if a.channelLookup != nil {
			name, ctype, valid := a.channelLookup(id)
			if valid {
				foundID, foundName, foundType, foundIndex = id, name, ctype, idx
				break
			}
			stale = append(stale, idx)
		} else {
			// No lookup wired (tests/early init): treat all as valid.
			foundID, foundName, foundType, foundIndex = id, id, "channel", idx
			break
		}
		idx += step
	}

	if foundIndex < 0 {
		// No valid target. Still drop the stale entries we discovered
		// so the stack doesn't keep walking past them next time, and
		// shift the cursor back to compensate for any drops below it.
		droppedBeforeCursor := 0
		for _, s := range stale {
			if s < stack.cursor {
				droppedBeforeCursor++
			}
		}
		a.dropStaleStackEntries(stack, stale)
		stack.cursor -= droppedBeforeCursor
		return nil
	}

	// Compute foundIndex's new position after stale drops:
	// every dropped index < foundIndex shifts foundIndex down by 1.
	newFoundIndex := foundIndex
	for _, s := range stale {
		if s < foundIndex {
			newFoundIndex--
		}
	}
	a.dropStaleStackEntries(stack, stale)
	stack.cursor = newFoundIndex

	id, name, ctype := foundID, foundName, foundType
	return func() tea.Msg {
		return ChannelSelectedMsg{ID: id, Name: name, Type: ctype, FromHistory: true}
	}
}

// dropStaleStackEntries returns a new entries slice with the indices
// in stale removed. Order of indices in stale doesn't matter.
func (a *App) dropStaleStackEntries(stack *navStack, stale []int) {
	if len(stale) == 0 {
		return
	}
	drop := make(map[int]struct{}, len(stale))
	for _, s := range stale {
		drop[s] = struct{}{}
	}
	out := make([]string, 0, len(stack.entries)-len(stale))
	for i, e := range stack.entries {
		if _, ok := drop[i]; !ok {
			out = append(out, e)
		}
	}
	stack.entries = out
}
```

The `tea.Cmd` and `tea.Msg` types come from the existing
`tea "charm.land/bubbletea/v2"` import at the top of the file.

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./internal/ui/ -run TestNavigate -v
```

Expected: PASS for all five new tests.

```bash
go test ./...
```

Expected: full suite passes.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/app.go internal/ui/app_test.go
git commit -m "feat(ui): navigateBack / navigateForward with stale-skip"
```

---

## Task 15: Wire `Ctrl+H` / `Ctrl+K` into `handleNormalMode`

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `internal/ui/app_test.go`

Add the two case arms to `handleNormalMode` and verify via a key
dispatch test.

- [ ] **Step 1: Write the failing test**

Add to `internal/ui/app_test.go`. The existing test pattern in this
file calls `handleNormalMode` directly (see `app_test.go:319, 391`)
and uses `tea.KeyPressMsg{Code: ..., Mod: tea.ModCtrl}` (see
`app_test.go:240`).

```go
func TestCtrlHTriggersNavBack(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"
	app.SetChannelLookupFunc(func(channelID string) (string, string, bool) {
		return channelID, "channel", true
	})

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})

	cmd := app.handleNormalMode(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected cmd from ctrl+h dispatch")
	}
	got := cmd()
	cs, ok := got.(ChannelSelectedMsg)
	if !ok {
		t.Fatalf("want ChannelSelectedMsg, got %T", got)
	}
	if cs.ID != "C1" || !cs.FromHistory {
		t.Errorf("want ID=C1 FromHistory=true, got %+v", cs)
	}
}

func TestCtrlKTriggersNavForward(t *testing.T) {
	app := NewApp()
	app.activeTeamID = "T1"
	app.SetChannelLookupFunc(func(channelID string) (string, string, bool) {
		return channelID, "channel", true
	})

	_, _ = app.Update(ChannelSelectedMsg{ID: "C1", Name: "a", Type: "channel"})
	_, _ = app.Update(ChannelSelectedMsg{ID: "C2", Name: "b", Type: "channel"})
	app.navHistory["T1"].cursor = 0

	cmd := app.handleNormalMode(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected cmd from ctrl+k dispatch")
	}
	got := cmd()
	cs, ok := got.(ChannelSelectedMsg)
	if !ok {
		t.Fatalf("want ChannelSelectedMsg, got %T", got)
	}
	if cs.ID != "C2" || !cs.FromHistory {
		t.Errorf("want ID=C2 FromHistory=true, got %+v", cs)
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

```bash
go test ./internal/ui/ -run TestCtrl -v
```

Expected: FAIL — ctrl+h / ctrl+k currently produce no command.

- [ ] **Step 3: Add the cases in `handleNormalMode`**

In `internal/ui/app.go` around line 2270-2272, the existing
`ToggleThread` case:

```go
	case key.Matches(msg, a.keys.ToggleThread):
		a.ToggleThread()
```

Immediately after, add:

```go
	case key.Matches(msg, a.keys.NavBack):
		if cmd := a.navigateBack(); cmd != nil {
			return cmd
		}

	case key.Matches(msg, a.keys.NavForward):
		if cmd := a.navigateForward(); cmd != nil {
			return cmd
		}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./internal/ui/ -run TestCtrl -v
```

Expected: PASS.

```bash
go test ./...
```

Expected: full suite passes.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/app.go internal/ui/app_test.go
git commit -m "feat(ui): bind ctrl+h / ctrl+k to navigateBack / navigateForward"
```

---

## Task 16: Final verification

**Files:** none modified — verification only.

- [ ] **Step 1: Run the entire test suite**

```bash
go test ./...
```

Expected: all PASS.

- [ ] **Step 2: Build the binary**

```bash
go build ./...
go build -o /tmp/slk-verify ./cmd/slk
```

Expected: clean, binary produced.

- [ ] **Step 3: Manual smoke test (recommended if a workspace is configured)**

Run `/tmp/slk-verify` against your workspace. Verify:

1. **Empty-query Ctrl+T after a fresh start**: opens with the
   first-time recents (any pre-existing visit data from the cache).
2. **Visit a few channels** (sidebar Enter on three different channels).
3. **Open Ctrl+T** with empty query — most-recently-visited should be at the top.
4. **Ctrl+H** — navigates to the previous channel; the message-pane header swaps.
5. **Ctrl+H again** — navigates further back if more history exists.
6. **Ctrl+K** — navigates forward through the stack.
7. **Open a new channel from the sidebar** while at a back-position — forward path is truncated; Ctrl+K is a no-op.
8. **Workspace switch**, then Ctrl+H — history is per-workspace; you don't jump back into the previous workspace.
9. **Restart slk** — recency ordering survives (it's persisted); back/forward stack does NOT (session-only — expected).

- [ ] **Step 4: No commit**

Verification only.

---

## Self-review notes

- **Spec coverage.** Each spec section maps to at least one task:
  - Section 1 (shared `ChannelSelectedMsg.FromHistory` + recorder hook) → Tasks 8, 11
  - Section 2 (keybindings) → Task 10
  - Section 3 (back/forward stack + lookup) → Tasks 12, 13, 14, 15
  - Section 4 (recency: schema, cache layer, plumbing, finder sort) → Tasks 1, 2, 3, 4, 5, 6, 7, 9
- **Stack edge cases:** dedupe (Task 12), forward-truncation (Task 12), 50-cap (Task 12), per-workspace isolation (Task 12), stale-skip (Task 14).
- **Recency edge cases:** empty-query order (Task 4), with-query tier wins (Task 5), with-query tiebreaker (Task 5), never-visited fallback (Task 4), live update (Task 6, 8).
- **Visit recording on `FromHistory`:** confirmed by `TestChannelSelectedFromHistoryStillRecordsVisit` in Task 11.
