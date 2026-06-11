# Window Management Phase 3: Per-Window Channel Views — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every window in the split tree owns a live `messages.Model`: splits show independent channels updating in real time, focus changes are instant pointer swaps (no channel re-dispatch), and channel-scoped events fan out to all windows viewing that channel.

**Architecture:** `App` gains `winModels map[wintree.LeafID]*messages.Model`, and the existing `messagepane` field becomes a `*messages.Model` that ALWAYS points at the focused window's model (invariant maintained by focus/split/close/reset paths). The ~97 existing focused-window call sites keep working almost verbatim because every `messages.Model` method has a pointer receiver. Event seams change shape: focused-window semantics stay on `a.messagepane`; channel-scoped events route via `modelsForChannel(chID)`; workspace/global events via `allWinModels()`. Rendering replaces Phase 2 placeholders with live read-only panes cached per window.

**Tech Stack:** Go 1.26, bubbletea v2, lipgloss v2.

**Spec:** `docs/superpowers/specs/2026-06-11-window-management-design.md` (Design §2, Phasing item 3). **Base branch:** create `window-management-phase3` off `window-management-phase2` (PR #86).

---

## Context for the implementer (from the seam-map research — verify line numbers, they drift)

- `App.messagepane messages.Model` is a VALUE field (app.go:91), constructed once (`messages.New(nil, "")`, app.go:404). All 85 model methods have pointer receivers; copying a Model value aliases its caches/maps and is unsafe — per-window storage must hold POINTERS.
- Global config that must be applied to every new model: `SetAvatarFunc` (app.go:1823), `SetUserNames` (2112), `SetChannelNames` (1744), `SetEmojiContext` (1843), `SetEmojiCustoms` (2198), `SetImageContext` (1831), `SetSpinnerFrame` (bootstrap.go:178), `SetFocused`.
- Channel-scoped event seams (all carry ChannelID): `MessagesLoadedMsg` (reducer_channels.go:77-111, currently DROPS non-active), `OlderMessagesLoadedMsg` (113-122), `NewMessageMsg` new/edit (reducer_send.go:204-310 / 207-225), `WSMessageDeletedMsg` (171-195), `MessageSentMsg`/`MessageSendFailedMsg`/`SendMessageMsg` optimistic (66-103, 315-368), `ThreadReplySentMsg` (reducer_threads.go:249), reactions (`updateReactionOnMessage` app.go:680-683 — channelID param currently UNUSED), `applyChannelMark` (app.go:2773-2780).
- Workspace/global seams to fan out: `PatchUserName` (reducer_workspace.go:112), `SetUserNames`/`SetChannelNames`/`SetEmojiCustoms`, `HandleEmojiImageReady` (reducer_io.go:218), `HandleAvatarReady` (233), `HandleImageReady` (181 — routes by channel NAME; model self-gates), `HandleImageFailed` (245), `InvalidateCache` (mode_theme_switcher.go:44, reducer_workspace.go:180/286), `SetSpinnerFrame`, `clearSelections` (app.go:1480).
- `a.fetchingOlder bool` (app.go:180) is a singleton in-flight flag for history backfill — becomes per-channel.
- `reduceChannelSelected` (reducer_channels.go:196-339) is the focused-window selection path: it sets `activeChannelID`, compose/statusbar/typing retargets, nav history, membership fetch, three-tier cache load. It keeps operating on `a.messagepane` (= focused model) — mostly unchanged.
- Phase 2 interim code that THIS phase deletes: `focusWindow`'s ChannelSelectedMsg re-dispatch + the KNOWN PHASE 2 LIMITATION comment (windows.go), placeholder rendering (view_window_region.go), `TestFocusWindow_DifferentChannelDispatchesSelection`, `TestRegion_SplitRendersPlaceholderWithChannelName`.
- Render caches: single `msgTop`/`msgPanel` slots (panelcache.go:78-85). Live unfocused panes need a per-window cache map, evicted on close/only/workspace reset.
- `ActiveChannelID()` feeds cmd/slk notification suppression + has_unread skip (main.go:1538, 2925, 2963). Phase 3 DECISION (deliberate): it keeps meaning the FOCUSED window's channel. Notifications for visible-but-unfocused channels still fire; spec's read-state rule ("only the focused window advances the read marker") stays satisfied because mark-read happens only on focused-window selection/entry.
- Mouse: the Phase 2 split-mode click guard (reducer_mouse.go:236) STAYS — per-window mouse routing is Phase 4.

Run tests from repo root: `go test ./internal/ui/... -count=1`.

---

### Task 1: Per-window model store + focused-pointer invariant

**Files:**
- Create: `internal/ui/winmodels.go`
- Modify: `internal/ui/app.go` (field type change + NewApp init)
- Modify: `internal/ui/windows.go` (split/close/only/focus rewires)
- Modify: `internal/ui/reducer_workspace.go` (reset rewire)
- Test: `internal/ui/winmodels_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/ui/winmodels_test.go`:

```go
package ui

import (
	"testing"

	"github.com/gammons/slk/internal/ui/messages"
	"github.com/gammons/slk/internal/ui/wintree"
)

func TestWinModels_RootWindowHasModelAndPointerInvariant(t *testing.T) {
	a := newWideTestApp(t)
	if a.winModels[a.focusedWin] == nil {
		t.Fatal("root window must have a model at construction")
	}
	if a.messagepane != a.winModels[a.focusedWin] {
		t.Fatal("messagepane must point at the focused window's model")
	}
}

func TestSplitWindow_NewWindowGetsSeededClone(t *testing.T) {
	a := newWideTestApp(t)
	_, _ = a.Update(ChannelSelectedMsg{ID: "C1", Name: "general", Type: "channel"})
	a.messagepane.SetMessages([]messages.MessageItem{
		{TS: "1.0", UserName: "alice", UserID: "U1", Text: "hello", Timestamp: "1:00 PM"},
	})
	src := a.messagepane
	_ = a.splitWindow(wintree.SplitSideBySide)
	if a.messagepane == src {
		t.Fatal("focused model must be the NEW window's model after split")
	}
	if got := len(a.messagepane.Messages()); got != 1 {
		t.Fatalf("new window should be seeded with the source's messages, got %d", got)
	}
	if a.winModels[a.focusedWin] != a.messagepane {
		t.Fatal("pointer invariant broken after split")
	}
}

func TestFocusWindow_IsPointerSwapNoDispatch(t *testing.T) {
	a := newWideTestApp(t)
	_, _ = a.Update(ChannelSelectedMsg{ID: "C1", Name: "general", Type: "channel"})
	first := a.focusedWin
	_ = a.splitWindow(wintree.SplitSideBySide)
	_, _ = a.Update(ChannelSelectedMsg{ID: "C2", Name: "ops", Type: "channel"})
	// Focus back: NO ChannelSelectedMsg dispatch (per-window models),
	// but active-channel context retargets to the window's channel.
	cmd := a.focusWindow(first)
	if cmd != nil {
		t.Fatal("focusWindow must not dispatch channel selection in Phase 3")
	}
	if a.activeChannelID != "C1" {
		t.Fatalf("activeChannelID = %q, want C1 (focused window's channel)", a.activeChannelID)
	}
	if a.messagepane != a.winModels[first] {
		t.Fatal("messagepane must follow focus")
	}
}

func TestCloseWindow_EvictsModel(t *testing.T) {
	a := newWideTestApp(t)
	_ = a.splitWindow(wintree.SplitSideBySide)
	closed := a.focusedWin
	_ = a.closeWindow()
	if _, ok := a.winModels[closed]; ok {
		t.Fatal("closed window's model must be evicted")
	}
	if len(a.winModels) != a.wins.Len() {
		t.Fatalf("winModels len %d != tree len %d", len(a.winModels), a.wins.Len())
	}
}

func TestOnlyWindow_EvictsOthers(t *testing.T) {
	a := newWideTestApp(t)
	_ = a.splitWindow(wintree.SplitSideBySide)
	_ = a.splitWindow(wintree.SplitStacked)
	a.onlyWindow()
	if len(a.winModels) != 1 {
		t.Fatalf("winModels len = %d, want 1 after :only", len(a.winModels))
	}
	if a.winModels[a.focusedWin] != a.messagepane {
		t.Fatal("pointer invariant broken after :only")
	}
}

func TestWorkspaceSwitch_RebuildsModels(t *testing.T) {
	a := newWideTestApp(t)
	_ = a.splitWindow(wintree.SplitSideBySide)
	_, _ = a.Update(WorkspaceSwitchedMsg{TeamID: "T2", TeamName: "Other", Channels: nil})
	if len(a.winModels) != 1 {
		t.Fatalf("winModels len = %d, want 1 after workspace switch", len(a.winModels))
	}
	if a.messagepane == nil || a.messagepane != a.winModels[a.focusedWin] {
		t.Fatal("pointer invariant broken after workspace switch")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestWinModels|TestSplitWindow_NewWindowGetsSeededClone|TestFocusWindow_IsPointerSwap|TestCloseWindow_EvictsModel|TestOnlyWindow_EvictsOthers|TestWorkspaceSwitch_RebuildsModels' -v`
Expected: compile error — `a.winModels` undefined.

- [ ] **Step 3: Change the field and add the store**

In `internal/ui/app.go`:
- Change `messagepane messages.Model` to `messagepane *messages.Model` and add, next to the `wins`/`focusedWin` fields:

```go
	// winModels holds one live messages.Model per window (Phase 3).
	// INVARIANT: messagepane == winModels[focusedWin] always; keys
	// exactly match wins.Leaves(). Maintained by newWindowModel /
	// focusWindow / closeWindow / onlyWindow / resetWindowTree.
	winModels map[wintree.LeafID]*messages.Model
```

- In `NewApp`, replace the `messagepane: messages.New(nil, "")` init: construct the tree first (it already is), then build the map and pointer. Because NewApp uses a composite literal, do this after the literal:

```go
	rootModel := messages.New(nil, "")
	a.winModels = map[wintree.LeafID]*messages.Model{rootWin: &rootModel}
	a.messagepane = a.winModels[rootWin]
```

- **Compile sweep:** `go build ./...` will surface every place that breaks from the value→pointer change. Expected: assignments (none besides NewApp), any `&a.messagepane` (none known), and tests doing `a.messagepane.X` keep compiling. Fix what surfaces; report anything surprising rather than hacking around it.
- Where NewApp previously applied global config to `messagepane` directly, leave as-is for now — those call sites (`a.messagepane.SetAvatarFunc(...)` etc.) still work through the pointer; Task 3 turns them into fan-out loops.

Create `internal/ui/winmodels.go`:

```go
// internal/ui/winmodels.go
//
// Per-window messages.Model store (window-management design §2,
// Phase 3). Each window in the tree owns a live model; a.messagepane
// always aliases the focused window's model so the ~100 existing
// focused-window call sites keep their semantics.
package ui

import (
	"github.com/gammons/slk/internal/ui/messages"
	"github.com/gammons/slk/internal/ui/wintree"
)

// newWindowModel constructs a model for a new window, applying the
// app-global configuration every pane needs. Mirror the Set* calls
// made on the root model during NewApp/bootstrap — if you find one
// not listed here, add it and note it in your report:
// SetAvatarFunc, SetUserNames, SetChannelNames, SetEmojiContext,
// SetEmojiCustoms, SetImageContext, SetSpinnerFrame.
func (a *App) newWindowModel(chName string) *messages.Model {
	m := messages.New(nil, chName)
	m.SetAvatarFunc(a.avatarFnForModels())
	m.SetUserNames(a.userNames)
	m.SetChannelNames(a.channelNamesForModels())
	m.SetEmojiContext(a.emojiCtxForModels())
	m.SetEmojiCustoms(a.emojiCustomsForModels())
	m.SetImageContext(a.imageCtxForModels())
	m.SetSpinnerFrame(a.spinnerFrame)
	return &m
}

// NOTE to implementer: the six a.*ForModels() accessors above are
// PSEUDOCODE for "the same values NewApp/bootstrap passed to the root
// model". Read where SetAvatarFunc / SetChannelNames / SetEmojiContext /
// SetEmojiCustoms / SetImageContext are called today (app.go:1823,
// 1744, 1843, 2198, 1831) and either (a) store those values on App
// fields when first set so newWindowModel can re-apply them, or
// (b) if they're already App fields, use them directly. Choose the
// smallest change; do NOT duplicate construction logic.

// modelsForChannel returns the models of every window viewing chID,
// in tree order. Used by channel-scoped event fan-out.
func (a *App) modelsForChannel(chID string) []*messages.Model {
	if chID == "" {
		return nil
	}
	var out []*messages.Model
	for _, id := range a.wins.Leaves() {
		if ch, ok := a.wins.Channel(id); ok && ch.ID == chID {
			if m := a.winModels[id]; m != nil {
				out = append(out, m)
			}
		}
	}
	return out
}

// allWinModels returns every window's model in tree order. Used by
// workspace/global fan-out (names, emoji, theme, spinner...).
func (a *App) allWinModels() []*messages.Model {
	out := make([]*messages.Model, 0, len(a.winModels))
	for _, id := range a.wins.Leaves() {
		if m := a.winModels[id]; m != nil {
			out = append(out, m)
		}
	}
	return out
}

// syncWinModels reconciles the model map with the tree after a
// structural change (close/only): evicts models for vanished windows.
// (Additions happen explicitly in splitWindow.)
func (a *App) syncWinModels() {
	live := make(map[wintree.LeafID]bool, a.wins.Len())
	for _, id := range a.wins.Leaves() {
		live[id] = true
	}
	for id := range a.winModels {
		if !live[id] {
			delete(a.winModels, id)
		}
	}
}

// resetWindowTree rebuilds the tree + model store to a single empty
// window (workspace switch). Replaces the inline reset in
// reduceWorkspaceSwitched.
func (a *App) resetWindowTree() {
	wins, rootWin := wintree.New(wintree.Channel{})
	a.wins = wins
	a.focusedWin = rootWin
	rootModel := a.newWindowModel("")
	a.winModels = map[wintree.LeafID]*messages.Model{rootWin: rootModel}
	a.messagepane = rootModel
}
```

- [ ] **Step 4: Rewire windows.go**

In `internal/ui/windows.go`:

1. `splitWindow`: after a successful split, create + seed the new model from the source window's model (instant content, same channel), maintain the invariant:

```go
func (a *App) splitWindow(dir wintree.Dir) tea.Cmd {
	src := a.messagepane
	srcCh, _ := a.wins.Channel(a.focusedWin)
	id, err := a.wins.Split(a.focusedWin, dir, a.windowBounds())
	if err != nil {
		return toastWithClear(a, "Not enough room", 2*time.Second)
	}
	m := a.newWindowModel(srcCh.Name)
	m.SetChannel(srcCh.Name, "")
	if src != nil {
		m.SetMessages(src.Messages())
	}
	a.winModels[id] = m
	a.focusedWin = id
	a.messagepane = m
	a.focusedPanel = PanelMessages
	return nil
}
```

(Check whether `messages.Model` has `SetChannelType` state worth cloning and a `Messages()` accessor returning a copy vs the live slice — read `Messages()`; if it returns the internal slice, copy it before SetMessages to avoid aliasing two models' content. Also clone `SetLastReadTS` if there's a getter; if no getter exists, add `LastReadTS() string` to messages/model.go — one-liner.)

2. `closeWindow`: after `a.wins.Close`, call `a.syncWinModels()` then `a.focusWindow(next)` (which now swaps the pointer).

3. `onlyWindow`: after `a.wins.Only`, call `a.syncWinModels()`.

4. `focusWindow` — full rewrite (this DELETES the Phase 2 re-dispatch and its KNOWN LIMITATION comment):

```go
// focusWindow moves window focus to id: a pointer swap plus an
// active-channel context retarget (compose, statusbar, typing,
// activeChannelID). Per-window models mean no channel re-dispatch.
func (a *App) focusWindow(id wintree.LeafID) tea.Cmd {
	if id == a.focusedWin {
		return nil
	}
	m := a.winModels[id]
	if m == nil {
		return nil // unknown window; invariant breach, ignore
	}
	a.focusedWin = id
	a.messagepane = m
	a.focusedPanel = PanelMessages
	if a.threadVisible {
		a.CloseThread() // spec §7: thread follows focused window
	}
	if ch, ok := a.wins.Channel(id); ok && ch.ID != "" && ch.ID != a.activeChannelID {
		a.retargetActiveChannel(ch)
	}
	return nil
}
```

5. Add `retargetActiveChannel` — factor the active-channel context updates OUT of `reduceChannelSelected` so both paths share them. Read reducer_channels.go:226-273 and extract the pieces that re-point the UI at a channel WITHOUT loading content or recording navigation: `a.activeChannelID = ch.ID`, `a.typingOut.ResetThrottle()`, `a.compose.SetChannel(ch.Name)`, `a.compose.SetActiveChannel(ch.ID)`, `a.threadCompose.SetActiveChannel(ch.ID)`, `a.statusbar.SetChannel(ch.Name)`, `a.statusbar.SetChannelType(ch.Type)`, and the membership fetch goroutine. NOT extracted (selection-only semantics): nav-history push, `channels.RecordVisit`, CloseThread/clearSelections ordering, the three-tier load, mark-read. `reduceChannelSelected` calls the new helper at the equivalent point; behavior there must be unchanged (existing channel-switch tests are the guard).

```go
// retargetActiveChannel re-points compose/statusbar/typing and
// activeChannelID at ch. Shared by channel selection (focused window)
// and window-focus changes. Content loading is NOT done here.
func (a *App) retargetActiveChannel(ch wintree.Channel) {
	// ...extracted body per above; signature may take (id, name, chType
	// string) instead if wintree.Channel is awkward at the reducer site.
}
```

6. In `reducer_workspace.go`, replace the inline tree reset (the Phase 2 block constructing `wintree.New`) with `a.resetWindowTree()`.

- [ ] **Step 5: Run the new tests**

Run: `go test ./internal/ui/ -run 'TestWinModels|TestSplitWindow_NewWindowGetsSeededClone|TestFocusWindow_IsPointerSwap|TestCloseWindow_EvictsModel|TestOnlyWindow_EvictsOthers|TestWorkspaceSwitch_RebuildsModels' -count=1 -v`
Expected: all PASS.

- [ ] **Step 6: Reconcile the Phase 2 tests this task obsoletes**

Run: `go test ./internal/ui/... -count=1`. Expected failures to handle:
- `TestFocusWindow_DifferentChannelDispatchesSelection` (windows_test.go): DELETE — it pins the removed re-dispatch mechanism (its replacement is `TestFocusWindow_IsPointerSwapNoDispatch`).
- Any test asserting `a.messagepane` value semantics: mechanical fixes only.
- Everything else must pass unmodified — especially the channel-switch flow tests in app_test.go (they guard the `retargetActiveChannel` extraction). Investigate regressions; do not weaken assertions.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): per-window messages models with focused-pointer invariant"
```

---

### Task 2: Channel-scoped event fan-out

**Files:**
- Modify: `internal/ui/reducer_channels.go`, `internal/ui/reducer_send.go`, `internal/ui/reducer_threads.go`, `internal/ui/app.go`
- Test: `internal/ui/fanout_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/ui/fanout_test.go`:

```go
package ui

