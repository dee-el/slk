// Package searchresults is the workspace-wide message search modal
// (ctrl+f). Unlike channelfinder it does not filter locally: Enter
// submits the query to Slack's search.messages and the caller injects
// results via SetResults/SetError.
package searchresults

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/gammons/slk/internal/ui/messages"
	"github.com/gammons/slk/internal/ui/overlay"
	"github.com/gammons/slk/internal/ui/styles"
	"github.com/muesli/reflow/truncate"
)

// Item is one search hit, rendered as
// "#channel  author  time  snippet". Built from Slack's search
// response, so uncached channels/users display fine.
type Item struct {
	ChannelID   string
	ChannelName string
	UserName    string
	TS          string
	ThreadTS    string // non-empty: hit is a thread reply
	Text        string
	Timestamp   string // pre-formatted for display
	IsDM        bool   // render the channel as "@name" instead of "#name"
}

// Action tells the mode handler what a keypress did.
type Action int

const (
	ActionNone   Action = iota
	ActionSubmit        // Enter on a non-empty query: run the search
	ActionSelect        // Enter on a result row: jump to it
	ActionClose         // Esc: modal closed
)

type state int

const (
	stateInput state = iota
	stateLoading
	stateResults
	stateError
)

// Model is the workspace search modal.
type Model struct {
	visible  bool
	query    string
	items    []Item
	selected int
	st       state
	errMsg   string
	total    int
}

// New creates a new search results modal.
func New() Model { return Model{} }

// Open shows the modal and resets state.
func (m *Model) Open() {
	m.visible = true
	m.query = ""
	m.items = nil
	m.selected = 0
	m.st = stateInput
	m.errMsg = ""
	m.total = 0
}

// Close hides the modal.
func (m *Model) Close() { m.visible = false }

// IsVisible returns whether the modal is showing.
func (m Model) IsVisible() bool { return m.visible }

// Query returns the current query text.
func (m Model) Query() string { return m.query }

// Loading reports whether a search is in flight.
func (m Model) Loading() bool { return m.st == stateLoading }

// SetResults installs server results for the in-flight query.
func (m *Model) SetResults(items []Item, total int) {
	if m.st != stateLoading {
		return // defense against stale async injection; the caller also guards by query
	}
	m.items = items
	m.total = total
	m.selected = 0
	m.st = stateResults
}

// SetError shows an error line; the query is preserved for retry.
func (m *Model) SetError(msg string) {
	if m.st != stateLoading {
		return // defense against stale async injection; the caller also guards by query
	}
	// Flatten so a multi-line error can't desync BoxSize from the render.
	m.errMsg = flattenText(msg)
	m.st = stateError
}

// Selected returns the highlighted result.
func (m Model) Selected() (Item, bool) {
	if m.st != stateResults || m.selected < 0 || m.selected >= len(m.items) {
		return Item{}, false
	}
	return m.items[m.selected], true
}

// HandleKey processes a normalized key string ("enter", "esc", "up",
// "down", "backspace", "space", or a printable rune) and reports the
// action.
func (m *Model) HandleKey(keyStr string) Action {
	switch keyStr {
	case "esc":
		m.Close()
		return ActionClose
	case "enter":
		if m.st == stateLoading {
			// A search is already in flight; re-submitting would fire
			// duplicate rate-limited search.messages calls.
			return ActionNone
		}
		if m.st == stateResults {
			if _, ok := m.Selected(); ok {
				return ActionSelect
			}
			return ActionNone
		}
		if m.query == "" {
			return ActionNone
		}
		m.st = stateLoading
		return ActionSubmit
	case "up", "ctrl+k", "ctrl+p":
		if m.st == stateResults && m.selected > 0 {
			m.selected--
		}
	case "down", "ctrl+j", "ctrl+n":
		if m.st == stateResults && m.selected < len(m.items)-1 {
			m.selected++
		}
	case "backspace":
		if m.query != "" {
			r := []rune(m.query)
			m.query = string(r[:len(r)-1])
			m.st = stateInput
		}
	case "space":
		// bubbletea v2's Key.String() renders a literal space as
		// "space"; queries can be multi-term, so map it back.
		m.query += " "
		m.st = stateInput
	default:
		if len([]rune(keyStr)) == 1 {
			m.query += keyStr
			m.st = stateInput
		}
	}
	return ActionNone
}

// listTopOffset is the box-local row of the first body row: top border
// (1) + top padding (1) + title (1) + input (1) + blank separator (1).
// Shared by renderBox (implicitly) and ClickRow's hit-testing.
const listTopOffset = 5

