# Phase 10: Yank and Clipboard Verification

> Index: `00-overview.md`. Previous: `09-autocomplete.md`. Next: `11-docs.md`.

**Goal:** Ensure the kitty unicode-placeholder rune (`U+10EEEE`) never reaches the OS clipboard when a user yanks (copies) a message that contains image-rendered emoji. Maintain the current selection-highlight visual alignment.

**Background (discovered during planning):**

The messages and thread panes store a parallel `linesPlain []plainLine` alongside the styled `linesNormal []string`. `SelectionText()` walks `linesPlain` and slices bytes by column. Today `linesPlain` is derived via `ansi.Strip(rendered)` + `buildPlainLine` — but `ansi.Strip` does NOT remove kitty placeholder runes (they are real Unicode codepoints, not ANSI escapes). After Phases 6-7, a yanked selection that covers an emoji image would include literal U+10EEEE bytes in the clipboard. That's the regression we close here.

**Design constraint:** Selection columns are computed from mouse coordinates against `linesNormal` widths. `SelectionText` slices `linesPlain` by those columns. For the slice to land on the right bytes, `linesPlain` widths must match `linesNormal` widths per line. We cannot expand a 2-cell kitty placement into a 10-cell `:thumbsup:` literal in `linesPlain` without breaking the column→byte correspondence.

**v1 approach:** Replace each contiguous run of U+10EEEE in the `linesPlain` source with the same number of ASCII spaces (and strip the leading SGR foreground escape that encoded the image ID). Selection of emoji-containing text yields readable clipboard text with spaces where emoji were. The emoji content is lost, but no garbage bytes leak.

**Follow-up (post-v1):** A richer mapping that preserves `:name:` or unicode glyph forms in `linesPlain`. Requires either a parallel "plain occurrence map" computed during render, or a custom `buildPlainLine` that treats kitty-placement clusters as fixed-cell-width with variable-byte-length plainText. Out of scope here; tracked in `00-overview.md`'s follow-up list.

**No search:** A repo-wide grep confirms slk has no in-buffer "find" / search-messages feature. The spec's mention of search behavior verification is moot.

**Files:**
- Modify: `internal/ui/messages/render.go` — `plainLines` strips U+10EEEE runs from its source before grapheme-walking.
- Modify: `internal/ui/messages/render_test.go` (or `plain_test.go`) — coverage.
- Modify: `internal/ui/messages/selection_test.go` — yank-test confirms clipboard cleanliness.
- Modify: `internal/ui/thread/selection_test.go` — parallel thread test.

---

### Task 10.1: Failing test — yank of image-emoji line produces clean clipboard text

**Files:**
- Modify: `internal/ui/messages/selection_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/messages/selection_test.go`:

```go
import (
	// ... existing
	emojiutil "github.com/gammons/slk/internal/emoji"
)

func TestSelectionText_ImageEmoji_NoPlaceholderRunesInClipboard(t *testing.T) {
	emojiutil.SetImageMode(true, 2)
	t.Cleanup(func() { emojiutil.SetImageMode(false, 2) })

	thumbURL := emojiutil.CDNBaseURL + "1f44d.png"
	ff := newFakePlaceFetcher() // defined in render_test.go (Phase 6)
	ff.setPrerendered(emojiutil.EmojiCacheKey(thumbURL), image.Pt(2, 1), image.Render{
		Cells: image.Pt(2, 1),
		Lines: []string{"\U0010EEEE\U0010EEEE"},
	})

	m := NewModel()
	m.SetSize(80, 24)
	m.SetEmojiContext(EmojiContext{
		PlaceCtx: emojiutil.PlaceContext{Fetcher: ff},
		Cells:    2,
	})
	m.SetMessages("C1", []MessageItem{
		{TS: "1.0", UserName: "u", UserID: "U", Text: "hi :thumbsup: bye"},
	})
	_ = m.View()

	// Select the full visible content — implementation-specific helper;
	// follow the pattern used by other selection tests in this file
	// (e.g., m.SelectAll() or m.SetSelection(...)). The exact API may
	// differ; consult internal/ui/messages/selection_test.go for the
	// existing pattern.
	m.SelectAll()

	got := m.SelectionText()
	if strings.ContainsRune(got, '\U0010EEEE') {
		t.Errorf("SelectionText contains U+10EEEE placeholder rune:\n%q", got)
	}
	// We do not assert that ":thumbsup:" is present — v1 substitutes
	// spaces; the richer mapping is a follow-up. We only verify that
	// no garbage bytes leak.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/messages/ -run TestSelectionText_ImageEmoji -v`