import (
	"testing"

	"github.com/gammons/slk/internal/ui/wintree"
)

// twoWindowApp returns an app with window 1 on C1 and window 2
// (focused) on C2.
func twoWindowApp(t *testing.T) (*App, wintree.LeafID, wintree.LeafID) {
	t.Helper()
	a := newWideTestApp(t)
	_, _ = a.Update(ChannelSelectedMsg{ID: "C1", Name: "general", Type: "channel"})
	w1 := a.focusedWin
	_ = a.splitWindow(wintree.SplitSideBySide)
	w2 := a.focusedWin
	_, _ = a.Update(ChannelSelectedMsg{ID: "C2", Name: "ops", Type: "channel"})
	return a, w1, w2
}

func TestFanout_NewMessageReachesUnfocusedWindow(t *testing.T) {
	a, w1, _ := twoWindowApp(t)
	before := len(a.winModels[w1].Messages())
	_, _ = a.Update(NewMessageMsg{ChannelID: "C1", TS: "9.0", UserID: "U9", UserName: "zoe", Text: "ping"})
	if got := len(a.winModels[w1].Messages()); got != before+1 {
		t.Fatalf("unfocused window on C1 should receive the message: %d -> %d", before, got)
	}
}

func TestFanout_NewMessageDoesNotReachOtherChannelWindow(t *testing.T) {
	a, _, w2 := twoWindowApp(t)
	before := len(a.winModels[w2].Messages())
	_, _ = a.Update(NewMessageMsg{ChannelID: "C1", TS: "9.0", UserID: "U9", UserName: "zoe", Text: "ping"})
	if got := len(a.winModels[w2].Messages()); got != before {
		t.Fatalf("window on C2 must not receive a C1 message: %d -> %d", before, got)
	}
}