// rowLines is how many screen lines each result row occupies: a header
// line (#channel  author  time  snippet...), a snippet continuation,
// and a blank separator line.
const rowLines = 3

// ClickRow maps a box-local row (localY, 0 = box top border) to a result
// row. Result rows are rowLines tall; a click on any line of a row
// (blank separator included) selects it. When the click lands on a
// visible list row it moves the selection there and returns true;
// otherwise it returns false.
// termHeight feeds the same window clamp the renderer uses; termWidth
// is accepted for interface symmetry and currently unused.
func (m *Model) ClickRow(termWidth, termHeight, localY int) bool {
	if m.st != stateResults {
		// Body rows in the input/loading/error states aren't results.
		return false
	}
	line := localY - listTopOffset
	if line < 0 {
		return false
	}
	row := line / rowLines
	start, end := m.visibleWindow(termHeight)
	if row >= end-start {
		return false
	}
	m.selected = start + row
	return true
}

// flattenText collapses a multi-line string into a single screen line:
// \n and \t become spaces, and all other control runes (\r, BEL, ...)
// are dropped. Control characters break ANSI-aware width math and the
// box alignment that depends on it.
func flattenText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r):
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// boxWidth returns the modal's outer width for a given terminal width:
// 70% of the terminal, floored at 40 columns. Single source of truth
// for renderBox and BoxSize.
func boxWidth(termWidth int) int {
	w := termWidth * 7 / 10
	if w < 40 {
		w = 40
	}
	return w
}

// visibleRowCap returns the scroll-window height in result rows for a
// terminal of termHeight lines: as many rows as fit the box's height
// budget — 70% of the terminal, clamped to termHeight-2 — once the
// chrome (and optional footer) is spent, but never below 1 row.
// Single source of truth for visibleWindow, so the renderer, BoxSize,
// and ClickRow all clamp identically (the rows*rowLines + chrome ≤
// budget invariant).
func (m Model) visibleRowCap(termHeight int) int {
	// Chrome: top border + top padding + title + input + blank +
	// bottom padding + bottom border = 7 (see BoxSize), plus the
	// "showing K of N" footer when the server reported more matches.
	chrome := 7
	if m.total > len(m.items) {
		chrome++
	}
	budget := termHeight * 7 / 10
	if budget > termHeight-2 {
		budget = termHeight - 2
	}
	n := (budget - chrome) / rowLines
	if n < 1 {
		n = 1
	}
	return n
}

// visibleWindow returns the [start, end) slice of m.items currently
// shown in the results list for a terminal of termHeight lines,
// applying the same scroll-window math the renderer uses.
func (m *Model) visibleWindow(termHeight int) (int, int) {
	maxVisible := m.visibleRowCap(termHeight)
	total := len(m.items)
	if maxVisible > total {
		maxVisible = total
	}
	startIdx := 0
	if m.selected >= maxVisible {
		startIdx = m.selected - maxVisible + 1
	}
	endIdx := startIdx + maxVisible
	if endIdx > total {
		endIdx = total
		startIdx = endIdx - maxVisible
		if startIdx < 0 {
			startIdx = 0
		}
	}
	return startIdx, endIdx
}

// BoxSize returns the outer dimensions of the rendered modal box for the
// given terminal size. termHeight clamps the visible result rows so the
// outer box fits within the terminal (see visibleWindow).
func (m *Model) BoxSize(termWidth, termHeight int) (int, int) {
	nRows := len(m.bodyLines(boxWidth(termWidth)-4, termHeight))
	if nRows < 1 {
		nRows = 1
	}
	// height = top border + top padding + title + input + blank + rows +
	// bottom padding + bottom border = nRows + 7.
	return boxWidth(termWidth), nRows + 7
}

// View renders just the overlay box.
func (m Model) View(termWidth, termHeight int) string {
	return m.renderBox(termWidth, termHeight)
}

// ViewOverlay renders the overlay as a centered modal with a dark backdrop.
func (m Model) ViewOverlay(termWidth, termHeight int, background string) string {
	if !m.visible {
		return background
	}

	box := m.renderBox(termWidth, termHeight)
	if box == "" {
		return background
	}

	return overlay.DimmedOverlay(termWidth, termHeight, background, box, 0.5)
}

