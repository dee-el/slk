# Phase 8: Reaction Picker

> Index: `00-overview.md`. Previous: `07-thread-pane.md`. Next: `09-autocomplete.md`.

**Goal:** Render image emoji in the reaction picker grid (`internal/ui/reactionpicker/`). Each visible row in the picker shows an emoji preview followed by its searchable name (`":thumbsup: thumbsup"` today). After this phase, the preview is a kitty image placement when image mode is active.

**Architectural note about flushes:** Unlike the messages pane, the picker doesn't have a per-frame `Flushes` slice walked by an outer renderer. Picker View() returns a string and the host composites it. To upload the kitty image bytes when the picker is the first/only consumer of a given emoji, the picker collects flush callbacks into a small per-View slice and invokes them at the end of View() against `image.KittyOutput` (the side-channel writer). In steady state most flushes are no-ops (`OnFlush=nil`) because the messages pane already triggered the upload — the registry deduplicates.

**Files:**
- Modify: `internal/ui/reactionpicker/model.go`
- Modify: `internal/ui/reactionpicker/model_test.go`
- Modify: `internal/ui/app.go` (forward `SetEmojiContext` to picker; add picker cache invalidation on `EmojiImageReadyMsg`)
- Modify: `internal/ui/reducer_io.go` (add picker arm)
- Modify: `cmd/slk/main.go` (no change if `App.SetEmojiContext` already covers it; otherwise add)

---

### Task 8.1: Add `EmojiContext` setter to picker

**Files:**
- Modify: `internal/ui/reactionpicker/model.go`

- [ ] **Step 1: Add types and setters**

Near the existing `SetCustomEmoji` (around line 69) in `internal/ui/reactionpicker/model.go`, add:

```go
// EmojiContext bundles the emoji-image rendering dependencies for the
// reaction picker. Set once at startup; updated again when the
// CustomEmojisLoadedMsg arrives via SetEmojiCustoms.
type EmojiContext struct {
	PlaceCtx slkemoji.PlaceContext
	Cells    int
	Customs  map[string]string
}

// SetEmojiContext configures emoji-image rendering for the picker.
// Mirrors messages.Model.SetEmojiContext.
func (m *Model) SetEmojiContext(ctx EmojiContext) {
	if ctx.Cells != 1 && ctx.Cells != 2 {
		ctx.Cells = 2
	}
	m.emojiCtx = ctx
}

// HandleEmojiImageReady forces the next View() to re-render so any
// emoji whose cold-cache fetch just completed picks up the warm
// placement. Picker has no render cache; this is currently a no-op
// hook for shape parity with messages/thread/autocomplete. Documented
// so future caching can drop in without changing the reducer arm.
func (m *Model) HandleEmojiImageReady(url string) {
	// no-op; picker re-evaluates Place on every View().
}
```

Add the `emojiCtx EmojiContext` field to the `Model` struct.

- [ ] **Step 2: Build to confirm**

Run: `go build ./internal/ui/reactionpicker/`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/reactionpicker/model.go
git commit -m "feat(reactionpicker): EmojiContext setter + HandleEmojiImageReady hook"
```

---

### Task 8.2: Failing test — picker rows render image placement when image mode is active

**Files:**
- Modify: `internal/ui/reactionpicker/model_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/reactionpicker/model_test.go`:

```go
import (
	// ... existing
	"context"
	goimage "image"
	slkemoji "github.com/gammons/slk/internal/emoji"
	imgpkg "github.com/gammons/slk/internal/image"
)

type fakePickerFetcher struct {
	prerender map[string]imgpkg.Render
}

func newFakePickerFetcher() *fakePickerFetcher {
	return &fakePickerFetcher{prerender: map[string]imgpkg.Render{}}
}

func (f *fakePickerFetcher) Prerendered(key string, t goimage.Point, _ imgpkg.Protocol) (imgpkg.Render, bool) {
	r, ok := f.prerender[key]
	return r, ok
}

func (f *fakePickerFetcher) Fetch(_ context.Context, _ imgpkg.FetchRequest) (imgpkg.FetchResult, error) {
	return imgpkg.FetchResult{}, nil
}

