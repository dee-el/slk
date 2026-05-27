# Phase 7: Thread Pane

> Index: `00-overview.md`. Previous: `06-messages-and-reactions.md`. Next: `08-picker.md`.

**Goal:** Apply the same image-emoji rendering to the thread pane — both reply body text and reply-level reaction pills. After this phase, opening a thread on a kitty terminal with `emoji_images = "on"` shows image emoji in every reply and pill.

**Scope:**
- `internal/ui/thread/model.go:1646` (body text render via `messages.RenderSlackMarkdown`).
- `internal/ui/thread/model.go:1679` (per-pill emoji resolution — the same `kyoemoji.Sprint` + `ShouldRenderUnicode` pattern as the messages pane).
- New `Model.SetEmojiContext` + `HandleEmojiImageReady` on the thread model.
- Reducer arm in `reducer_io.go` already-pending TODO from Phase 6 Task 6.8 (the `a.threadPanel.HandleEmojiImageReady(m.URL)` call we left commented out).

**Files:**
- Modify: `internal/ui/thread/model.go`
- Modify: `internal/ui/thread/model_test.go`
- Modify: `internal/ui/app.go` (forward `SetEmojiContext` to threadPanel; update `SetCustomEmoji` to call `threadPanel.SetEmojiCustoms`)
- Modify: `internal/ui/reducer_io.go` (uncomment the thread invalidation arm)
- Modify: `cmd/slk/main.go` (call `app.SetEmojiContext` on the thread pane alongside the messages pane)

---

### Task 7.1: Failing test — Thread Model exposes `SetEmojiContext` + `HandleEmojiImageReady`

**Files:**
- Modify: `internal/ui/thread/model_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/thread/model_test.go`:

```go
import (
	// ... existing
	emojiutil "github.com/gammons/slk/internal/emoji"
	"github.com/gammons/slk/internal/ui/messages"
)

func TestThreadModel_SetEmojiContext_InvalidatesCache(t *testing.T) {
	m := NewModel()
	// Drive the model into a state where its cache is populated. The
	// thread cache concept mirrors the messages-pane cache; the exact
	// method to populate it depends on existing test helpers — use the
	// same pattern as TestThreadModel_HandleImageReady_InvalidatesCache
	// (search the file for that test or the closest analog).
	// At minimum: SetReplies(...) + View().
	m.SetReplies("C1", "1.0", []messages.MessageItem{
		{TS: "1.1", UserName: "alice", UserID: "U1", Text: "hi"},
	})
	_ = m.View()

	startVersion := m.Version()
	m.SetEmojiContext(EmojiContext{
		PlaceCtx: emojiutil.PlaceContext{},
		Cells:    2,
		Customs:  nil,
	})
	if m.Version() == startVersion {
		t.Errorf("SetEmojiContext did not bump thread cache version")
	}
}

func TestThreadModel_HandleEmojiImageReady_BumpsVersion(t *testing.T) {
	m := NewModel()
	m.SetReplies("C1", "1.0", []messages.MessageItem{
		{TS: "1.1", UserName: "alice", UserID: "U1", Text: "hi"},
	})
	_ = m.View()

	v0 := m.Version()
	m.HandleEmojiImageReady("https://example.com/x.png")
	if m.Version() == v0 {
		t.Errorf("HandleEmojiImageReady did not bump thread cache version")
	}
}
```