func (m Model) renderBox(termWidth, termHeight int) string {
	if !m.visible {
		return ""
	}

	// Overlay dimensions
	overlayWidth := boxWidth(termWidth)
	innerWidth := overlayWidth - 4 // border + padding

	// All inner spans share the modal bg so the dimmed app behind the
	// overlay doesn't bleed through where individual styled fragments end.
	bg := styles.Background

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Background(bg).
		Foreground(styles.Primary).
		Render("Search Workspace")

	// Query input with blue left border
	var inputText string
	if m.query == "" {
		placeholder := lipgloss.NewStyle().Background(bg).Foreground(styles.TextMuted).Render("Type a query, Enter to search...")
		inputText = "█ " + placeholder
	} else {
		// Truncate head-side (keep the tail and the cursor visible) so a
		// long query can't wrap the input line and desync BoxSize. The
		// input style spends 2 cols (left border + padding) of innerWidth.
		inputText = m.query + "█"
		if avail := innerWidth - 2; lipgloss.Width(inputText) > avail {
			r := []rune(inputText)
			for len(r) > 0 && lipgloss.Width("…"+string(r)) > avail {
				r = r[1:]
			}
			inputText = "…" + string(r)
		}
	}
	input := lipgloss.NewStyle().
		BorderStyle(lipgloss.Border{Left: "▌"}).
		BorderLeft(true).
		BorderForeground(styles.Primary).
		BorderBackground(bg).
		PaddingLeft(1).
		Background(bg).
		Foreground(styles.TextPrimary).
		Render(inputText)

	content := title + "\n" + input + "\n\n" + strings.Join(m.bodyLines(innerWidth, termHeight), "\n")

	// Re-paint modal bg+fg after every ANSI reset emitted by inner styled
	// spans so trailing cells don't inherit the dimmed app behind the
	// overlay.
	content = messages.ReapplyBgAfterResets(content, messages.BgANSI()+messages.FgANSI())

	// Wrap in a bordered box
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.Primary).
		BorderBackground(bg).
		Background(bg).
		Padding(1, 1).
		Width(overlayWidth).
		Render(content)
}

// bodyLines renders the rows below the input line for the current state:
// a spinner line while loading, the error line, a "No results"
// placeholder, or the result rows plus an optional "showing K of N"
// footer. innerWidth is the usable content width inside the box;
// termHeight clamps the result-row window (see visibleWindow).
func (m Model) bodyLines(innerWidth, termHeight int) []string {
	bg := styles.Background
	muted := lipgloss.NewStyle().Background(bg).Foreground(styles.TextMuted)

	switch m.st {
	case stateLoading:
		return []string{muted.Italic(true).Render("Searching…")}
	case stateError:
		errLine := lipgloss.NewStyle().Background(bg).Foreground(styles.Error).Render(m.errMsg)
		if lipgloss.Width(errLine) > innerWidth {
			errLine = truncate.StringWithTail(errLine, uint(innerWidth), "…")
		}
		return []string{errLine}
	case stateResults:
		if len(m.items) == 0 {
			return []string{muted.Italic(true).Render("No results")}
		}
		return m.resultRows(innerWidth, termHeight)
	default: // stateInput
		return []string{""}
	}
}

// splitAtWidth splits plain (ANSI-free) text at the widest cell
// boundary that fits within w columns: head is at most w cells wide,
// tail is the untouched remainder. Wide runes are never split.
// Caveat: the boundary is rune-based, so a multi-rune grapheme cluster
// (e.g. a ZWJ emoji sequence 👩‍🚀) can be split across head and tail —
// each half renders as its constituent runes rather than the composed
// glyph. Cosmetic only; no bytes are lost.
func splitAtWidth(s string, w int) (head, tail string) {
	if w <= 0 {
		return "", s
	}
	if lipgloss.Width(s) <= w {
		return s, ""
	}
	// For ANSI-free input truncate.String returns a byte prefix of s,
	// so slicing off len(head) bytes yields the exact remainder.
	head = truncate.String(s, uint(w))
	return head, s[len(head):]
}