Expected: FAIL — the rendered line contains `\U0010EEEE\U0010EEEE` and that leaks straight through `ansi.Strip` + `plainLines` into the clipboard.

---

### Task 10.2: Implement `stripKittyPlaceholders` helper

**Files:**
- Modify: `internal/ui/messages/render.go`

- [ ] **Step 1: Add the helper**

Append to `internal/ui/messages/render.go`:

```go
// stripKittyPlaceholders replaces each contiguous run of kitty image
// placeholder runes (U+10EEEE) with the same number of ASCII spaces,
// AND drops the SGR foreground sequence that precedes each run
// (which encodes the image ID and is meaningless once the
// placeholder bytes are removed).
//
// Used by plainLines to scrub linesPlain so SelectionText output
// doesn't carry the U+10EEEE bytes into the OS clipboard. Same-
// width substitution preserves the column count so the selection
// slice still lands on the right offsets.
//
// v1 loses the emoji content (becomes spaces). A richer mapping
// that preserves ":name:" form is tracked as a follow-up; see
// docs/superpowers/specs/2026-05-27-emoji-as-images-design.md.
func stripKittyPlaceholders(s string) string {
	const placeholder = '\U0010EEEE'
	if !strings.ContainsRune(s, placeholder) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] != placeholder {
			b.WriteRune(runes[i])
			continue
		}
		// Found a placeholder run. Count consecutive placeholders.
		j := i
		for j < len(runes) && runes[j] == placeholder {
			j++
		}
		// Replace with the same number of spaces.
		for k := 0; k < j-i; k++ {
			b.WriteByte(' ')
		}
		i = j - 1
	}
	return b.String()
}
```

(SGR-stripping note: `ansi.Strip` — already called at the top of `plainLines` — removes the SGR foreground escape that precedes each placeholder run. By the time `stripKittyPlaceholders` runs, the input is post-`ansi.Strip` so the SGR is already gone. The helper handles only the placeholder runes themselves.)

- [ ] **Step 2: Wire the helper into `plainLines`**

Modify `plainLines` in `internal/ui/messages/render.go` (around line 676):

```go
func plainLines(s string) []plainLine {
	stripped := ansi.Strip(s)
	stripped = stripKittyPlaceholders(stripped)
	rawLines := strings.Split(stripped, "\n")
	out := make([]plainLine, len(rawLines))
	for i, line := range rawLines {
		out[i] = buildPlainLine(line)
	}
	return out
}
```

- [ ] **Step 3: Run tests to verify they pass**

Run: `go test ./internal/ui/messages/ -run TestSelectionText_ImageEmoji -v`
Expected: PASS.