func TestFanout_SameChannelTwiceBothUpdate(t *testing.T) {
	a := newWideTestApp(t)
	_, _ = a.Update(ChannelSelectedMsg{ID: "C1", Name: "general", Type: "channel"})
	w1 := a.focusedWin
	_ = a.splitWindow(wintree.SplitSideBySide) // clone: both on C1
	w2 := a.focusedWin
	_, _ = a.Update(NewMessageMsg{ChannelID: "C1", TS: "9.0", UserID: "U9", UserName: "zoe", Text: "ping"})
	n1, n2 := len(a.winModels[w1].Messages()), len(a.winModels[w2].Messages())
	if n1 != n2 || n1 == 0 {
		t.Fatalf("both C1 windows must update: w1=%d w2=%d", n1, n2)
	}
}

func TestFanout_MessagesLoadedSeedsUnfocusedWindow(t *testing.T) {
	a, w1, _ := twoWindowApp(t)
	_, _ = a.Update(MessagesLoadedMsg{ChannelID: "C1", Messages: testMessageItems(3)})
	if got := len(a.winModels[w1].Messages()); got != 3 {
		t.Fatalf("MessagesLoaded for C1 must apply to the unfocused C1 window, got %d", got)
	}
}

func TestFanout_MarkReadOnlyOnFocusedSelection(t *testing.T) {
	// Spec §2 read-state rule: the unfocused window receiving realtime
	// traffic must NOT trigger a mark-read; only focused selection/entry
	// does. NewMessage to the unfocused C1 window → no MarkRead cmd.
	a, _, _ := twoWindowApp(t)
	_, cmd := a.Update(NewMessageMsg{ChannelID: "C1", TS: "9.0", UserID: "U9", UserName: "zoe", Text: "ping"})
	for _, msg := range drainBatch(cmd) {
		// Read reducer_send.go/services to learn what a MarkRead cmd
		// looks like; assert none is present for C1. If MarkRead is
		// not representable as a msg (it's a direct service call cmd),
		// assert via the seam the codebase offers — read first, then
		// implement the strongest available assertion and document it.
		_ = msg
	}
}
```

Add a tiny helper (in fanout_test.go) `testMessageItems(n int) []messages.MessageItem` building n items with distinct TS values. Read `NewMessageMsg`'s real field names in msgs.go first and fix the literals above if they differ (e.g. it may nest a MessageItem) — the INTENT of each test is binding, the literal shapes are not.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestFanout -v`
Expected: compile errors and/or failures showing events don't reach unfocused windows (the unfocused C1 window misses the append). Confirm each failure reason before continuing.

