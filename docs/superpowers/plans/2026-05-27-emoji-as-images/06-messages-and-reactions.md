# Phase 6: Messages Pane and Reactions

> Index: `00-overview.md`. Previous: `05-place-helper.md`. Next: `07-thread-pane.md`.

**Goal:** First user-visible delivery. Replace the glyph-substitution emoji rendering in (a) the message body pipeline and (b) the reaction pill construction with the image-token pipeline from Phases 3-5, on the kitty-only image path. After this phase, a kitty user with `emoji_images = "on"` sees image emoji in the main messages pane and in every reaction pill on every visible message.

**Strategy:**
- Keep `RenderSlackMarkdown(text, userNames, channelNames) string` working unchanged for the ~30 test/non-render callers (back-compat shim).
- Add a sibling `RenderSlackMarkdownWith(text, opts) string` that takes a `RenderSlackMarkdownOpts` struct carrying the emoji-image context. The shim calls the new function with a zero opts struct.
- Inside the new function, the emoji-resolution step at the current line 570 branches on whether the opts carry a non-nil `PlaceContext.Fetcher` AND `emoji.ImageModeActive()` is true.
- A new `renderEmojiTokensInline` helper does the token-stream → string conversion, collecting kitty flushes into the caller's accumulator.
- Reaction pill construction at `model.go:1779` gets the same branching.
- A new `Model.SetEmojiContext` setter mirrors `SetImageContext`. Both are set once at startup from `cmd/slk/main.go`.
- A new `HandleEmojiImageReady(url)` performs coarse cache invalidation when the cold-cache fetch completes.

**Files:**
- Modify: `internal/ui/messages/render.go` — new opts struct, new entry point, new helper.
- Modify: `internal/ui/messages/render_test.go` — tests for the new helper.
- Modify: `internal/ui/messages/model.go` — Model setter, reaction-pill rendering, call-site plumbing, cache invalidation.
- Modify: `internal/ui/messages/model_test.go` — integration test for emoji-image rendering.
- Modify: `internal/ui/msgs.go` — re-export `emoji.EmojiImageReadyMsg` (or import alias) for reducer use.
- Modify: `internal/ui/reducer_io.go` — arm for `EmojiImageReadyMsg`.
- Modify: `cmd/slk/main.go` — construct PlaceContext, set it on the messages model.

---

### Task 6.1: Add `RenderSlackMarkdownOpts` and `RenderSlackMarkdownWith` shim

**Files:**
- Modify: `internal/ui/messages/render.go`

- [ ] **Step 1: Add the opts struct and entry point**

Insert above the existing `RenderSlackMarkdown` function (around line 433) in `internal/ui/messages/render.go`:

```go
// RenderSlackMarkdownOpts extends the legacy 3-arg RenderSlackMarkdown
// signature with optional emoji-image rendering. When PlaceCtx.Fetcher
// is non-nil AND emoji.ImageModeActive() returns true, emoji are
// rendered as kitty image placements via emoji.Place; otherwise the
// legacy glyph/shortcode-text path runs (byte-identical to the
// 3-arg form).
//
// EmojiFlushes accumulates kitty image upload callbacks for the warm
// path. nil disables flush collection (cold-only callers, or callers
// that don't care about flushes — e.g., tests).
type RenderSlackMarkdownOpts struct {
	UserNames    map[string]string
	ChannelNames map[string]string

	// Emoji-image opts (zero values disable the image path).
	PlaceCtx     emojiutil.PlaceContext
	EmojiCells   int                       // 0 falls back to 2
	Customs      map[string]string         // workspace custom emoji map; may be nil
	EmojiFlushes *[]func(io.Writer) error  // append-only; may be nil
}
```

Add the `io` import to `render.go` if not already present (likely already there for OSC-8 hyperlinks; confirm with `grep '"io"' internal/ui/messages/render.go`).

- [ ] **Step 2: Refactor `RenderSlackMarkdown` into a shim**

Replace the existing `RenderSlackMarkdown` function with a shim that delegates to the new function:

```go
// RenderSlackMarkdown is the legacy 3-arg entry point preserved for
// all callers that don't need emoji-image rendering (tests, threads
// view preview, etc.). New code should use RenderSlackMarkdownWith
// to get the image-path branch.
func RenderSlackMarkdown(text string, userNames map[string]string, channelNames map[string]string) string {
	return RenderSlackMarkdownWith(text, RenderSlackMarkdownOpts{
		UserNames:    userNames,
		ChannelNames: channelNames,
	})
}

// RenderSlackMarkdownWith is the full-featured entry point. See
// RenderSlackMarkdownOpts for the per-call configuration.
func RenderSlackMarkdownWith(text string, opts RenderSlackMarkdownOpts) string {
	// Body of the legacy function, lifted verbatim but parameterized
	// by opts.UserNames / opts.ChannelNames where it previously read
	// the arguments directly.

	// Handle code blocks first (before other formatting to avoid conflicts)
	text = codeBlockRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := codeBlockRe.FindStringSubmatch(match)[1]
		inner = strings.TrimSpace(inner)
		return "\n" + codeBlockStyle().Render(inner) + "\n"
	})

	// Process line by line for blockquotes
	lines := strings.Split(text, "\n")
	var result []string
	for _, line := range lines {
		if strings.HasPrefix(line, "&gt; ") || strings.HasPrefix(line, "> ") {
			quoted := strings.TrimPrefix(line, "&gt; ")
			quoted = strings.TrimPrefix(quoted, "> ")
			quoted = slackEntityDecoder.Replace(quoted)
			line = blockquoteStyle().Render(quoted)
		} else {
			line = renderInlineFormattingWith(line, opts)
			line = slackEntityDecoder.Replace(line)
		}
		result = append(result, line)
	}

	output := strings.Join(result, "\n")
	output = ReapplyBgAfterResets(output, BgANSI()+FgANSI())
	return output
}
```

- [ ] **Step 3: Refactor `renderInlineFormatting` to take opts**

Rename `renderInlineFormatting` to `renderInlineFormattingWith` and change its signature; add a thin back-compat wrapper that uses the legacy positional args:

```go
// renderInlineFormatting is the legacy 3-arg wrapper. Used only by
// tests that pre-date the opts struct; production code (called via
// RenderSlackMarkdownWith) goes through renderInlineFormattingWith.
func renderInlineFormatting(text string, userNames map[string]string, channelNames map[string]string) string {
	return renderInlineFormattingWith(text, RenderSlackMarkdownOpts{
		UserNames:    userNames,
		ChannelNames: channelNames,
	})
}

func renderInlineFormattingWith(text string, opts RenderSlackMarkdownOpts) string {
	userNames := opts.UserNames
	channelNames := opts.ChannelNames

	// ... existing body unchanged (inline code, bold, italic, strikethrough,
	// links, channel mentions, user mentions) until the emoji-resolution
	// block at the current line 570 ...

	// The emoji-resolution block is replaced in Task 6.3.
	text = emojiutil.ResolveShortcodesInText(emojiutil.StripSkinToneFromText(text))

	return text
}
```

(Move the body of the old function verbatim into `renderInlineFormattingWith`, swapping `userNames`/`channelNames` references to come from opts at the top. The emoji line is updated in Task 6.3.)

- [ ] **Step 4: Build to confirm**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 5: Run existing render tests**

Run: `go test ./internal/ui/messages/ -run TestRenderSlackMarkdown -v -count=1`
Expected: all PASS — the shim path is byte-identical to the old code.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/messages/render.go
git commit -m "refactor(messages): add RenderSlackMarkdownOpts shim around legacy entry point"
```

---

### Task 6.2: Failing test — inline emoji-token renderer

**Files:**
- Modify: `internal/ui/messages/render_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/messages/render_test.go`:

```go
import (
	// ... existing imports
	emojiutil "github.com/gammons/slk/internal/emoji"
)

func TestRenderEmojiTokensInline_ImageModeOff(t *testing.T) {
	// With image mode off, the helper returns the literal text of
	// each token verbatim — including the :name: form of unresolved
	// emoji. This is the fallback path; production goes through the
	// legacy ResolveShortcodesInText pipeline when image mode is off.
	emojiutil.SetImageMode(false, 2)
	t.Cleanup(func() { emojiutil.SetImageMode(false, 2) })

	thumbURL := emojiutil.CDNBaseURL + "1f44d.png"
	tokens := []emojiutil.Token{
		{Kind: emojiutil.TokenText, Text: "hi "},
		{Kind: emojiutil.TokenEmoji, Text: ":thumbsup:", URL: thumbURL},
		{Kind: emojiutil.TokenText, Text: " bye"},
	}

	var flushes []func(io.Writer) error
	got := renderEmojiTokensInline(tokens, emojiutil.PlaceContext{}, 2, &flushes)
	want := "hi :thumbsup: bye"
	if got != want {
		t.Errorf("renderEmojiTokensInline image-off = %q, want %q", got, want)
	}
	if len(flushes) != 0 {
		t.Errorf("flushes collected with image-off path = %d, want 0", len(flushes))
	}
}