Run: `go test ./internal/ui/messages/ -v -count=1`
Expected: no regression on existing selection / yank / plain-line tests.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/messages/render.go internal/ui/messages/selection_test.go
git commit -m "fix(messages): strip kitty placeholder runes from clipboard text"
```

---

### Task 10.3: Mirror in thread pane

**Files:**
- Modify: `internal/ui/thread/selection_test.go`

- [ ] **Step 1: Confirm whether thread uses the same `plainLines`**

The thread pane has its own selection logic but reuses `messages.PlainLine` and likely `plainLines` for parity. Search:

```bash
grep -n 'plainLines\|PlainLine' internal/ui/thread/*.go
```

If thread invokes `messages.plainLines` directly (or via a shared exported helper), the fix from Task 10.2 already covers it — no additional change needed. Verify with a thread selection test analogous to Task 10.1:

- [ ] **Step 2: Write a thread selection test**

Append to `internal/ui/thread/selection_test.go`:

```go
func TestThreadSelectionText_ImageEmoji_NoPlaceholderRunesInClipboard(t *testing.T) {
	emojiutil.SetImageMode(true, 2)
	t.Cleanup(func() { emojiutil.SetImageMode(false, 2) })

	thumbURL := emojiutil.CDNBaseURL + "1f44d.png"
	ff := newFakePlaceFetcher() // local fake helper
	ff.setPrerendered(emojiutil.EmojiCacheKey(thumbURL), goimage.Pt(2, 1), imgpkg.Render{
		Cells: goimage.Pt(2, 1),
		Lines: []string{"\U0010EEEE\U0010EEEE"},
	})

	m := NewModel()
	m.SetSize(80, 24)
	m.SetEmojiContext(EmojiContext{
		PlaceCtx: emojiutil.PlaceContext{Fetcher: ff},
		Cells:    2,
	})
	m.SetReplies("C1", "1.0", []messages.MessageItem{
		{TS: "1.1", UserName: "alice", UserID: "U1", Text: "reply :thumbsup:"},
	})
	_ = m.View()

	m.SelectAll() // or whatever the thread test helper is

	got := m.SelectionText()
	if strings.ContainsRune(got, '\U0010EEEE') {
		t.Errorf("thread SelectionText contains U+10EEEE:\n%q", got)
	}
}
```

- [ ] **Step 3: Run test**

Run: `go test ./internal/ui/thread/ -run TestThreadSelectionText_ImageEmoji -v`
Expected: PASS if thread reuses `messages.plainLines`; FAIL if thread has its own plain-line construction.

If FAIL: locate the thread's plain-line construction (search for `linesPlain` in `internal/ui/thread/`), and apply the same `stripKittyPlaceholders` call there. If the helper isn't exported, either export it from messages (`StripKittyPlaceholders`) or duplicate the small function in the thread package.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/thread/selection_test.go
# add any thread package files that needed the same fix
git commit -m "test(thread): confirm clipboard text strips kitty placeholders"
```

---

### Task 10.4: Document the v1 limitation

**Files:**
- Modify: `docs/superpowers/specs/2026-05-27-emoji-as-images-design.md` (add an addendum) OR
- Modify: `wiki/Terminal-Compatibility.md` (user-facing note)

- [ ] **Step 1: Add an addendum to the design spec**

Append to `docs/superpowers/specs/2026-05-27-emoji-as-images-design.md`:

```markdown

## Addendum (Phase 10 implementation discovery)

Yank/copy-to-clipboard of a selection covering image-rendered emoji produces spaces
where the emoji were (one space per cell of the image's footprint, so a 2-cell emoji
becomes two spaces). The emoji content does not survive to the clipboard in v1.

Reason: maintaining selection-rectangle accuracy requires `linesPlain` widths to match
`linesNormal` widths per line. A 2-cell kitty image placement (rendered) cannot be
substituted by a 10-cell `:thumbsup:` literal in `linesPlain` without breaking the
column→byte mapping used by `SelectionText`. v1 takes the simpler same-width
substitution; the richer mapping (preserving `:name:` form via a custom column→byte
map that treats placement runs as fixed-cell-width / variable-byte-length tokens) is
post-v1 work.

Workaround for users who need `:name:` form on yank: set `emoji_images = "off"` in
config.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-05-27-emoji-as-images-design.md
git commit -m "docs(emoji-as-images): document yank-loses-emoji v1 limitation"
```

---

### Task 10.5: Final phase check

- [ ] **Step 1: Build the full project**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./... -count=1`
Expected: no new failures.

- [ ] **Step 3: Manual smoke (recommended)**

In a real kitty terminal: open slk, select a message containing an emoji (mouse drag, or whatever selection keybind is used), copy via the standard keystroke (`Ctrl+C` or the documented yank). Paste into another terminal / app. Verify:
- The pasted text does NOT contain garbled bytes (no `􏻮`-like characters).
- The pasted text is readable; emoji positions show as spaces.

Phase 10 complete. Clipboard text is safe. Continue to `11-docs.md`.