- [ ] **Step 3: Rewire the channel-scoped seams**

Pattern for every seam — replace the "gate on activeChannelID, apply to a.messagepane" shape with a fan-out loop. Example for `MessagesLoadedMsg` (reducer_channels.go:77-111):

```go
	// Before: if m.ChannelID != a.activeChannelID { drop }
	// After: apply to every window viewing the channel.
	models := a.modelsForChannel(m.ChannelID)
	if len(models) == 0 {
		// no window views this channel anymore — stale fetch, drop
		return nil, true
	}
	for _, mm := range models {
		mm.SetLoading(false)
		mm.SetLastReadTS(m.LastReadTS)
		if m.Messages != nil {
			mm.SetMessages(m.Messages)
		}
	}
```

Apply the same transformation to each seam (preserve each seam's existing nil-guards/dedup logic, looping only the model writes):
1. `MessagesLoadedMsg` — as above. PRESERVE the nil-messages "keep cache on network failure" branch per model.
2. `OlderMessagesLoadedMsg` (reducer_channels.go:113-122) — `PrependMessages` to all matching models; see Step 4 for the fetchingOlder flag.
3. `NewMessageMsg` new-message branch (reducer_send.go:252-272) — `AppendMessage`/`IncrementReplyCount` loop. The "inactive channel" notify branch (274-299) now means "no window views it" — compute via `len(a.modelsForChannel(...)) == 0`. IMPORTANT: a message arriving for a visible-but-unfocused window must still bump unread state for the sidebar (`notifyReadStateChanged`) because the read marker only advances on focused entry — read the branch carefully and keep sidebar/unread behavior for unfocused windows (the channel is visible but NOT read). Self-send dedup guards (`selfSend.IsSelfSent`/`InFlight`) stay App-level, checked once BEFORE the loop.
4. `NewMessageMsg` edit branch (207-225) — `UpdateMessageInPlace` loop.
5. `WSMessageDeletedMsg` (171-195) — `RemoveMessageByTS` loop.
6. `MessageSentMsg` (66-91) / `MessageSendFailedMsg` (93-103) / `SendMessageMsg` optimistic append (315-368) — loop. (Sends originate from the focused window, but a same-channel sibling window must also show the optimistic message.)
7. `ThreadReplySentMsg` (reducer_threads.go:249) — `IncrementReplyCount` loop.
8. `updateReactionOnMessage` (app.go:680-683) — now USE the channelID param: loop `a.modelsForChannel(channelID)` for the pane write; threadPanel write unchanged.
9. `applyChannelMark` (app.go:2773-2780) — `SetLastReadTS` loop (replaces the activeChannelID gate).

- [ ] **Step 4: Per-channel fetchingOlder**

Change `a.fetchingOlder bool` (app.go:180) to `fetchingOlder map[string]bool` keyed by channel ID (init in NewApp). `maybeFetchOlderHistory` (app.go:1302-1322) gates and sets per `a.activeChannelID` (backfill is triggered by focused-window scrolling — focused semantics are correct); `OlderMessagesLoadedMsg` clears `a.fetchingOlder[m.ChannelID]`. Check `app_olderfetch_spinner_test.go` — fix mechanically if it touches the flag directly.

- [ ] **Step 5: Run the fan-out tests + full suite**

Run: `go test ./internal/ui/ -run TestFanout -count=1 -v` then `go test ./internal/ui/... -count=1`
Expected: all PASS. The link-nav tests (reducer_links_test.go) gate on activeChannelID and should still pass since permalink nav targets the focused window; investigate any failure.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): fan channel-scoped events out to all windows viewing the channel"
```

---

### Task 3: Workspace/global fan-out

**Files:**
- Modify: `internal/ui/app.go`, `internal/ui/reducer_io.go`, `internal/ui/reducer_workspace.go`, `internal/ui/mode_theme_switcher.go`, `internal/ui/bootstrap.go`
- Test: `internal/ui/fanout_global_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/ui/fanout_global_test.go`:

```go
package ui