func TestRenderEmojiTokensInline_ImageModeOn_ColdPath(t *testing.T) {
	emojiutil.SetImageMode(true, 2)
	t.Cleanup(func() { emojiutil.SetImageMode(false, 2) })

	thumbURL := emojiutil.CDNBaseURL + "1f44d.png"
	tokens := []emojiutil.Token{
		{Kind: emojiutil.TokenText, Text: "hi "},
		{Kind: emojiutil.TokenEmoji, Text: ":thumbsup:", URL: thumbURL},
	}

	// fakePlaceFetcher always reports cold (Prerendered miss).
	// fetchFn never called because we don't drain the goroutine here.
	ff := newFakePlaceFetcher()
	pctx := emojiutil.PlaceContext{Fetcher: ff, SendMsg: func(any) {}}

	var flushes []func(io.Writer) error
	got := renderEmojiTokensInline(tokens, pctx, 2, &flushes)
	want := "hi   " // text + 2 spaces for cold-cache emoji
	if got != want {
		t.Errorf("renderEmojiTokensInline cold-path = %q, want %q", got, want)
	}
	if len(flushes) != 0 {
		t.Errorf("cold-path flushes = %d, want 0", len(flushes))
	}
}

func TestRenderEmojiTokensInline_ImageModeOn_WarmPath(t *testing.T) {
	emojiutil.SetImageMode(true, 2)
	t.Cleanup(func() { emojiutil.SetImageMode(false, 2) })

	thumbURL := emojiutil.CDNBaseURL + "1f44d.png"
	tokens := []emojiutil.Token{
		{Kind: emojiutil.TokenEmoji, Text: ":thumbsup:", URL: thumbURL},
		{Kind: emojiutil.TokenText, Text: "!"},
	}

	ff := newFakePlaceFetcher()
	ff.setPrerendered(emojiutil.EmojiCacheKey(thumbURL), image.Pt(2, 1), image.Render{
		Cells:   image.Pt(2, 1),
		Lines:   []string{"\U0010EEEE\U0010EEEE"},
		OnFlush: func(io.Writer) error { return nil },
	})
	pctx := emojiutil.PlaceContext{Fetcher: ff}

	var flushes []func(io.Writer) error
	got := renderEmojiTokensInline(tokens, pctx, 2, &flushes)
	want := "\U0010EEEE\U0010EEEE!"
	if got != want {
		t.Errorf("renderEmojiTokensInline warm = %q, want %q", got, want)
	}
	if len(flushes) != 1 {
		t.Errorf("warm-path flushes = %d, want 1", len(flushes))
	}
}

// newFakePlaceFetcher is a test fake for emojiutil.PlaceFetcher.
// (Lives in render_test.go since it's used only here; the equivalent
// fake inside internal/emoji's place_test.go is separate.)
type fakePlaceFetcher struct {
	prerender map[string]image.Render
}

func newFakePlaceFetcher() *fakePlaceFetcher {
	return &fakePlaceFetcher{prerender: map[string]image.Render{}}
}

func (f *fakePlaceFetcher) setPrerendered(key string, t image.Point, r image.Render) {
	f.prerender[fmt.Sprintf("%s|%dx%d", key, t.X, t.Y)] = r
}

func (f *fakePlaceFetcher) Prerendered(key string, t image.Point, proto image.Protocol) (image.Render, bool) {
	r, ok := f.prerender[fmt.Sprintf("%s|%dx%d", key, t.X, t.Y)]
	return r, ok
}

func (f *fakePlaceFetcher) Fetch(ctx context.Context, req image.FetchRequest) (image.FetchResult, error) {
	return image.FetchResult{}, nil
}
```

Add the imports needed at the top of `render_test.go`:

```go
import (
	"context"
	"fmt"
	"io"
	image "github.com/gammons/slk/internal/image"
	// ... existing
)
```

(`image` here is the slk internal package, aliased to mask the stdlib `image` if it's also needed.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/messages/ -run TestRenderEmojiTokensInline -v`
Expected: FAIL — `renderEmojiTokensInline` is undefined.

---

### Task 6.3: Implement `renderEmojiTokensInline` and wire into emoji-resolution branch

**Files:**
- Modify: `internal/ui/messages/render.go`

- [ ] **Step 1: Add the helper**

Append to `internal/ui/messages/render.go`:

```go
// renderEmojiTokensInline walks a Token stream and returns the
// rendered inline string. Emoji tokens are placed via emoji.Place
// when the image path is active (emoji.ImageModeActive() AND
// placeCtx.Fetcher != nil); otherwise they render as their plain-text
// representation (the source-form text already captured on the
// Token).
//
// Kitty image upload callbacks collected on the warm path are
// appended to *flushes when non-nil. flushes left nil disables
// collection (caller doesn't care; cold-path callers).
func renderEmojiTokensInline(
	tokens []emojiutil.Token,
	placeCtx emojiutil.PlaceContext,
	cells int,
	flushes *[]func(io.Writer) error,
) string {
	if cells <= 0 {
		cells = 2
	}
	imageOK := emojiutil.ImageModeActive() && placeCtx.Fetcher != nil

	var b strings.Builder
	for _, tok := range tokens {
		switch tok.Kind {
		case emojiutil.TokenText:
			b.WriteString(tok.Text)
		case emojiutil.TokenEmoji:
			if imageOK && tok.URL != "" {
				placement, flush, ok := emojiutil.Place(placeCtx, tok.URL, cells)
				if ok {
					b.WriteString(placement)
					if flush != nil && flushes != nil {
						*flushes = append(*flushes, flush)
					}
					continue
				}
			}
			// Fallback: plain-text form (":name:" for unresolved
			// shortcodes / image-mode off, or the source-form glyph
			// for raw-codepoint emoji that bypassed Place).
			b.WriteString(tok.Text)
		}
	}
	return b.String()
}
```

- [ ] **Step 2: Wire the image-path branch into `renderInlineFormattingWith`**

In `renderInlineFormattingWith`, locate the emoji-resolution line (the verbatim line from the old function):

```go
text = emojiutil.ResolveShortcodesInText(emojiutil.StripSkinToneFromText(text))
```

Replace with:

```go
	// Emoji resolution.
	//
	// Image path (kitty + emoji_images=on): tokenize the text and
	// render emoji as kitty image placements via emoji.Place. The
	// width math (set up by Phase 4) already reports the configured
	// cell footprint for every image-renderable cluster, so layout
	// is deterministic regardless of font.
	//
	// Legacy path: the glyph/shortcode-text substitution that
	// retained ":name:" for multi-codepoint sequences. See
	// internal/emoji/shouldrender.go.
	if emojiutil.ImageModeActive() && opts.PlaceCtx.Fetcher != nil {
		stripped := emojiutil.StripSkinToneFromText(text)
		tokens := emojiutil.ResolveEmojiToTokens(stripped, opts.Customs)
		text = renderEmojiTokensInline(tokens, opts.PlaceCtx, opts.EmojiCells, opts.EmojiFlushes)
	} else {
		text = emojiutil.ResolveShortcodesInText(emojiutil.StripSkinToneFromText(text))
	}
```

- [ ] **Step 3: Run tests to verify they pass**

Run: `go test ./internal/ui/messages/ -run TestRenderEmojiTokensInline -v`
Expected: PASS (all three subtests).

- [ ] **Step 4: Run all render tests to confirm no regression**