func TestPicker_View_ImageMode_UsesPlacement(t *testing.T) {
	slkemoji.SetImageMode(true, 2)
	t.Cleanup(func() { slkemoji.SetImageMode(false, 2) })

	thumbURL := slkemoji.CDNBaseURL + "1f44d.png"
	ff := newFakePickerFetcher()
	ff.prerender[slkemoji.EmojiCacheKey(thumbURL)] = imgpkg.Render{
		Cells: goimage.Pt(2, 1),
		Lines: []string{"\U0010EEEE\U0010EEEE"},
	}

	m := New()
	m.SetEmojiContext(EmojiContext{
		PlaceCtx: slkemoji.PlaceContext{Fetcher: ff},
		Cells:    2,
		Customs:  nil,
	})

	// Filter to a small set so the assert is unambiguous.
	m.SetQuery("thumbsup")

	out := m.View(80)
	if !strings.Contains(out, "\U0010EEEE") {
		t.Errorf("picker View does not contain kitty placeholder runes\noutput=%q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/reactionpicker/ -run TestPicker_View_ImageMode -v`
Expected: FAIL — the rendering loop still uses `slkemoji.ShouldRenderUnicode` + raw `entry.Unicode`.

---

### Task 8.3: Update picker row rendering to honor image mode

**Files:**
- Modify: `internal/ui/reactionpicker/model.go`

- [ ] **Step 1: Update the preview computation in `renderBox`**

Locate the row-building loop in `renderBox` (around line 338-352). The current preview line:

```go
		var line string
		if slkemoji.ShouldRenderUnicode(entry.Unicode) {
			line = entry.Unicode + " " + entry.Name
		} else {
			line = ":" + entry.Name + ":"
		}
```

Replace with an image-aware version. We need to also collect flush callbacks into a per-View slice; declare it just outside the loop:

```go
	imageOK := slkemoji.ImageModeActive() && m.emojiCtx.PlaceCtx.Fetcher != nil
	cells := m.emojiCtx.Cells
	if cells <= 0 {
		cells = 2
	}
	var pendingFlushes []func(io.Writer) error

	// ... existing thumbscroll setup ...

	for i := start; i < end; i++ {
		entry := list[i]

		var preview string
		if imageOK {
			if url, ok := slkemoji.URLForShortcode(entry.Name, m.emojiCtx.Customs); ok {
				if placement, flush, ok := slkemoji.Place(m.emojiCtx.PlaceCtx, url, cells); ok {
					preview = placement
					if flush != nil {
						pendingFlushes = append(pendingFlushes, flush)
					}
				}
			}
		}
		if preview == "" {
			// Legacy fallback path.
			if slkemoji.ShouldRenderUnicode(entry.Unicode) {
				preview = entry.Unicode
			} else {
				preview = ":" + entry.Name + ":"
			}
		}
		line := preview + " " + entry.Name

		// ... existing isExistingReaction + truncate + selection
		// styling + scrollbar block, unchanged.
	}
```

(The width arithmetic at `lipgloss.Width(line) > rowWidth` works as-is: the kitty placement reports 2 cells via the Phase 4 width override.)

- [ ] **Step 2: Fire pending flushes at the end of View()**

After the rows are joined into the final `box` string and just before returning, fire any pending flushes:

```go
	// Fire any kitty image upload callbacks the per-row Place calls
	// produced. Most are no-ops (the messages pane already triggered
	// the upload via the shared Registry); the picker still owns the
	// fire to handle the case where it's the first/only surface to
	// reference a given emoji this session.
	for _, fl := range pendingFlushes {
		_ = fl(imgpkg.KittyOutput)
	}

	return box
```

Add the `imgpkg` import.

- [ ] **Step 3: Run tests to verify they pass**

Run: `go test ./internal/ui/reactionpicker/ -run TestPicker_View_ImageMode -v`
Expected: PASS.

Run: `go test ./internal/ui/reactionpicker/ -v -count=1`
Expected: no regression on existing tests (image mode off → legacy fallback path = byte-identical).

- [ ] **Step 4: Commit**

```bash
git add internal/ui/reactionpicker/model.go internal/ui/reactionpicker/model_test.go
git commit -m "feat(reactionpicker): render preview emoji as kitty image when image mode active"
```

---

### Task 8.4: Wire `App.SetEmojiContext` to forward to picker; add reducer arm

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `internal/ui/reducer_io.go`

- [ ] **Step 1: Forward in `App.SetEmojiContext`**

In `internal/ui/app.go`, extend the `SetEmojiContext` forwarder (defined in Phase 7 Task 7.5):

```go
func (a *App) SetEmojiContext(ctx messages.EmojiContext) {
	a.messagepane.SetEmojiContext(ctx)
	a.threadPanel.SetEmojiContext(thread.EmojiContext{
		PlaceCtx: ctx.PlaceCtx, Cells: ctx.Cells, Customs: ctx.Customs,
	})
	a.reactionPicker.SetEmojiContext(reactionpicker.EmojiContext{
		PlaceCtx: ctx.PlaceCtx, Cells: ctx.Cells, Customs: ctx.Customs,
	})
}
```

- [ ] **Step 2: Forward in `SetCustomEmoji`**

Extend `SetCustomEmoji` similarly:

```go
func (a *App) SetCustomEmoji(customs map[string]string) {
	// ... existing body
	a.messagepane.SetEmojiCustoms(customs)
	a.threadPanel.SetEmojiCustoms(customs)
	a.reactionPicker.SetEmojiCustoms(customs)
}
```

Add the `SetEmojiCustoms` method to the picker (mirrors the messages-pane version):

```go
// SetEmojiCustoms updates the customs map without changing PlaceCtx
// or Cells. Called from App.SetCustomEmoji when the workspace's
// custom emoji list arrives.
func (m *Model) SetEmojiCustoms(customs map[string]string) {
	m.emojiCtx.Customs = customs
}
```

- [ ] **Step 3: Add picker arm to the reducer**

In `internal/ui/reducer_io.go`, extend the `EmojiImageReadyMsg` case:

```go
	case EmojiImageReadyMsg:
		debuglog.ImgFetch("recv: kind=emoji-ready url=%s", m.URL)
		a.messagepane.HandleEmojiImageReady(m.URL)
		a.threadPanel.HandleEmojiImageReady(m.URL)
		a.reactionPicker.HandleEmojiImageReady(m.URL) // no-op in v1; future caching may use it
		// Phase 9 adds autocomplete.
		return nil, true
```

- [ ] **Step 4: Build and test**

Run: `go build ./...`
Expected: clean.

Run: `go test ./...`
Expected: no new failures.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/app.go internal/ui/reactionpicker/model.go internal/ui/reducer_io.go
git commit -m "feat(ui): forward emoji context to reaction picker; wire reducer arm"
```

---

### Task 8.5: Final phase check

- [ ] **Step 1: Build the full project**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./... -count=1`
Expected: no new failures.

- [ ] **Step 3: Manual smoke (recommended)**

In a real kitty terminal: open slk, focus a message, open the reaction picker (`+` or whatever the keybind is). Type a query (e.g., `fire`). Verify:
- The filtered list shows image emoji previews next to each name.
- Scrolling the list doesn't leave ghost images.
- Selecting an emoji still works (no regression on `HandleKey`).

Phase 8 complete. The reaction picker grid renders image emoji. Continue to `09-autocomplete.md`.