import (
	"testing"
)

func TestGlobalFanout_UserNamesReachAllWindows(t *testing.T) {
	a, w1, w2 := twoWindowApp(t)
	a.SetUserNames(map[string]string{"U7": "newname"})
	if got := a.winModels[w1].ResolveUserName("U7"); got != "newname" {
		t.Fatalf("w1 ResolveUserName = %q", got)
	}
	if got := a.winModels[w2].ResolveUserName("U7"); got != "newname" {
		t.Fatalf("w2 ResolveUserName = %q", got)
	}
}

func TestGlobalFanout_ThemeInvalidationBumpsAllVersions(t *testing.T) {
	a, w1, w2 := twoWindowApp(t)
	v1, v2 := a.winModels[w1].Version(), a.winModels[w2].Version()
	// Use the same entry point the theme switcher uses (read
	// mode_theme_switcher.go:44 and call the App-level seam it hits).
	a.invalidateAllWinModelCaches()
	if a.winModels[w1].Version() == v1 || a.winModels[w2].Version() == v2 {
		t.Fatal("theme invalidation must bump every window model's version")
	}
}

func TestGlobalFanout_SpinnerFrameReachesAllLoadingWindows(t *testing.T) {
	a, w1, w2 := twoWindowApp(t)
	a.winModels[w1].SetLoading(true)
	a.winModels[w2].SetLoading(true)
	v1, v2 := a.winModels[w1].Version(), a.winModels[w2].Version()
	a.advanceSpinnerForTests() // see Step 3: expose or drive via the real tick msg
	if a.winModels[w1].Version() == v1 || a.winModels[w2].Version() == v2 {
		t.Fatal("spinner frame must reach all loading windows")
	}
}
```

Adapt the three entry points to what the code actually exposes (read first): `SetUserNames` may be a method on App or inline in a reducer; the theme seam may be best tested through the real msg. The test INTENT is binding: each global mutation must reach every window's model. Use the real public seams where possible (e.g. `a.Update(themeMsg)`), helper methods only as a last resort.

- [ ] **Step 2: Verify failures**

Run: `go test ./internal/ui/ -run TestGlobalFanout -v` — confirm each fails because only the focused model updates.

- [ ] **Step 3: Convert the global seams to fan-out loops**

For each, replace `a.messagepane.X(...)` with `for _, m := range a.allWinModels() { m.X(...) }`:
- `SetUserNames` (app.go:2112), `SetChannelNames` (app.go:1744 in SetChannels), `SetEmojiCustoms` (app.go:2198)
- `PatchUserName` (reducer_workspace.go:112)
- `HandleEmojiImageReady` (reducer_io.go:218), `HandleAvatarReady` (233), `HandleImageReady` (181 — fan out; each model self-gates by channel name), `HandleImageFailed` (245)
- Theme invalidation: `InvalidateCache` calls (mode_theme_switcher.go:44, reducer_workspace.go:180, :286) → add `invalidateAllWinModelCaches()` helper on App and use it at all three sites
- Spinner: bootstrap.go:173/:178 — `IsLoading` becomes "any model loading", `SetSpinnerFrame` loops all models. For the test, prefer driving the REAL spinner tick msg through `a.Update` (find the tick msg type in bootstrap.go) over inventing `advanceSpinnerForTests`; only add a helper if the tick path has untestable dependencies
- `clearSelections` (app.go:1480) — loop all models (+ threadPanel as today)
- NewApp/bootstrap wiring (`SetAvatarFunc` app.go:1823, `SetImageContext` 1831, `SetEmojiContext` 1843): these run once at startup — make sure the values are retained on App fields so `newWindowModel` (Task 1) applies them to later windows; convert the call sites to loops only if they can re-fire after startup (read each).

- [ ] **Step 4: Run + full suite**

Run: `go test ./internal/ui/ -run TestGlobalFanout -count=1 -v && go test ./internal/ui/... -count=1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): fan workspace/global model mutations out to every window"
```

---

### Task 4: Live unfocused panes + per-window render caches

**Files:**
- Modify: `internal/ui/view_window_region.go` (placeholder → live pane)
- Modify: `internal/ui/panelcache.go` (per-window cache map)
- Modify: `internal/ui/view_messages.go` (only if extracting a shared bordered-pane helper is needed)
- Test: `internal/ui/view_window_region_test.go` (modify)

- [ ] **Step 1: Update the region tests**

In `internal/ui/view_window_region_test.go`:
- REPLACE `TestRegion_SplitRendersPlaceholderWithChannelName` with:

```go
func TestRegion_SplitRendersLiveContentInBothWindows(t *testing.T) {
	a, w1, _ := twoWindowApp(t)
	_, _ = a.Update(MessagesLoadedMsg{ChannelID: "C1", Messages: testMessageItems(2)})
	_ = w1
	out := ansi.Strip(renderRegion(a))
	// Unfocused window (C1/general) must show real message text, not a
	// placeholder; focused window shows ops.
	if !strings.Contains(out, "msg-1") { // testMessageItems text pattern
		t.Fatalf("unfocused window should render its channel's messages:\n%s", out)
	}
	if strings.Contains(out, "(no channel)") {
		t.Fatal("no placeholders may remain in Phase 3")
	}
}

