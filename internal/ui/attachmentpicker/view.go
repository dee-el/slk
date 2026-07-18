package attachmentpicker

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/gammons/slk/internal/ui/messages"
	"github.com/gammons/slk/internal/ui/overlay"
	"github.com/gammons/slk/internal/ui/styles"
	"github.com/muesli/reflow/truncate"
)

// ViewOverlay renders the picker over a dimmed background.
func (m *Model) ViewOverlay(termWidth, termHeight int, background string) string {
	if !m.visible {
		return background
	}
	box := m.renderBox(termWidth, termHeight)
	if box == "" {
		return background
	}
	return overlay.DimmedOverlay(termWidth, termHeight, background, box, 0.5)
}

// BoxSize reports the outer modal dimensions for mouse hit-testing.
func (m *Model) BoxSize(termWidth, termHeight int) (int, int) {
	box := m.renderBox(termWidth, termHeight)
	return lipgloss.Width(box), lipgloss.Height(box)
}

func (m *Model) renderBox(termWidth, termHeight int) string {
	width := termWidth * 7 / 10
	if width < 48 {
		width = 48
	}
	if width > 100 {
		width = 100
	}
	if width > termWidth-2 {
		width = termWidth - 2
	}
	if width < 8 {
		return ""
	}
	innerWidth := width - 4
	bg := styles.Background

	title := lipgloss.NewStyle().
		Bold(true).
		Background(bg).
		Foreground(styles.Primary).
		Render("Attach files")
	directory := m.currentDirectory
	if lipgloss.Width(directory) > innerWidth {
		directory = truncate.StringWithTail(directory, uint(innerWidth), "...")
	}
	directoryLine := lipgloss.NewStyle().Background(bg).Foreground(styles.TextMuted).Render(directory)

	start, end := m.visibleWindow(termHeight)
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		item := m.items[i]
		marker := "[ ]"
		size := humanSize(item.Size)
		if item.IsDir {
			marker = "[dir]"
			size = ""
		} else if _, ok := m.selected[canonicalPath(item.Path)]; ok {
			marker = "[x]"
		}
		indicator := " "
		if i == m.cursor {
			indicator = "▌"
		}
		fixedWidth := lipgloss.Width(indicator) + 2 + lipgloss.Width(marker)
		if size != "" {
			fixedWidth += 2 + lipgloss.Width(size)
		}
		nameWidth := innerWidth - fixedWidth
		if nameWidth < 1 {
			nameWidth = 1
		}
		name := item.Name
		if lipgloss.Width(name) > nameWidth {
			name = truncate.StringWithTail(name, uint(nameWidth), "...")
		}
		name = lipgloss.NewStyle().Background(bg).Width(nameWidth).Render(name)
		rowText := indicator + " " + marker + " " + name
		if size != "" {
			rowText += "  " + size
		}
		rowStyle := lipgloss.NewStyle().Background(bg).Foreground(styles.TextPrimary)
		if i == m.cursor {
			rowStyle = rowStyle.Foreground(styles.Primary).Bold(true)
		}
		rows = append(rows, rowStyle.Render(rowText))
	}
	if len(rows) == 0 {
		text := "No files"
		if m.loading {
			text = "Loading..."
		}
		rows = append(rows, lipgloss.NewStyle().Background(bg).Foreground(styles.TextMuted).Render(text))
	}

	count := m.reservedCount + len(m.selected)
	footer := fmt.Sprintf("space toggle  a attach  esc cancel  %d/%d selected", count, m.maxSelected)
	if lipgloss.Width(footer) > innerWidth {
		footer = truncate.StringWithTail(footer, uint(innerWidth), "...")
	}
	footerLine := lipgloss.NewStyle().Background(bg).Foreground(styles.TextMuted).Render(footer)

	lines := []string{title, directoryLine}
	lines = append(lines, rows...)
	if m.errText != "" {
		errText := m.errText
		if lipgloss.Width(errText) > innerWidth {
			errText = truncate.StringWithTail(errText, uint(innerWidth), "...")
		}
		lines = append(lines, lipgloss.NewStyle().Background(bg).Foreground(styles.Error).Render(errText))
	}
	lines = append(lines, footerLine)
	content := messages.ReapplyBgAfterResets(strings.Join(lines, "\n"), messages.BgANSI()+messages.FgANSI())

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.Primary).
		BorderBackground(bg).
		Background(bg).
		Padding(1, 1).
		Width(width).
		Render(content)
}

func (m *Model) visibleWindow(termHeight int) (int, int) {
	visible := visibleRows(termHeight)
	if visible > len(m.items) {
		visible = len(m.items)
	}
	start := 0
	if m.cursor >= visible && visible > 0 {
		start = m.cursor - visible + 1
	}
	end := start + visible
	if end > len(m.items) {
		end = len(m.items)
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func visibleRows(termHeight int) int {
	n := termHeight - 10
	if n < 1 {
		n = 1
	}
	if n > 20 {
		n = 20
	}
	return n
}

func humanSize(size int64) string {
	const kb = 1024
	const mb = 1024 * kb
	switch {
	case size >= mb:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(mb))
	case size >= kb:
		return fmt.Sprintf("%d KB", size/kb)
	default:
		return "<1 KB"
	}
}
