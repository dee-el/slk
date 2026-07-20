package blockkit

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	tableMaxRows         = 100
	tableMaxCols         = 20
	tableMinCellWidth    = 3
	tableDesiredWidthCap = 30
	osc8Prefix           = "\x1b]8;;"
	osc8TermESC          = "\x1b\\"
	osc8TermBEL          = "\a"
)

type preparedTable struct {
	Rows    [][]preparedTableCell
	Columns []TableColumn
}

type preparedTableCell struct {
	Text  string
	Lines []string
}

func appendTable(out *RenderResult, table TableBlock, ctx Context, width int) {
	out.Lines = append(out.Lines, renderTable(table, ctx, width)...)
}

func renderTable(table TableBlock, ctx Context, width int) []string {
	if len(table.Rows) == 0 || width <= 0 {
		return nil
	}

	prepared := prepareTable(table, ctx)
	columnCount := preparedTableColumnCount(prepared)
	if columnCount == 0 {
		return appendTableSummary(renderStackedTable(prepared, width, ctx.WrapText), table, width)
	}
	if width-columnCount-1 < columnCount*tableMinCellWidth {
		return appendTableSummary(renderStackedTable(prepared, width, ctx.WrapText), table, width)
	}

	widths := measurePreparedTableColumns(prepared, width)
	if len(widths) == 0 {
		return appendTableSummary(renderStackedTable(prepared, width, ctx.WrapText), table, width)
	}

	vertical := dividerStyle().Render("│")
	blankCells := make([]string, len(widths))
	for i, colWidth := range widths {
		blankCells[i] = strings.Repeat(" ", colWidth)
	}

	lines := []string{tableBorder('┌', '┬', '┐', widths)}
	for rowIdx, row := range prepared.Rows {
		cells := make([][]string, len(widths))
		rowHeight := 1
		for colIdx, colWidth := range widths {
			cell := preparedTableCellAt(row, colIdx)
			column := preparedTableColumnAt(prepared, colIdx)
			cells[colIdx] = renderPreparedTableCell(cell, column, ctx.WrapText, colWidth, false)
			if len(cells[colIdx]) > rowHeight {
				rowHeight = len(cells[colIdx])
			}
		}
		for lineIdx := 0; lineIdx < rowHeight; lineIdx++ {
			parts := make([]string, 0, len(widths)*2+1)
			parts = append(parts, vertical)
			for colIdx := range widths {
				line := blankCells[colIdx]
				if lineIdx < len(cells[colIdx]) {
					line = cells[colIdx][lineIdx]
				}
				parts = append(parts, line, vertical)
			}
			lines = append(lines, strings.Join(parts, ""))
		}
		if rowIdx < len(prepared.Rows)-1 {
			lines = append(lines, tableBorder('├', '┼', '┤', widths))
		}
	}
	lines = append(lines, tableBorder('└', '┴', '┘', widths))
	return appendTableSummary(lines, table, width)
}

func renderStackedTable(table preparedTable, width int, wrapText func(string, int) string) []string {
	if width <= 0 {
		return nil
	}

	lines := make([]string, 0, len(table.Rows))
	for rowIdx, row := range table.Rows {
		rowLabel := truncateTableLine(fmt.Sprintf("Row %d", rowIdx+1), width)
		lines = append(lines, headerStyle().Render(rowLabel))
		for colIdx := range row {
			labelPlain := fmt.Sprintf("C%d: ", colIdx+1)
			labelWidth := lipgloss.Width(labelPlain)
			if labelWidth >= width {
				lines = append(lines, mutedStyle().Render(truncateTableLine(labelPlain, width)))
				for _, cellLine := range renderPreparedTableCell(row[colIdx], preparedTableColumnAt(table, colIdx), wrapText, width, true) {
					lines = append(lines, cellLine)
				}
				continue
			}
			label := mutedStyle().Render(labelPlain)
			contentWidth := width - labelWidth
			if contentWidth < 1 {
				contentWidth = 1
			}
			cellLines := renderPreparedTableCell(row[colIdx], preparedTableColumnAt(table, colIdx), wrapText, contentWidth, true)
			for lineIdx, cellLine := range cellLines {
				prefix := strings.Repeat(" ", labelWidth)
				if lineIdx == 0 {
					prefix = label
				}
				lines = append(lines, prefix+cellLine)
			}
		}
	}
	return lines
}