func TestRegion_UnfocusedWindowUpdatesOnNewMessage(t *testing.T) {
	a, _, _ := twoWindowApp(t)
	_, _ = a.Update(NewMessageMsg{ChannelID: "C1", TS: "9.0", UserID: "U9", UserName: "zoe", Text: "live-update-proof"})
	out := ansi.Strip(renderRegion(a))
	if !strings.Contains(out, "live-update-proof") {
		t.Fatalf("unfocused window must re-render new content:\n%s", out)
	}
}
```

(`testMessageItems` from fanout_test.go should give items with greppable text like "msg-1", "msg-2" — adjust it there if needed.)
- KEEP the dimension/shrink/single-window-identity tests unchanged — they must pass against live panes too (the invariants are renderer-shape-independent).

- [ ] **Step 2: Verify failures**

Run: `go test ./internal/ui/ -run TestRegion_ -v` — the two new tests fail (placeholder shows no message text). Confirm reasons.

- [ ] **Step 3: Implement live unfocused panes with per-window caches**

In `internal/ui/panelcache.go`, add a per-window cache map alongside the existing fields:

```go
	// winPanes caches the bordered output of unfocused live window
	// panes, one slot per window (Phase 3). Focused windows render
	// through msgTop/msgPanel as before. Evicted via dropWinPane.
	winPanes map[wintree.LeafID]*panelCache
