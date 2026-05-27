# Phase 9: Emoji Autocomplete Dropdown

> Index: `00-overview.md`. Previous: `08-picker.md`. Next: `10-yank-and-search.md`.

**Goal:** Render image emoji in the compose autocomplete dropdown (`internal/ui/emojipicker/`). When a user types `:thu` in compose, the dropdown that appears shows entries like `:thumbsup: thumbsup`. After this phase, the preview is a kitty image placement when image mode is active.

The autocomplete is structurally identical to the reaction picker (Phase 8) — same "preview + name" row pattern. Same flush-collection approach. Plan is shorter because the changes mirror Phase 8.

**Files:**
- Modify: `internal/ui/emojipicker/model.go`
- Modify: `internal/ui/emojipicker/model_test.go`
- Modify: `internal/ui/app.go` (forward `SetEmojiContext` to the compose's autocomplete pickers; both `app.compose` and `app.threadCompose` carry one)
- Modify: `internal/ui/compose/model.go` (pass-through setter)
- Modify: `internal/ui/reducer_io.go` (extend the picker arm to include autocomplete invalidation)

---

### Task 9.1: Add `EmojiContext` to the emojipicker model

**Files:**
- Modify: `internal/ui/emojipicker/model.go`

- [ ] **Step 1: Add types and setters**

In `internal/ui/emojipicker/model.go`, near the existing entry setter (`SetEntries`, around line 30), add:

```go
// EmojiContext bundles the emoji-image rendering dependencies for
// the compose autocomplete dropdown. Mirrors the picker's version
// in shape and purpose. The Customs field is unused here because the
// entries the dropdown searches already include workspace customs
// (see emoji.BuildEntries); it's kept for shape parity with the
// other emoji-context types so all callers use the same setter
// signature.
type EmojiContext struct {
	PlaceCtx emoji.PlaceContext
	Cells    int
	Customs  map[string]string
}

// SetEmojiContext configures emoji-image rendering for the autocomplete
// dropdown. Mirrors the same setter on other UI surfaces.
func (m *Model) SetEmojiContext(ctx EmojiContext) {
	if ctx.Cells != 1 && ctx.Cells != 2 {
		ctx.Cells = 2
	}
	m.emojiCtx = ctx
}

// HandleEmojiImageReady is a no-op hook for shape parity with other
// surfaces. The dropdown has no render cache.
func (m *Model) HandleEmojiImageReady(_ string) {}
```

Add the `emojiCtx EmojiContext` field to the Model struct.

- [ ] **Step 2: Build to confirm**

Run: `go build ./internal/ui/emojipicker/`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/emojipicker/model.go
git commit -m "feat(emojipicker): EmojiContext setter + HandleEmojiImageReady hook"
```

---

### Task 9.2: Failing test — dropdown row uses image placement when image mode active

**Files:**
- Modify: `internal/ui/emojipicker/model_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/emojipicker/model_test.go`:

```go
import (
	// ... existing
	"context"
	goimage "image"
	"strings"
	slkemoji "github.com/gammons/slk/internal/emoji"
	imgpkg "github.com/gammons/slk/internal/image"
)

type fakeDropdownFetcher struct {
	prerender map[string]imgpkg.Render
}

func (f *fakeDropdownFetcher) Prerendered(key string, _ goimage.Point, _ imgpkg.Protocol) (imgpkg.Render, bool) {
	r, ok := f.prerender[key]
	return r, ok
}
func (f *fakeDropdownFetcher) Fetch(_ context.Context, _ imgpkg.FetchRequest) (imgpkg.FetchResult, error) {
	return imgpkg.FetchResult{}, nil
}

func TestDropdown_View_ImageMode_UsesPlacement(t *testing.T) {
	slkemoji.SetImageMode(true, 2)
	t.Cleanup(func() { slkemoji.SetImageMode(false, 2) })

	thumbURL := slkemoji.CDNBaseURL + "1f44d.png"
	ff := &fakeDropdownFetcher{
		prerender: map[string]imgpkg.Render{
			slkemoji.EmojiCacheKey(thumbURL): {
				Cells: goimage.Pt(2, 1),
				Lines: []string{"\U0010EEEE\U0010EEEE"},
			},
		},
	}

	var m Model
	m.SetEntries([]emoji.EmojiEntry{
		{Name: "thumbsup", Unicode: "\U0001F44D", Display: "\U0001F44D"},
		{Name: "thumbsdown", Unicode: "\U0001F44E", Display: "\U0001F44E"},
	})
	m.SetEmojiContext(EmojiContext{
		PlaceCtx: slkemoji.PlaceContext{Fetcher: ff},
		Cells:    2,
	})
	m.Open()
	m.Query("thumbs")

	out := m.View(40)
	if !strings.Contains(out, "\U0010EEEE") {
		t.Errorf("autocomplete View does not contain kitty placeholder runes\noutput=%q", out)
	}
}
```

(`Open`, `Query`, and `SetEntries` are the existing methods on the picker; confirm names by reading `internal/ui/emojipicker/model.go` if any differ.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/emojipicker/ -run TestDropdown_View_ImageMode -v`
Expected: FAIL — the View still uses `e.Display` verbatim.

---

### Task 9.3: Update dropdown row rendering

**Files:**
- Modify: `internal/ui/emojipicker/model.go`

- [ ] **Step 1: Update the preview computation in `View`**

Locate the rendering loop in `View` (around line 129-144). The current code:

```go
	for i, e := range m.filtered {
		// ... indicator + nameStyle setup
		pad := previewWidth - lipgloss.Width(e.Display)
		if pad < 0 {
			pad = 0
		}
		preview := e.Display + strings.Repeat(" ", pad)
		row := fmt.Sprintf("%s%s  %s", indicator, preview, nameStyle.Render(":"+e.Name+":"))
		rows = append(rows, row)
	}
```

Replace with the image-aware version:

```go
	imageOK := emoji.ImageModeActive() && m.emojiCtx.PlaceCtx.Fetcher != nil
	cells := m.emojiCtx.Cells
	if cells <= 0 {
		cells = 2
	}
	var pendingFlushes []func(io.Writer) error

	for i, e := range m.filtered {
		// ... existing indicator + nameStyle setup

		var preview string
		if imageOK {
			if url, ok := emoji.URLForShortcode(e.Name, m.emojiCtx.Customs); ok {
				if placement, flush, ok := emoji.Place(m.emojiCtx.PlaceCtx, url, cells); ok {
					preview = placement
					if flush != nil {
						pendingFlushes = append(pendingFlushes, flush)
					}
				}
			}
		}
		if preview == "" {
			preview = e.Display
		}

		pad := previewWidth - lipgloss.Width(preview)
		if pad < 0 {
			pad = 0
		}
		preview = preview + strings.Repeat(" ", pad)
		row := fmt.Sprintf("%s%s  %s", indicator, preview, nameStyle.Render(":"+e.Name+":"))
		rows = append(rows, row)
	}
```

Note: `previewWidth` is computed at line 120-126 from `e.Display` widths. With image mode active, image placements all report 2 cells via the Phase 4 width override, so the loop's max-width logic naturally accommodates them. No change needed to the previewWidth computation.

- [ ] **Step 2: Fire pending flushes before returning the box**

After the existing `box := lipgloss.NewStyle()...Render(content)` line and before `return box`, add:

```go
	for _, fl := range pendingFlushes {
		_ = fl(imgpkg.KittyOutput)
	}
	return box
```

Add the `imgpkg` import.

- [ ] **Step 3: Run tests to verify they pass**

Run: `go test ./internal/ui/emojipicker/ -run TestDropdown_View_ImageMode -v`
Expected: PASS.

Run: `go test ./internal/ui/emojipicker/ -v -count=1`
Expected: no regression.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/emojipicker/model.go internal/ui/emojipicker/model_test.go
git commit -m "feat(emojipicker): render autocomplete dropdown previews as image emoji"
```

---

### Task 9.4: Plumb `SetEmojiContext` through compose

**Files:**
- Modify: `internal/ui/compose/model.go`

- [ ] **Step 1: Add a pass-through setter**

In `internal/ui/compose/model.go`, near the existing `SetEmojiEntries` (around line 944), add:

```go
// SetEmojiContext forwards the emoji-image rendering context to the
// underlying autocomplete picker. Called from App at startup and
// whenever the customs map changes.
func (m *Model) SetEmojiContext(ctx emojipicker.EmojiContext) {
	m.emojiPicker.SetEmojiContext(ctx)
}
```

- [ ] **Step 2: Build and confirm**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/compose/model.go
git commit -m "feat(compose): forward SetEmojiContext to the autocomplete dropdown"
```

---

### Task 9.5: Wire `App.SetEmojiContext` to forward to both composes

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Extend the forwarder**

In `internal/ui/app.go`, extend the `SetEmojiContext` forwarder (extended in Phase 8 Task 8.4):

```go
func (a *App) SetEmojiContext(ctx messages.EmojiContext) {
	a.messagepane.SetEmojiContext(ctx)
	a.threadPanel.SetEmojiContext(thread.EmojiContext{
		PlaceCtx: ctx.PlaceCtx, Cells: ctx.Cells, Customs: ctx.Customs,
	})
	a.reactionPicker.SetEmojiContext(reactionpicker.EmojiContext{
		PlaceCtx: ctx.PlaceCtx, Cells: ctx.Cells, Customs: ctx.Customs,
	})
	a.compose.SetEmojiContext(emojipicker.EmojiContext{
		PlaceCtx: ctx.PlaceCtx, Cells: ctx.Cells, Customs: ctx.Customs,
	})
	a.threadCompose.SetEmojiContext(emojipicker.EmojiContext{
		PlaceCtx: ctx.PlaceCtx, Cells: ctx.Cells, Customs: ctx.Customs,
	})
}
```

Add the `emojipicker` import.

- [ ] **Step 2: Extend the reducer arm**

In `internal/ui/reducer_io.go`, extend the `EmojiImageReadyMsg` case to cover both compose pickers:

```go
	case EmojiImageReadyMsg:
		debuglog.ImgFetch("recv: kind=emoji-ready url=%s", m.URL)
		a.messagepane.HandleEmojiImageReady(m.URL)
		a.threadPanel.HandleEmojiImageReady(m.URL)
		a.reactionPicker.HandleEmojiImageReady(m.URL)
		// Autocomplete dropdowns have no cache; the no-op hooks keep
		// the surface symmetric. Listed here for the audit trail.
		// a.compose.emojiPicker / a.threadCompose.emojiPicker
		return nil, true
```

(The compose hooks are internal — no setter needed for the no-op pass-through.)

- [ ] **Step 3: Build and test**

Run: `go build ./...`
Expected: clean.

Run: `go test ./... -count=1`
Expected: no new failures.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/app.go internal/ui/reducer_io.go
git commit -m "feat(app): forward emoji context to compose autocomplete dropdowns"
```

---

### Task 9.6: Final phase check

- [ ] **Step 1: Build the full project**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./... -count=1`
Expected: no new failures.

- [ ] **Step 3: Manual smoke (recommended)**

In a real kitty terminal: open slk, focus the compose, type `:thu`. Verify:
- The dropdown shows image emoji previews next to each name.
- Selecting an entry with Enter still inserts the `:name:` text into compose (no regression on selection behavior).
- Selecting and pressing Tab still completes correctly.
- Closing the dropdown (Esc) doesn't leave ghost images.

Phase 9 complete. Image emoji appear in: messages pane body + reactions (Phase 6), thread pane body + reactions (Phase 7), reaction picker grid (Phase 8), compose autocomplete dropdown (Phase 9). All in-scope surfaces are covered.

Continue to `10-yank-and-search.md`.