(If the thread model uses `InvalidateCache()` rather than a Version counter, adapt the assertion accordingly — check what existing `TestImageReadyMsg_RoutesToThread` does in `internal/ui/app_thread_image_test.go` for the right pattern.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/thread/ -run TestThreadModel_SetEmojiContext -v`
Expected: FAIL — `SetEmojiContext`, `EmojiContext`, and `HandleEmojiImageReady` are undefined on the thread Model.

---

### Task 7.2: Add `EmojiContext`, `SetEmojiContext`, `SetEmojiCustoms`, `HandleEmojiImageReady` to thread Model

**Files:**
- Modify: `internal/ui/thread/model.go`

- [ ] **Step 1: Add the type and methods**

In `internal/ui/thread/model.go`, near the existing image-context setter (find via `grep SetImageContext internal/ui/thread/model.go`), add:

```go
// EmojiContext bundles the emoji-image rendering dependencies for the
// thread pane. Mirrors messages.EmojiContext in shape and purpose —
// see internal/ui/messages/model.go for the architectural rationale.
type EmojiContext struct {
	PlaceCtx emojiutil.PlaceContext
	Cells    int
	Customs  map[string]string
}

// SetEmojiContext configures emoji-image rendering for thread replies.
// Should be called once at startup (cmd/slk/main.go) and again after
// CustomEmojisLoadedMsg arrives (via SetEmojiCustoms).
func (m *Model) SetEmojiContext(ctx EmojiContext) {
	if ctx.Cells != 1 && ctx.Cells != 2 {
		ctx.Cells = 2
	}
	m.emojiCtx = ctx
	m.InvalidateCache()
}

// SetEmojiCustoms updates the customs map on the active emoji
// context. Invalidates the cache so the next View() picks up the
// new map.
func (m *Model) SetEmojiCustoms(customs map[string]string) {
	m.emojiCtx.Customs = customs
	m.InvalidateCache()
}

// HandleEmojiImageReady is invoked when emoji.EmojiImageReadyMsg
// lands. Coarse invalidation — same emoji can appear anywhere in
// the open thread. v1 nukes the cache; the next View() rebuilds
// with the now-warm emoji placement.
func (m *Model) HandleEmojiImageReady(url string) {
	debuglog.ImgFetch("thread.HandleEmojiImageReady: url=%s wholesale_invalidate", url)
	m.InvalidateCache()
}
```

Add the `emojiCtx EmojiContext` field to the `Model` struct (find the struct definition; it's near the top of the file).

If `InvalidateCache()` doesn't exist as a public method but the existing image-ready handler manipulates a `version` counter directly, mirror that pattern instead.

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./internal/ui/thread/ -run TestThreadModel -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/thread/model.go internal/ui/thread/model_test.go
git commit -m "feat(thread): EmojiContext + setters + HandleEmojiImageReady"
```

---

### Task 7.3: Plumb opts into the thread body-text render

**Files:**
- Modify: `internal/ui/thread/model.go`

- [ ] **Step 1: Update the body-text render call**

Locate the body-text render in `internal/ui/thread/model.go` (around line 1646):

```go
	text := styles.MessageText.Render(messages.WordWrap(messages.RenderSlackMarkdown(messages.MessageTextSource(msg), userNames, channelNames), contentWidth))
```

Replace with the opts version. Find the function this lives in (likely `renderThreadMessage` or similar); the function should already have a `flushes []func(io.Writer) error` accumulator (mirroring the messages pane's pattern). If not, add one to the function's named returns or local state — search for `Flushes` in the same function body for the existing pattern.

```go
	bodyOpts := messages.RenderSlackMarkdownOpts{
		UserNames:    userNames,
		ChannelNames: channelNames,
		PlaceCtx:     m.emojiCtx.PlaceCtx,
		EmojiCells:   m.emojiCtx.Cells,
		Customs:      m.emojiCtx.Customs,
		EmojiFlushes: &flushes,
	}
	text := styles.MessageText.Render(messages.WordWrap(messages.RenderSlackMarkdownWith(messages.MessageTextSource(msg), bodyOpts), contentWidth))
```

If the thread render function doesn't currently collect flushes (because inline images in threads were "out of scope for v1" per a comment in the inline-images plan), this task introduces one — name it `flushes` and append it to the per-frame flush slice the thread pane already emits. Search the file for `Flushes` to find the existing emit point and the per-entry struct shape.

- [ ] **Step 2: Build and confirm**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/thread/model.go
git commit -m "feat(thread): plumb EmojiContext into reply body-text rendering"
```

---

### Task 7.4: Update thread reaction pill construction

**Files:**
- Modify: `internal/ui/thread/model.go`

- [ ] **Step 1: Replace the per-pill emoji resolution**

In `internal/ui/thread/model.go`, locate the reaction pill construction (around line 1657-1690, matching the pattern at messages/model.go:1764). The existing code is structurally identical to the messages pane:

```go
		for i, r := range msg.Reactions {
			nameForLookup := emojiutil.StripSkinTone(r.Emoji)
			resolved := kyoemoji.Sprint(":" + nameForLookup + ":")
			var emojiStr string
			if emojiutil.ShouldRenderUnicode(resolved) {
				emojiStr = resolved
			} else {
				emojiStr = ":" + nameForLookup + ":"
			}
			pillText := fmt.Sprintf("%s%d", emojiStr, r.Count)
			// ... style, append
		}
```

Replace with the image-aware version (identical shape to the messages pane version in Phase 6 Task 6.7):

```go
		imageOK := emojiutil.ImageModeActive() && m.emojiCtx.PlaceCtx.Fetcher != nil
		cells := m.emojiCtx.Cells
		if cells <= 0 {
			cells = 2
		}
		for i, r := range msg.Reactions {
			nameForLookup := emojiutil.StripSkinTone(r.Emoji)
			var emojiStr string
			placedFlush := (func(io.Writer) error)(nil)
			if imageOK {
				if url, ok := emojiutil.URLForShortcode(nameForLookup, m.emojiCtx.Customs); ok {
					if placement, flush, ok := emojiutil.Place(m.emojiCtx.PlaceCtx, url, cells); ok {
						emojiStr = placement
						placedFlush = flush
					}
				}
			}
			if emojiStr == "" {
				resolved := kyoemoji.Sprint(":" + nameForLookup + ":")
				if emojiutil.ShouldRenderUnicode(resolved) {
					emojiStr = resolved
				} else {
					emojiStr = ":" + nameForLookup + ":"
				}
			}
			pillText := fmt.Sprintf("%s%d", emojiStr, r.Count)
			// ... existing style + append (preserve verbatim)
			if placedFlush != nil {
				flushes = append(flushes, placedFlush)
			}
		}
```

- [ ] **Step 2: Build and test**

Run: `go build ./...` and `go test ./internal/ui/thread/ -v -count=1`
Expected: clean / no new failures.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/thread/model.go
git commit -m "feat(thread): render reply reaction pills as image emoji"
```

---

### Task 7.5: Wire `App.SetEmojiContext` to forward to threadPanel

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Update the forwarding accessor**

Replace the `SetEmojiContext` accessor on `App` (added in Phase 6 Task 6.9):

```go
// SetEmojiContext forwards the emoji rendering context to both the
// messages pane and the thread pane. They each hold their own copy
// because they have independent render caches and call paths.
func (a *App) SetEmojiContext(ctx messages.EmojiContext) {
	a.messagepane.SetEmojiContext(ctx)
	a.threadPanel.SetEmojiContext(thread.EmojiContext{
		PlaceCtx: ctx.PlaceCtx,
		Cells:    ctx.Cells,
		Customs:  ctx.Customs,
	})
}
```

Add the `thread` import if not present.

- [ ] **Step 2: Update `SetCustomEmoji` to forward to thread**

In the same file, the `SetCustomEmoji` body (modified in Phase 6 Task 6.9) gets a thread call appended:

```go
func (a *App) SetCustomEmoji(customs map[string]string) {
	// ... existing body
	a.messagepane.SetEmojiCustoms(customs)
	a.threadPanel.SetEmojiCustoms(customs)
}
```

- [ ] **Step 3: Build and test**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/app.go
git commit -m "feat(app): forward emoji context + customs to thread pane"
```

---

### Task 7.6: Uncomment the reducer arm for thread invalidation

**Files:**
- Modify: `internal/ui/reducer_io.go`

- [ ] **Step 1: Enable the thread invalidation**

Locate the `EmojiImageReadyMsg` case added in Phase 6 Task 6.8. Replace the placeholder comment with the active call:

```go
	case EmojiImageReadyMsg:
		debuglog.ImgFetch("recv: kind=emoji-ready url=%s", m.URL)
		a.messagepane.HandleEmojiImageReady(m.URL)
		a.threadPanel.HandleEmojiImageReady(m.URL)
		// Phase 8 adds picker invalidation. Phase 9 adds autocomplete.
		return nil, true
```

- [ ] **Step 2: Build and test**

Run: `go build ./...`
Expected: clean.

Run: `go test ./internal/ui/ -v -count=1`
Expected: no new failures.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/reducer_io.go
git commit -m "feat(ui): EmojiImageReadyMsg now invalidates the thread pane cache"
```

---

### Task 7.7: Integration test — thread reply with image emoji

**Files:**
- Modify: `internal/ui/thread/model_test.go`

- [ ] **Step 1: Write the test**

Append to `internal/ui/thread/model_test.go`:

```go
func TestThreadModel_RenderReplyWithImageEmoji_WarmCache(t *testing.T) {
	emojiutil.SetImageMode(true, 2)
	t.Cleanup(func() { emojiutil.SetImageMode(false, 2) })

	thumbURL := emojiutil.CDNBaseURL + "1f44d.png"

	// fakePlaceFetcher mirroring the one defined in messages/render_test.go.
	// Define a local one if the test packages don't share helpers.
	ff := newFakePlaceFetcher()
	ff.setPrerendered(emojiutil.EmojiCacheKey(thumbURL), goimage.Pt(2, 1), imgpkg.Render{
		Cells: goimage.Pt(2, 1),
		Lines: []string{"\U0010EEEE\U0010EEEE"},
	})

	m := NewModel()
	m.SetSize(80, 24)
	m.SetEmojiContext(EmojiContext{
		PlaceCtx: emojiutil.PlaceContext{Fetcher: ff},
		Cells:    2,
		Customs:  nil,
	})
	m.SetReplies("C1", "1.0", []messages.MessageItem{
		{TS: "1.1", UserName: "alice", UserID: "U1", Text: "reply :thumbsup:",
			Reactions: []messages.MessageReaction{{Emoji: "thumbsup", Count: 1}},
		},
	})

	out := m.View()
	if !strings.Contains(out, "\U0010EEEE") {
		t.Errorf("thread view does not contain kitty placeholder runes; image mode appears inactive\noutput=%q", out)
	}
	if strings.Contains(out, ":thumbsup:") {
		t.Errorf("thread view contains literal :thumbsup: text; image mode did not replace it\noutput=%q", out)
	}
}

// newFakePlaceFetcher / type fakePlaceFetcher — copy from
// internal/ui/messages/render_test.go (or factor into a shared
// testutil package if more than two test files want them; v1 just
// duplicates).
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/ui/thread/ -run TestThreadModel_RenderReplyWithImageEmoji -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/thread/model_test.go
git commit -m "test(thread): integration test for image emoji in reply body + pills"
```

---

### Task 7.8: Wire `app.SetEmojiContext` to the thread pane at startup

**Files:**
- Modify: `cmd/slk/main.go`

- [ ] **Step 1: Confirm the call is already in place**

The `app.SetEmojiContext(...)` call added in Phase 6 Task 6.9 step 4 already forwards to both panes via the updated forwarder in Phase 7 Task 7.5. No new call is needed; just confirm with:

Run: `grep -n 'SetEmojiContext' cmd/slk/main.go`
Expected: two lines — one with `buildPlaceCtx(nil)` (pre-Program), one with `buildPlaceCtx(p.Send)` (post-Program). Both forward to both panes.

If only one call exists, add the second per Phase 6 Task 6.9 step 4.

- [ ] **Step 2: Build and smoke-run**

Run: `go build ./...`
Expected: clean.

Run: `go run ./cmd/slk --help`
Expected: clean exit.

(No commit needed if no file changed.)

---

### Task 7.9: Final phase check

- [ ] **Step 1: Build the full project**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./... -count=1`
Expected: no new failures.

- [ ] **Step 3: Manual smoke (recommended)**

Run slk on real kitty. Open a channel with a threaded message and a reply containing an emoji + a reaction. Press `t` (or whatever the thread-open keybind is) to open the thread. Verify:
- Reply body emoji render as images.
- Reply reaction pills render as image emoji.
- Layout alignment is correct.

Phase 7 complete. Both main and thread panes render image emoji. Continue to `08-picker.md`.