// resultRows renders the visible window of result rows plus the
// "showing K of N" footer when the server reported more matches than
// were fetched. Each result is rowLines (3) screen lines: a header line
// ("#channel  author  timestamp  snippet..."), a snippet continuation
// line truncated with "…" when more remains, and a blank separator.
// When the fetched list overflows the visible window a proportional
// scrollbar gutter is drawn on the right (same pattern as
// channelfinder/workspacefinder/themeswitcher), spanning all lines of
// each row — separator included, for gutter continuity.
func (m Model) resultRows(innerWidth, termHeight int) []string {
	bg := styles.Background

	total := len(m.items)
	startIdx, endIdx := m.visibleWindow(termHeight)
	maxVisible := endIdx - startIdx

	showScrollbar := total > maxVisible
	contentWidth := innerWidth - 1 // 1 col indicator/space prefix
	if showScrollbar {
		contentWidth-- // 1 col for the scrollbar gutter
	}

	var thumbStart, thumbEnd int
	if showScrollbar {
		thumbHeight := maxVisible * maxVisible / total
		if thumbHeight < 1 {
			thumbHeight = 1
		}
		denom := total - maxVisible
		if denom < 1 {
			denom = 1
		}
		thumbStart = startIdx * (maxVisible - thumbHeight) / denom
		if thumbStart < 0 {
			thumbStart = 0
		}
		if thumbStart > maxVisible-thumbHeight {
			thumbStart = maxVisible - thumbHeight
		}
		thumbEnd = thumbStart + thumbHeight
	}
	thumbStyle := lipgloss.NewStyle().Background(bg).Foreground(styles.Primary)
	trackStyle := lipgloss.NewStyle().Background(bg).Foreground(styles.Border)

	var rows []string
	for i := startIdx; i < endIdx; i++ {
		item := m.items[i]
		isSelected := i == m.selected

		// Render fragments separately (see channelfinder): a single
		// outer style over pre-styled text would lose attributes
		// after each inner ANSI reset.
		chanStyle := lipgloss.NewStyle().Background(bg).Foreground(styles.TextMuted)
		nameStyle := lipgloss.NewStyle().Background(bg).Foreground(styles.TextPrimary)
		textStyle := lipgloss.NewStyle().Background(bg).Foreground(styles.TextPrimary)
		if isSelected {
			nameStyle = nameStyle.Foreground(styles.Primary).Bold(true)
			textStyle = textStyle.Foreground(styles.Primary).Bold(true)
		}

		sigil := "#"
		if item.IsDM {
			sigil = "@"
		}
		snippet := flattenText(item.Text)
		// Header fields are flattened too: a control rune in a channel
		// or author name would break the width math below.
		channelName := flattenText(item.ChannelName)
		userName := flattenText(item.UserName)

		// Line 1: header + as much snippet as fits. The plain header
		// width (snippet is plain too — flattenText emits no ANSI)
		// decides the split point.
		headerPlain := sigil + channelName + "  " + userName + "  " + item.Timestamp + "  "
		part1, rest := splitAtWidth(snippet, contentWidth-lipgloss.Width(headerPlain))
		line1 := chanStyle.Render(sigil+channelName) + "  " +
			nameStyle.Render(userName) + "  " +
			chanStyle.Render(item.Timestamp) + "  " +
			textStyle.Render(part1)
		// Defensive: an overlong header (part1 already "") still must
		// not wrap the box. truncate.StringWithTail is ANSI-aware.
		if lipgloss.Width(line1) > contentWidth {
			line1 = truncate.StringWithTail(line1, uint(contentWidth), "…")
		}

		// Line 2: snippet continuation, truncated with "…" if more
		// remains; blank when the snippet fit on line 1.
		line2 := ""
		if rest = strings.TrimLeft(rest, " "); rest != "" {
			if lipgloss.Width(rest) > contentWidth {
				rest = truncate.StringWithTail(rest, uint(contentWidth), "…")
			}
			line2 = textStyle.Render(rest)
		}

		// Line 3: a blank separator between rows (trailing one above
		// the footer/border included). It never carries the selected
		// indicator but does carry the scrollbar gutter rune.
		for li, line := range []string{line1, line2, ""} {
			separator := li == 2
			// Right-pad with spaces to fill the row.
			if pad := contentWidth - lipgloss.Width(line); pad > 0 {
				line += strings.Repeat(" ", pad)
			}
			var row string
			if isSelected && !separator {
				// Selected indicator spans the content lines of the row.
				indicator := lipgloss.NewStyle().Background(bg).Foreground(styles.Accent).Render("▌")
				row = indicator + line
			} else {
				row = " " + line
			}
			if showScrollbar {
				// Thumb math is row-based; all lines of a row share
				// its gutter rune, so the gutter stays proportional.
				rel := i - startIdx
				if rel >= thumbStart && rel < thumbEnd {
					row += thumbStyle.Render("█")
				} else {
					row += trackStyle.Render("│")
				}
			}
			rows = append(rows, row)
		}
	}

	if m.total > len(m.items) {
		footer := lipgloss.NewStyle().Background(bg).Foreground(styles.TextMuted).
			Render(fmt.Sprintf("showing %d of %d", len(m.items), m.total))
		rows = append(rows, footer)
	}
	return rows
}