func appendTableSummary(lines []string, table TableBlock, width int) []string {
	if !table.RowsTruncated && !table.ColsTruncated {
		return lines
	}
	summary := fmt.Sprintf("[table truncated: showing %d rows x %d columns]", len(table.Rows), len(table.Columns))
	return append(lines, mutedStyle().Render(truncateTableLine(summary, width)))
}

func normalizeTableCellText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

func prepareTable(table TableBlock, ctx Context) preparedTable {
	out := preparedTable{Columns: table.Columns}
	if len(table.Rows) == 0 {
		return out
	}
	out.Rows = make([][]preparedTableCell, len(table.Rows))
	for rowIdx, row := range table.Rows {
		if len(row) == 0 {
			continue
		}
		out.Rows[rowIdx] = make([]preparedTableCell, len(row))
		for colIdx, cell := range row {
			out.Rows[rowIdx][colIdx] = prepareTableCell(cell, ctx)
		}
	}
	return out
}

func prepareTableCell(cell TableCell, ctx Context) preparedTableCell {
	text := normalizeTableCellText(cell.Text)
	if ctx.RenderText != nil {
		text = ctx.RenderText(text, ctx.UserNames)
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	return preparedTableCell{Text: text, Lines: lines}
}

func measureTableColumns(table TableBlock, ctx Context, width int) []int {
	return measurePreparedTableColumns(prepareTable(table, ctx), width)
}

func measurePreparedTableColumns(table preparedTable, width int) []int {
	columnCount := preparedTableColumnCount(table)
	if columnCount == 0 {
		return nil
	}

	available := width - columnCount - 1
	if available < columnCount*tableMinCellWidth {
		return nil
	}

	widths := make([]int, columnCount)
	desired := make([]int, columnCount)
	for i := range widths {
		widths[i] = tableMinCellWidth
		desired[i] = tableMinCellWidth
	}

	for _, row := range table.Rows {
		for colIdx := 0; colIdx < columnCount && colIdx < len(row); colIdx++ {
			cellWidth := preparedTableDesiredWidth(row[colIdx])
			if cellWidth > desired[colIdx] {
				desired[colIdx] = cellWidth
			}
		}
	}

	remaining := available - columnCount*tableMinCellWidth
	for remaining > 0 {
		progressed := false
		for i := range widths {
			if remaining == 0 {
				break
			}
			if widths[i] >= desired[i] {
				continue
			}
			widths[i]++
			remaining--
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return widths
}

func renderTableCell(cell TableCell, column TableColumn, ctx Context, width int) []string {
	return renderPreparedTableCell(prepareTableCell(cell, ctx), column, ctx.WrapText, width, false)
}

func renderPreparedTableCell(cell preparedTableCell, column TableColumn, wrapText func(string, int) string, width int, forceWrap bool) []string {
	if width <= 0 {
		return []string{""}
	}

	logicalLines := cell.Lines
	if len(logicalLines) == 0 {
		logicalLines = []string{""}
	}

	out := make([]string, 0, len(logicalLines))
	for _, line := range logicalLines {
		if forceWrap || column.Wrapped {
			for _, wrapped := range wrapTableLine(line, wrapText, width) {
				align := column.Align
				if forceWrap {
					align = TableAlignLeft
				}
				out = append(out, alignTableLine(wrapped, align, width))
			}
			continue
		}
		if lipgloss.Width(line) > width {
			line = truncateTableLine(line, width)
		}
		out = append(out, alignTableLine(line, column.Align, width))
	}
	if len(out) == 0 {
		return []string{strings.Repeat(" ", width)}
	}
	return out
}

func alignTableLine(line string, align TableAlignment, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) > width {
		line = truncateTableLine(line, width)
	}
	pad := width - lipgloss.Width(line)
	if pad <= 0 {
		return line
	}
	switch align {
	case TableAlignRight:
		return strings.Repeat(" ", pad) + line
	case TableAlignCenter:
		left := pad / 2
		right := pad - left
		return strings.Repeat(" ", left) + line + strings.Repeat(" ", right)
	default:
		return line + strings.Repeat(" ", pad)
	}
}

func tableBorder(left, middle, right rune, widths []int) string {
	if len(widths) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteRune(left)
	for i, width := range widths {
		b.WriteString(strings.Repeat("─", width))
		if i < len(widths)-1 {
			b.WriteRune(middle)
		}
	}
	b.WriteRune(right)
	return dividerStyle().Render(b.String())
}

func preparedTableColumnCount(table preparedTable) int {
	count := len(table.Columns)
	for _, row := range table.Rows {
		if len(row) > count {
			count = len(row)
		}
	}
	return count
}

func preparedTableColumnAt(table preparedTable, idx int) TableColumn {
	if idx >= 0 && idx < len(table.Columns) {
		return table.Columns[idx]
	}
	return defaultTableColumn()
}

func preparedTableCellAt(row []preparedTableCell, idx int) preparedTableCell {
	if idx >= 0 && idx < len(row) {
		return row[idx]
	}
	return preparedTableCell{Lines: []string{""}}
}

func tableCellText(cell TableCell, ctx Context) string {
	return prepareTableCell(cell, ctx).Text
}

func tableDesiredWidth(cell TableCell, ctx Context) int {
	return preparedTableDesiredWidth(prepareTableCell(cell, ctx))
}

func preparedTableDesiredWidth(cell preparedTableCell) int {
	maxWidth := tableMinCellWidth
	for _, line := range cell.Lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > tableDesiredWidthCap {
			lineWidth = tableDesiredWidthCap
		}
		if lineWidth > maxWidth {
			maxWidth = lineWidth
		}
	}
	return maxWidth
}

func wrapTableLine(line string, wrapText func(string, int) string, width int) []string {
	if width <= 0 {
		return []string{""}
	}

	wrapped := line
	if wrapText != nil {
		wrapped = wrapText(line, width)
	} else {
		wrapped = ansi.Wrap(line, width, "")
	}
	if wrapped == "" {
		return []string{""}
	}
	wrapped = hardwrapTableOverlongLines(wrapped, width)
	wrapped = balanceTableHyperlinks(wrapped)
	out := strings.Split(wrapped, "\n")
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func hardwrapTableOverlongLines(s string, width int) string {
	if width <= 0 || s == "" {
		return s
	}
	parts := make([]string, 0, strings.Count(s, "\n")+1)
	for _, line := range strings.Split(s, "\n") {
		if lipgloss.Width(line) <= width {
			parts = append(parts, line)
			continue
		}
		parts = append(parts, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
	}
	return strings.Join(parts, "\n")
}

func balanceTableHyperlinks(s string) string {
	if !strings.Contains(s, osc8Prefix) || !strings.Contains(s, "\n") {
		return s
	}
	var out strings.Builder
	activeURL := ""
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], osc8Prefix) {
			seq, url, next, ok := consumeTableOSC8(s, i)
			if !ok {
				out.WriteString(s[i:])
				break
			}
			out.WriteString(seq)
			activeURL = url
			i = next
			continue
		}
		if s[i] == '\n' && activeURL != "" {
			out.WriteString(tableOSC8Close())
			out.WriteByte('\n')
			out.WriteString(tableOSC8Open(activeURL))
			i++
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func consumeTableOSC8(s string, start int) (seq, url string, next int, ok bool) {
	if !strings.HasPrefix(s[start:], osc8Prefix) {
		return "", "", start, false
	}
	rest := s[start+len(osc8Prefix):]
	idxESC := strings.Index(rest, osc8TermESC)
	idxBEL := strings.Index(rest, osc8TermBEL)
	term := ""
	idx := -1
	switch {
	case idxESC >= 0 && (idxBEL < 0 || idxESC < idxBEL):
		idx = idxESC
		term = osc8TermESC
	case idxBEL >= 0:
		idx = idxBEL
		term = osc8TermBEL
	default:
		return "", "", start, false
	}
	url = rest[:idx]
	next = start + len(osc8Prefix) + idx + len(term)
	return s[start:next], url, next, true
}

func tableOSC8Open(url string) string {
	return osc8Prefix + url + osc8TermESC
}

func tableOSC8Close() string {
	return osc8Prefix + osc8TermESC
}

func truncateTableLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	return ansi.Truncate(line, width, "...")
}