```

(plus `getWinPane(id)` lazy-init and `dropWinPane(id)` helpers, and the wintree import; call `dropWinPane` from `syncWinModels`'s eviction loop and reset the map in `resetWindowTree` — wire through App since panelRenderCache is owned by App.)

In `internal/ui/view_window_region.go`, replace `renderPlaceholderWindow` with a live read-only pane:

```go
// renderUnfocusedWindow renders a live, read-only pane for an
// unfocused window: dimmed border, channel name in the title row,
// message content via ViewBare. No compose/typing rows (focused
// window only). Cached per window on (version, rect, themeVer).
func (a *App) renderUnfocusedWindow(n wintree.LayoutNode, themeVer int64) string {
	m := a.winModels[n.ID]
	if m == nil || n.Rect.W < 4 || n.Rect.H < 4 {
		return a.renderBlankWindow(n) // degenerate rect: exact-size blank
	}
	m.SetFocused(false) // before Version read; bumps only on flip
	c := a.renderCache.getWinPane(n.ID)
	key := themeVer << 1
	if c.hit(m.Version(), n.Rect.W, n.Rect.H, key) {
		return c.output
	}
	contentH := n.Rect.H - 2 // top+bottom border rows
	view := m.ViewBare(contentH, n.Rect.W-2)
	view = messages.ReapplyBgAfterResets(view, messages.BgANSI())
	out := exactSize(
		styles.UnfocusedBorder.Width(n.Rect.W-2).Render(view),
		n.Rect.W, n.Rect.H,
	)
	c.store(out, m.Version(), n.Rect.W, n.Rect.H, key)
	return out
}
```

Implementation notes (read before coding):
- The model renders its own channel-name header chrome (`ViewBare` includes it — verify by reading messages/model.go's chrome handling around :2692). If the header already shows the channel name, do NOT add a second title row. If it doesn't, render the name into the top border like the thread panel does (read view_thread.go for the pattern).
- `renderBlankWindow(n)`: the Phase 2 degenerate-rect blank (`exactSize("", W, H)` guarded for ≥1 dims) — keep that logic, renamed from the placeholder path.
- Keep ALL Phase 2 dimensional guards (zero-extent skip at parent, focused-leaf clamps) — the shrink tests enforce them.
- `renderWindowNode`'s focused-leaf branch is unchanged (full pane with compose via renderMessagesRegion).
- Focused-window `SetFocused(true)` already happens inside renderMessagesRegion (view_messages.go:71). Unfocused models get `SetFocused(false)` here.

- [ ] **Step 4: Run + full suite + benches compile**

Run: `go test ./internal/ui/ -run TestRegion_ -count=1 -v && go test ./internal/ui/... -count=1 && go vet ./internal/ui/...`
Expected: all PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): live unfocused window panes with per-window render caches"
```