Run: `go test ./internal/ui/messages/ -run TestRenderSlackMarkdown -v -count=1`
Expected: PASS — legacy callers still go through the unchanged glyph path because they pass empty opts.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/messages/render.go internal/ui/messages/render_test.go
git commit -m "feat(messages): renderEmojiTokensInline + image-mode branch in RenderSlackMarkdownWith"
```

---

### Task 6.4: Failing test — Model exposes a `SetEmojiContext` setter

**Files:**
- Modify: `internal/ui/messages/model_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/messages/model_test.go`:

```go
func TestModel_SetEmojiContext_InvalidatesCache(t *testing.T) {
	m := NewModel()
	m.SetMessages("C1", []MessageItem{{TS: "1.0", UserName: "u", UserID: "U", Text: "hello"}})
	// Force a render to populate m.cache.
	_ = m.View()

	if m.cache == nil {
		t.Fatalf("cache should be populated after View()")
	}

	m.SetEmojiContext(EmojiContext{
		PlaceCtx: emojiutil.PlaceContext{},
		Cells:    2,
		Customs:  nil,
	})
	if m.cache != nil {
		t.Errorf("cache should be nil after SetEmojiContext (forces re-render)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/messages/ -run TestModel_SetEmojiContext -v`
Expected: FAIL — `SetEmojiContext` and `EmojiContext` are undefined.

---

### Task 6.5: Add `EmojiContext` and `SetEmojiContext` to Model

**Files:**
- Modify: `internal/ui/messages/model.go`

- [ ] **Step 1: Add the type and setter**

In `internal/ui/messages/model.go`, just above `SetImageContext` (around line 1245), add:

```go
// EmojiContext bundles the emoji-image rendering dependencies. Held
// by the Model and threaded through RenderSlackMarkdownWith when
// building each message's body and reaction pills.
type EmojiContext struct {
	PlaceCtx emojiutil.PlaceContext
	Cells    int                // 1 or 2; 0 falls back to 2
	Customs  map[string]string  // workspace custom emoji map; nil = empty
}

// SetEmojiContext configures the emoji-image rendering path. Should
// be called once at startup (from cmd/slk/main.go) after the
// PlaceContext, Customs map, and EmojiCells are known. Subsequent
// calls invalidate the render cache so the new context takes effect
// on the next View().
func (m *Model) SetEmojiContext(ctx EmojiContext) {
	if ctx.Cells != 1 && ctx.Cells != 2 {
		ctx.Cells = 2
	}
	m.emojiCtx = ctx
	m.cache = nil
	m.dirty()
}
```

Add the field to the `Model` struct (find the struct definition; the field list is around lines 80-300):

```go
type Model struct {
	// ... existing fields
	emojiCtx EmojiContext
	// ...
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./internal/ui/messages/ -run TestModel_SetEmojiContext -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/messages/model.go internal/ui/messages/model_test.go
git commit -m "feat(messages): EmojiContext + SetEmojiContext setter"
```

---

### Task 6.6: Plumb opts at the body-text call sites

**Files:**
- Modify: `internal/ui/messages/model.go`

- [ ] **Step 1: Update `renderMessagePlain` to use the new opts**

In `internal/ui/messages/model.go`, locate `renderMessagePlain` (around line 1704). The body-text render is at line 1722:

```go
	text := styles.MessageText.Render(WordWrap(RenderSlackMarkdown(MessageTextSource(msg), userNames, channelNames), contentWidth))
```

Replace with a version that uses `RenderSlackMarkdownWith` and collects emoji flushes into the existing `flushes` accumulator returned by this function:

```go
	bodyOpts := RenderSlackMarkdownOpts{
		UserNames:    userNames,
		ChannelNames: channelNames,
		PlaceCtx:     m.emojiCtx.PlaceCtx,
		EmojiCells:   m.emojiCtx.Cells,
		Customs:      m.emojiCtx.Customs,
		EmojiFlushes: &flushes, // append-in-place into the named return
	}
	text := styles.MessageText.Render(WordWrap(RenderSlackMarkdownWith(MessageTextSource(msg), bodyOpts), contentWidth))
```

Note: `flushes` is the named return variable of `renderMessagePlain` (line 1705). Appending into it via the pointer means warm-path emoji uploads land in the same per-message flush list that inline-image attachments already use — the View() loop fires all of them per frame.

- [ ] **Step 2: Update the blockkit `RenderText` closure**

Locate the `RenderText` closure (around line 1676):

```go
		RenderText: func(s string, un map[string]string) string {
			return RenderSlackMarkdown(s, un, channelNames)
		},
```

Replace with:

```go
		RenderText: func(s string, un map[string]string) string {
			// blockkit's RenderText is called from inside block rendering
			// where the per-call flush accumulator isn't accessible. Pass
			// the emoji opts but no flush collector: warm-path emoji
			// flushes inside rich-text blocks are best-effort in v1
			// (they'll be re-collected on the next render when the
			// per-message buildCache walks the entry again). Worst case:
			// one extra frame of cold-cache spacing for a block-kit
			// emoji on first reveal. Acceptable.
			return RenderSlackMarkdownWith(s, RenderSlackMarkdownOpts{
				UserNames:    un,
				ChannelNames: channelNames,
				PlaceCtx:     m.emojiCtx.PlaceCtx,
				EmojiCells:   m.emojiCtx.Cells,
				Customs:      m.emojiCtx.Customs,
				EmojiFlushes: nil,
			})
		},
```

- [ ] **Step 3: Build and run the model tests**

Run: `go build ./...`
Expected: clean.

Run: `go test ./internal/ui/messages/ -v -count=1`
Expected: no new failures.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/messages/model.go
git commit -m "feat(messages): plumb EmojiContext into body-text rendering"
```

---

### Task 6.7: Update reaction pill construction to use image emoji

**Files:**
- Modify: `internal/ui/messages/model.go`

- [ ] **Step 1: Replace the per-pill emoji resolution**

In `internal/ui/messages/model.go`, locate the reaction-pill construction loop (around line 1764). The current code is:

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

Replace with an image-aware version that uses `emoji.Place` when the image path is active:

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
				// Legacy fallback path (image mode off, or no URL).
				resolved := kyoemoji.Sprint(":" + nameForLookup + ":")
				if emojiutil.ShouldRenderUnicode(resolved) {
					emojiStr = resolved
				} else {
					emojiStr = ":" + nameForLookup + ":"
				}
			}
			pillText := fmt.Sprintf("%s%d", emojiStr, r.Count)
			var style lipgloss.Style
			if isSelected && m.reactionNavActive && i == m.reactionNavIndex {
				style = styles.ReactionPillSelected
			} else if r.HasReacted {
				style = styles.ReactionPillOwn
			} else {
				style = styles.ReactionPillOther
			}
			pills = append(pills, style.Render(pillText))
			pillEmojis = append(pillEmojis, r.Emoji)
			if placedFlush != nil {
				flushes = append(flushes, placedFlush)
			}
		}
```

Note: `flushes` here refers to the same named-return slice that `renderMessagePlain` accumulates. Image emoji in reaction pills land in the same per-frame flush list as body text and inline attachments.

- [ ] **Step 2: Build and run tests**

Run: `go build ./...`
Expected: clean.

Run: `go test ./internal/ui/messages/ -v -count=1`
Expected: no new failures. The reaction tests still pass because image mode is OFF by default in tests (no SetEmojiContext call) and the fallback branch runs.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/messages/model.go
git commit -m "feat(messages): render reaction pills as image emoji when image mode active"
```

---

### Task 6.8: Add `HandleEmojiImageReady` and wire reducer

**Files:**
- Modify: `internal/ui/messages/model.go`
- Modify: `internal/ui/msgs.go`
- Modify: `internal/ui/reducer_io.go`

- [ ] **Step 1: Add `HandleEmojiImageReady` to Model**

In `internal/ui/messages/model.go`, near `HandleImageReady` (around line 403), add:

```go
// HandleEmojiImageReady is invoked by the host (App.Update) when an
// emoji.EmojiImageReadyMsg lands. Same emoji appears in any visible
// message, reaction pill, or block-kit element — so the cheapest
// correct invariant is a wholesale render-cache invalidation. The
// next View() rebuilds with the now-warm emoji placement (single
// kitty transmit + N placements per the registry's dedup contract).
//
// v1 keeps this coarse; if heavy-emoji channels show measurable
// invalidation churn (multiple emoji arriving in a burst), a future
// follow-up can index per-URL → per-message-TS for targeted
// staleEntries population. Punt for now: kitty image placements are
// cheap, full re-render of a viewport is sub-frame on the workloads
// tested.
func (m *Model) HandleEmojiImageReady(url string) {
	debuglog.ImgFetch("messages.HandleEmojiImageReady: url=%s wholesale_invalidate", url)
	m.cache = nil
	m.dirty()
}
```

- [ ] **Step 2: Re-export `EmojiImageReadyMsg` for reducer use**

In `internal/ui/msgs.go`, add a type alias so reducers can refer to the message without importing the emoji package in their import block:

```go
// EmojiImageReadyMsg re-exports emoji.EmojiImageReadyMsg so reducers
// can refer to it without an extra import. Dispatched when a previously
// cold-cache emoji finishes fetching and is now warm-renderable across
// every UI surface.
type EmojiImageReadyMsg = emojiutil.EmojiImageReadyMsg
```

Add the import if not present (`emojiutil "github.com/gammons/slk/internal/emoji"` — likely already there for other reasons; confirm with `grep emojiutil internal/ui/msgs.go`).

- [ ] **Step 3: Add the reducer arm**

In `internal/ui/reducer_io.go`, locate the `imgrender.ImageReadyMsg` case (around line 154). Add a sibling case below it:

```go
	case EmojiImageReadyMsg:
		debuglog.ImgFetch("recv: kind=emoji-ready url=%s", m.URL)
		// An emoji-image fetch landed. Invalidate every surface
		// that renders emoji so the next View() picks up the warm-
		// cache placement. Cheap coarse invalidation in v1.
		a.messagepane.HandleEmojiImageReady(m.URL)
		a.threadPanel.HandleEmojiImageReady(m.URL)
		// Picker and autocomplete add their own handlers in Phases
		// 8-9; safe to leave commented out here until then.
		return nil, true
```

(The `a.threadPanel.HandleEmojiImageReady` call won't compile until Phase 7. For Phase 6, leave it out and add a comment:)

```go
	case EmojiImageReadyMsg:
		debuglog.ImgFetch("recv: kind=emoji-ready url=%s", m.URL)
		a.messagepane.HandleEmojiImageReady(m.URL)
		// Phase 7 adds a.threadPanel.HandleEmojiImageReady(m.URL).
		// Phase 8 adds picker invalidation. Phase 9 adds autocomplete.
		return nil, true
```

- [ ] **Step 4: Build and test**

Run: `go build ./...`
Expected: clean.

Run: `go test ./internal/ui/ -run TestApp -v -count=1 || go test ./internal/ui/ -run TestReducer -v -count=1`
Expected: no new failures (the new arm is only triggered when a real EmojiImageReadyMsg arrives, which production tests don't yet exercise).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/messages/model.go internal/ui/msgs.go internal/ui/reducer_io.go
git commit -m "feat(messages): HandleEmojiImageReady + reducer arm for re-render"
```

---

### Task 6.9: Wire `PlaceContext` and customs into `cmd/slk/main.go`

**Files:**
- Modify: `cmd/slk/main.go`

- [ ] **Step 1: Build the PlaceContext at startup**

In `cmd/slk/main.go`, near the existing `buildImgCtx` closure (around line 642), add a parallel `buildPlaceCtx` closure:

```go
	// buildPlaceCtx mirrors buildImgCtx for emoji-image placements.
	// The Fetcher is the same instance (one cache, one prerender
	// pipeline). SendMsg dispatches EmojiImageReadyMsg through
	// bubbletea so reducers can invalidate per-surface caches.
	buildPlaceCtx := func(send func(tea.Msg)) emojiutil.PlaceContext {
		return emojiutil.PlaceContext{
			Fetcher: imageFetcher,
			SendMsg: func(v any) {
				if send != nil {
					if msg, ok := v.(tea.Msg); ok {
						send(msg)
					}
				}
			},
		}
	}
```

(`tea.Msg` is `any`, so the type-assertion in the closure is just a re-typing for the bubbletea send signature.)

- [ ] **Step 2: Call `SetEmojiContext` on the messages model alongside `SetImageContext`**

Locate where `app.SetImageContext(buildImgCtx(nil))` is called (around line 653). Add:

```go
	app.SetImageContext(buildImgCtx(nil))
	app.SetImageFetcher(imageFetcher)

	// Emoji-image rendering. Active only on kitty (per ImageMode
	// gate set earlier). When inactive the messages pane uses the
	// legacy glyph/shortcode-text rendering path.
	app.SetEmojiContext(messages.EmojiContext{
		PlaceCtx: buildPlaceCtx(nil), // SendMsg refreshed below once Program exists
		Cells:    cfg.Appearance.EmojiCells,
		Customs:  nil,                 // populated by CustomEmojisLoadedMsg
	})
```

- [ ] **Step 3: Add the `App.SetEmojiContext` accessor**

In `internal/ui/app.go`, near the existing `SetImageContext` accessor, add:

```go
// SetEmojiContext forwards the emoji rendering context to the
// messages pane. Subsequent CustomEmojisLoadedMsg dispatches update
// the customs map via App.SetCustomEmoji which calls back into the
// messages pane's emojiCtx.
func (a *App) SetEmojiContext(ctx messages.EmojiContext) {
	a.messagepane.SetEmojiContext(ctx)
}
```

- [ ] **Step 4: Refresh the SendMsg after Program exists**

Locate where the second SetImageContext call lands (search for `SetImageContext(buildImgCtx(p.Send)` in `cmd/slk/main.go`). Add the matching second call for emoji context immediately after:

```go
	app.SetImageContext(buildImgCtx(p.Send))
	app.SetEmojiContext(messages.EmojiContext{
		PlaceCtx: buildPlaceCtx(p.Send),
		Cells:    cfg.Appearance.EmojiCells,
		Customs:  nil, // CustomEmojisLoadedMsg fills this in
	})
```

- [ ] **Step 5: Update SetCustomEmoji to also re-set the emoji context's Customs map**

In `internal/ui/app.go`, locate `SetCustomEmoji` (around line 1706). Add a call to update the messages pane's emoji-context customs map at the end:

```go
func (a *App) SetCustomEmoji(customs map[string]string) {
	// ... existing body that updates the picker / compose entries ...

	// Update the messages pane's emoji-image context so newly-known
	// custom emoji URLs become resolvable on the next render.
	a.messagepane.SetEmojiCustoms(customs)
}
```

And add the corresponding setter in `internal/ui/messages/model.go`:

```go
// SetEmojiCustoms updates only the customs map on the active emoji
// context, leaving PlaceCtx and Cells untouched. Invalidates the
// render cache so the new map is consulted on the next View().
//
// Called from App.SetCustomEmoji when CustomEmojisLoadedMsg arrives
// from the workspace bootstrap.
func (m *Model) SetEmojiCustoms(customs map[string]string) {
	m.emojiCtx.Customs = customs
	m.cache = nil
	m.dirty()
}
```

- [ ] **Step 6: Build and smoke-run**

Run: `go build ./...`
Expected: clean.

Run: `go run ./cmd/slk --help 2>&1 | head -1`
Expected: clean exit.

- [ ] **Step 7: Commit**

```bash
git add cmd/slk/main.go internal/ui/app.go internal/ui/messages/model.go
git commit -m "feat(slk): wire PlaceContext + customs into messages pane at startup"
```

---

### Task 6.10: Integration test — image emoji visible in a rendered message

**Files:**
- Modify: `internal/ui/messages/model_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/messages/model_test.go`:

```go
func TestModel_RenderMessageWithImageEmoji_WarmCache(t *testing.T) {
	emojiutil.SetImageMode(true, 2)
	t.Cleanup(func() { emojiutil.SetImageMode(false, 2) })

	thumbURL := emojiutil.CDNBaseURL + "1f44d.png"
	heartURL := emojiutil.CDNBaseURL + "2764.png"

	ff := newFakePlaceFetcher() // defined in render_test.go
	ff.setPrerendered(emojiutil.EmojiCacheKey(thumbURL), image.Pt(2, 1), image.Render{
		Cells: image.Pt(2, 1),
		Lines: []string{"\U0010EEEE\U0010EEEE"},
	})
	ff.setPrerendered(emojiutil.EmojiCacheKey(heartURL), image.Pt(2, 1), image.Render{
		Cells: image.Pt(2, 1),
		Lines: []string{"\U0010EEEE\U0010EEEE"},
	})

	m := NewModel()
	m.SetSize(80, 24)
	m.SetEmojiContext(EmojiContext{
		PlaceCtx: emojiutil.PlaceContext{Fetcher: ff},
		Cells:    2,
		Customs:  nil,
	})
	m.SetMessages("C1", []MessageItem{{
		TS:       "1.0",
		UserName: "alice",
		UserID:   "U1",
		Text:     "hi :thumbsup: and \u2764\uFE0F",
		Reactions: []MessageReaction{
			{Emoji: "thumbsup", Count: 3, HasReacted: false},
		},
	}})

	out := m.View()

	// The rendered output should contain kitty placeholder runes
	// (from the warm-path Place calls), NOT the literal ":thumbsup:"
	// text or the bare unicode glyph.
	if !strings.Contains(out, "\U0010EEEE") {
		t.Errorf("rendered view does not contain kitty placeholder runes; image mode appears inactive\noutput=%q", out)
	}
	if strings.Contains(out, ":thumbsup:") {
		t.Errorf("rendered view contains literal :thumbsup: text; image mode did not replace it\noutput=%q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/ui/messages/ -run TestModel_RenderMessageWithImageEmoji -v`
Expected: PASS.

If it fails on the "literal :thumbsup:" check, the body-text path is still going through the legacy branch — confirm `m.emojiCtx.PlaceCtx.Fetcher != nil` at the call site (the `bodyOpts` struct in Task 6.6).

If it fails on "no placeholder runes", check that `image.Render.Lines` is being correctly returned by the fake fetcher's `Prerendered` method.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/messages/model_test.go
git commit -m "test(messages): integration test for image emoji in body + reactions"
```

---

### Task 6.11: Final phase check

- [ ] **Step 1: Build the full project**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`
Expected: no new failures. If pre-existing tests fail because they exercise the rendering path with image mode active in an unrelated test (cross-test state pollution), add `emojiutil.SetImageMode(false, 2)` cleanup to those tests.

- [ ] **Step 3: Manual smoke (recommended)**

Run slk in a real kitty terminal with `[appearance] emoji_images = "on"`. Open a channel with a message containing a recognizable emoji (`:thumbsup:`, `:fire:`, etc.) and a reaction pill. Verify:

- The emoji renders as an image, not as text or a flat glyph.
- The reaction pill shows the emoji image followed by the count.
- The image and the count align on the same line (no row-shift from the kitty placement).
- Scrolling the channel doesn't leave ghost images.

If any of these fail, the kitty registry / flush mechanism is likely missing a callback — confirm the `flushes` accumulator is being walked by `View()` (the existing inline-image path proves this works; we're just adding more callers to the same slice).

Phase 6 complete. **The feature is live on the main messages pane.** Continue to `07-thread-pane.md` to mirror the same changes in the thread panel.