---

### Task 5: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Full suite, vet, build, race spot-check**

Run: `go test ./... -count=1 && go vet ./... && go build ./cmd/slk && rm -f slk && go test ./internal/ui/ -count=1 -race -run 'TestFanout|TestWinModels|TestRegion_'`
Expected: everything passes; race detector clean on the new paths.

- [ ] **Step 2: gofmt on touched files**

Run: `gofmt -l internal/ui/winmodels.go internal/ui/windows.go internal/ui/view_window_region.go internal/ui/panelcache.go internal/ui/reducer_channels.go internal/ui/reducer_send.go internal/ui/reducer_io.go internal/ui/fanout_test.go internal/ui/fanout_global_test.go internal/ui/winmodels_test.go`
Expected: no output (pre-existing repo-wide dirt excluded).

- [ ] **Step 3: Manual smoke (requires configured slk)**

1. `:vsp`, pick a different channel in each window → BOTH update live as messages arrive
2. `ctrl+w h/l` between windows → instant (no reload flicker), compose targets the focused window's channel, statusbar follows
3. Send a message in window A; a same-channel window B shows it (optimistic + confirmed)
4. Reactions/edits/deletes in an unfocused window's channel appear without focusing it
5. Unread badge: traffic to an unfocused window's channel still shows sidebar unread until you focus+enter it (read marker only advances on focused selection)
6. Scroll-up backfill works independently after focusing each window; `:only` then everything still works
7. Workspace switch (`1`/`2`) → single window, correct channel, no ghosts; theme switch recolors all windows

- [ ] **Step 4: Report Phase 3 complete**

Phase 4 (mouse routing, drag-resize, resize chords, focus-cycle window-walk, thread polish) gets its own plan once this lands.
