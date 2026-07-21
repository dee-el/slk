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
	tableMaxCellRunes    = 10000
	tableMaxCellLines    = 200
	tableCellEllipsis    = "..."
	osc8Prefix           = "\x1b]8;;"
	osc8TermESC          = "\x1b\\"
	osc8TermBEL          = "\a"
)

type preparedTable struct {
	Rows                 [][]preparedTableCell
	Columns              []TableColumn
	ContentTruncated     bool
	ContentTruncatedCell int
}

type preparedTableCell struct {
	Text      string
	Lines     []string
	Truncated bool
}

type tableRenderMeta struct {
	ContentTruncated      bool
	ContentTruncatedCells int
}

type tableCanvas struct {
	Lines  []string
	Width  int
	Height int
	Meta   tableRenderMeta
}

func (m *tableRenderMeta) addCell(truncated bool) {
	if !truncated {
		return
	}
	m.ContentTruncated = true
	m.ContentTruncatedCells++
}

func appendTable(out *RenderResult, table TableBlock, ctx Context, width int, path string) {
	key := TableKey{MessageTS: ctx.MessageTS, Path: path, BlockID: table.BlockID}
	lines, region, ok := renderTable(table, ctx, width, key)
	if !ok {
		return
	}
	region.LineStart += len(out.Lines)
	region.LineEnd += len(out.Lines)
	out.Lines = append(out.Lines, lines...)
	out.TableRegions = append(out.TableRegions, region)
}

func renderTable(table TableBlock, ctx Context, width int, key TableKey) ([]string, TableRegion, bool) {
	if len(table.Rows) == 0 || width <= 0 {
		return nil, TableRegion{}, false
	}

	prepared := prepareTable(table, ctx)
	focused := ctx.tableViewport(key)
	columnCount := preparedTableColumnCount(prepared)
	if width < 4 || columnCount == 0 {
		lines, meta := renderStackedTable(prepared, width, ctx.WrapText)
		lines = append(lines, tableSummaryLines(table, meta, width)...)
		return lines, TableRegion{
			Key:                   key,
			LineStart:             0,
			LineEnd:               len(lines),
			ViewWidth:             width,
			ViewHeight:            len(lines),
			FullWidth:             width,
			FullHeight:            len(lines),
			Focused:               focused.Focused,
			ContentTruncated:      meta.ContentTruncated,
			ContentTruncatedCells: meta.ContentTruncatedCells,
		}, len(lines) > 0
	}

	canvas := buildTableCanvas(prepared, ctx.WrapText)
	if canvas.Height == 0 || canvas.Width == 0 {
		lines, meta := renderStackedTable(prepared, width, ctx.WrapText)
		lines = append(lines, tableSummaryLines(table, meta, width)...)
		return lines, TableRegion{
			Key:                   key,
			LineStart:             0,
			LineEnd:               len(lines),
			ViewWidth:             width,
			ViewHeight:            len(lines),
			FullWidth:             width,
			FullHeight:            len(lines),
			Focused:               focused.Focused,
			ContentTruncated:      meta.ContentTruncated,
			ContentTruncatedCells: meta.ContentTruncatedCells,
		}, len(lines) > 0
	}
	summary := tableSummaryLines(table, canvas.Meta, width)

	maxHeight := focused.MaxHeight
	if maxHeight <= 0 {
		maxHeight = ctx.TableMaxHeight
	}
	if maxHeight <= 0 {
		maxHeight = canvas.Height + len(summary) + 2
	}
	if canvas.Width <= width && canvas.Height+len(summary) <= maxHeight {
		lines := append(append([]string{}, canvas.Lines...), summary...)
		return lines, TableRegion{
			Key:                   key,
			LineStart:             0,
			LineEnd:               len(lines),
			ViewWidth:             canvas.Width,
			ViewHeight:            canvas.Height,
			FullWidth:             canvas.Width,
			FullHeight:            canvas.Height,
			Focused:               focused.Focused,
			ContentTruncated:      canvas.Meta.ContentTruncated,
			ContentTruncatedCells: canvas.Meta.ContentTruncatedCells,
		}, true
	}

	lines, region := renderTableViewport(canvas, key, focused, width, len(summary), maxHeight)
	lines = append(lines, summary...)
	region.LineEnd = len(lines)
	return lines, region, true
}

func renderTableViewport(canvas tableCanvas, key TableKey, input TableViewportInput, width, summaryLines, maxHeight int) ([]string, TableRegion) {
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}

	budget := canvas.Height + 2
	if maxHeight > 0 {
		budget = maxHeight - summaryLines
		if budget < 3 {
			budget = 3
		}
	}
	innerHeight := budget - 2
	if innerHeight < 1 {
		innerHeight = 1
	}
	if innerHeight > canvas.Height {
		innerHeight = canvas.Height
	}

	maxX := canvas.Width - innerWidth
	if maxX < 0 {
		maxX = 0
	}
	maxY := canvas.Height - innerHeight
	if maxY < 0 {
		maxY = 0
	}
	xOffset := clampInt(input.XOffset, 0, maxX)
	yOffset := clampInt(input.YOffset, 0, maxY)

	region := TableRegion{
		Key:                   key,
		LineStart:             0,
		LineEnd:               innerHeight + 2,
		XOffset:               xOffset,
		YOffset:               yOffset,
		ViewWidth:             innerWidth,
		ViewHeight:            innerHeight,
		FullWidth:             canvas.Width,
		FullHeight:            canvas.Height,
		MaxX:                  maxX,
		MaxY:                  maxY,
		Focused:               input.Focused,
		ContentTruncated:      canvas.Meta.ContentTruncated,
		ContentTruncatedCells: canvas.Meta.ContentTruncatedCells,
	}

	lines := make([]string, 0, innerHeight+2)
	lines = append(lines, tableViewportTop(region))
	blank := strings.Repeat(" ", innerWidth)
	for row := 0; row < innerHeight; row++ {
		content := blank
		if src := yOffset + row; src < len(canvas.Lines) {
			content = clipTableLine(canvas.Lines[src], xOffset, innerWidth)
		}
		lines = append(lines, tableViewportRow(content, innerWidth))
	}
	lines = append(lines, tableViewportBottom(innerWidth))
	return lines, region
}

func buildTableCanvas(table preparedTable, wrapText func(string, int) string) tableCanvas {
	widths := measurePreparedTableColumns(table)
	if len(widths) == 0 {
		return tableCanvas{}
	}
	var meta tableRenderMeta

	vertical := dividerStyle().Render("│")
	blankCells := make([]string, len(widths))
	for i, colWidth := range widths {
		blankCells[i] = strings.Repeat(" ", colWidth)
	}

	lines := []string{tableBorder('┌', '┬', '┐', widths)}
	for rowIdx, row := range table.Rows {
		cells := make([][]string, len(widths))
		rowHeight := 1
		for colIdx, colWidth := range widths {
			cell := preparedTableCellAt(row, colIdx)
			column := preparedTableColumnAt(table, colIdx)
			var truncated bool
			cells[colIdx], truncated = renderPreparedTableCellTracked(cell, column, wrapText, colWidth, false)
			meta.addCell(truncated)
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
		if rowIdx < len(table.Rows)-1 {
			lines = append(lines, tableBorder('├', '┼', '┤', widths))
		}
	}
	lines = append(lines, tableBorder('└', '┴', '┘', widths))
	return tableCanvas{Lines: lines, Width: tableCanvasWidth(widths), Height: len(lines), Meta: meta}
}

func renderStackedTable(table preparedTable, width int, wrapText func(string, int) string) ([]string, tableRenderMeta) {
	if width <= 0 {
		return nil, tableRenderMeta{}
	}

	var meta tableRenderMeta
	lines := make([]string, 0, len(table.Rows))
	for rowIdx, row := range table.Rows {
		rowLabel := truncateTableLine(fmt.Sprintf("Row %d", rowIdx+1), width)
		lines = append(lines, headerStyle().Render(rowLabel))
		for colIdx := range row {
			labelPlain := fmt.Sprintf("C%d: ", colIdx+1)
			labelWidth := lipgloss.Width(labelPlain)
			if labelWidth >= width {
				lines = append(lines, mutedStyle().Render(truncateTableLine(labelPlain, width)))
				cellLines, truncated := renderPreparedTableCellTracked(row[colIdx], preparedTableColumnAt(table, colIdx), wrapText, width, true)
				meta.addCell(truncated)
				for _, cellLine := range cellLines {
					lines = append(lines, cellLine)
				}
				continue
			}
			label := mutedStyle().Render(labelPlain)
			contentWidth := width - labelWidth
			if contentWidth < 1 {
				contentWidth = 1
			}
			cellLines, truncated := renderPreparedTableCellTracked(row[colIdx], preparedTableColumnAt(table, colIdx), wrapText, contentWidth, true)
			meta.addCell(truncated)
			for lineIdx, cellLine := range cellLines {
				prefix := strings.Repeat(" ", labelWidth)
				if lineIdx == 0 {
					prefix = label
				}
				lines = append(lines, prefix+cellLine)
			}
		}
	}
	return lines, meta
}

func tableSummaryLines(table TableBlock, meta tableRenderMeta, width int) []string {
	var parts []string
	if table.RowsTruncated || table.ColsTruncated {
		parts = append(parts, fmt.Sprintf("showing %d rows x %d columns", len(table.Rows), len(table.Columns)))
	}
	if meta.ContentTruncated {
		parts = append(parts, fmt.Sprintf("%d %s capped at %d runes/%d lines", meta.ContentTruncatedCells, pluralize(meta.ContentTruncatedCells, "cell", "cells"), tableMaxCellRunes, tableMaxCellLines))
	}
	if len(parts) == 0 {
		return nil
	}
	label := "[table truncated: " + strings.Join(parts, "; ") + "]"
	if !table.RowsTruncated && !table.ColsTruncated {
		label = "[table content truncated: " + strings.Join(parts, "; ") + "]"
	}
	return []string{mutedStyle().Render(truncateTableLine(label, width))}
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
			if out.Rows[rowIdx][colIdx].Truncated {
				out.ContentTruncated = true
				out.ContentTruncatedCell++
			}
		}
	}
	return out
}

func prepareTableCell(cell TableCell, ctx Context) preparedTableCell {
	text, truncated := capTableCellText(normalizeTableCellText(cell.Text))
	if ctx.RenderText != nil {
		text = ctx.RenderText(text, ctx.UserNames)
	}
	lines := strings.Split(text, "\n")
	if len(lines) > tableMaxCellLines {
		lines = lines[:tableMaxCellLines]
		text = strings.Join(lines, "\n")
		truncated = true
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return preparedTableCell{Text: text, Lines: lines, Truncated: truncated}
}

func capTableCellText(text string) (string, bool) {
	if text == "" {
		return "", false
	}
	var b strings.Builder
	runes := 0
	lines := 1
	truncated := false
	for _, r := range text {
		if runes >= tableMaxCellRunes {
			truncated = true
			break
		}
		if r == '\n' {
			if lines >= tableMaxCellLines {
				truncated = true
				break
			}
			lines++
		}
		b.WriteRune(r)
		runes++
	}
	out := b.String()
	if !truncated {
		return out, false
	}
	return appendTableCellEllipsis(out, tableMaxCellRunes), true
}

func appendTableCellEllipsis(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	for tableRuneCount(text)+len(tableCellEllipsis) > maxRunes {
		text = trimLastTableRune(text)
		if text == "" {
			break
		}
	}
	if text == "" {
		if maxRunes < len(tableCellEllipsis) {
			return strings.Repeat(".", maxRunes)
		}
		return tableCellEllipsis
	}
	return text + tableCellEllipsis
}

func appendVisibleEllipsisToLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if strings.HasSuffix(ansi.Strip(line), tableCellEllipsis) {
		return line
	}
	if width <= len(tableCellEllipsis) {
		return strings.Repeat(".", width)
	}
	ellipsisWidth := lipgloss.Width(tableCellEllipsis)
	if lipgloss.Width(line) >= width-ellipsisWidth {
		return ansi.Truncate(line, width-ellipsisWidth, "") + tableCellEllipsis
	}
	return line + tableCellEllipsis
}

func tableRuneCount(text string) int {
	count := 0
	for range text {
		count++
	}
	return count
}

func trimLastTableRune(text string) string {
	if text == "" {
		return ""
	}
	last := 0
	for i := range text {
		last = i
	}
	return text[:last]
}

func measureTableColumns(table TableBlock, ctx Context) []int {
	return measurePreparedTableColumns(prepareTable(table, ctx))
}

func measurePreparedTableColumns(table preparedTable) []int {
	columnCount := preparedTableColumnCount(table)
	if columnCount == 0 {
		return nil
	}
	widths := make([]int, columnCount)
	for i := range widths {
		widths[i] = tableMinCellWidth
	}
	for _, row := range table.Rows {
		for colIdx := 0; colIdx < columnCount && colIdx < len(row); colIdx++ {
			cellWidth := preparedTableDesiredWidth(row[colIdx])
			if cellWidth > widths[colIdx] {
				widths[colIdx] = cellWidth
			}
		}
	}
	return widths
}

func renderTableCell(cell TableCell, column TableColumn, ctx Context, width int) []string {
	lines, _ := renderPreparedTableCellTracked(prepareTableCell(cell, ctx), column, ctx.WrapText, width, false)
	return lines
}

func renderPreparedTableCellTracked(cell preparedTableCell, column TableColumn, wrapText func(string, int) string, width int, forceWrap bool) ([]string, bool) {
	if width <= 0 {
		return []string{""}, cell.Truncated
	}

	logicalLines := cell.Lines
	if len(logicalLines) == 0 {
		logicalLines = []string{""}
	}

	rawLines := make([]string, 0, len(logicalLines))
	truncated := cell.Truncated
	ellipsisNeeded := cell.Truncated
	for _, line := range logicalLines {
		remaining := tableMaxCellLines - len(rawLines)
		if remaining <= 0 {
			truncated = true
			ellipsisNeeded = true
			break
		}
		var (
			physical      []string
			lineTruncated bool
		)
		if forceWrap || column.Wrapped {
			physical, lineTruncated = wrapTableLineLimited(line, wrapText, width, remaining)
		} else {
			if lipgloss.Width(line) > width {
				line = truncateTableLine(line, width)
			}
			physical = []string{line}
		}
		rawLines = append(rawLines, physical...)
		if lineTruncated {
			truncated = true
			ellipsisNeeded = true
		}
	}
	if len(rawLines) == 0 {
		rawLines = []string{""}
	}
	if truncated && ellipsisNeeded {
		rawLines[len(rawLines)-1] = appendVisibleEllipsisToLine(rawLines[len(rawLines)-1], width)
	}
	align := column.Align
	if forceWrap {
		align = TableAlignLeft
	}
	out := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		out = append(out, alignTableLine(line, align, width))
	}
	return out, truncated
}

func wrapTableLineLimited(line string, wrapText func(string, int) string, width, maxLines int) ([]string, bool) {
	if maxLines <= 0 {
		return []string{""}, true
	}
	maxVisible := width * maxLines
	if maxVisible < width {
		maxVisible = width
	}
	bounded, truncated := capTableWrapInput(line, maxVisible)
	physical := wrapTableLine(bounded, wrapText, width)
	if len(physical) > maxLines {
		physical = physical[:maxLines]
		truncated = true
	}
	return physical, truncated
}

func capTableWrapInput(line string, maxVisible int) (string, bool) {
	if maxVisible <= 0 || lipgloss.Width(line) <= maxVisible {
		return line, false
	}
	bounded := ansi.Cut(line, 0, maxVisible)
	bounded = balanceTableHyperlinks(bounded)
	bounded = balanceTableSGR(bounded)
	return bounded, true
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

func tableViewportTop(region TableRegion) string {
	innerWidth := region.ViewWidth
	status := tableViewportStatus(region)
	return dividerStyle().Render("┌" + padOrTruncateTableStatus(status, innerWidth) + "┐")
}

func tableViewportBottom(innerWidth int) string {
	return dividerStyle().Render("└" + strings.Repeat("─", innerWidth) + "┘")
}

func tableViewportRow(line string, innerWidth int) string {
	return dividerStyle().Render("│") + padRight(line, innerWidth) + dividerStyle().Render("│")
}

func tableViewportStatus(region TableRegion) string {
	hLeft := ' '
	hRight := ' '
	vUp := ' '
	vDown := ' '
	if region.XOffset > 0 {
		hLeft = '<'
	}
	if region.XOffset < region.MaxX {
		hRight = '>'
	}
	if region.YOffset > 0 {
		vUp = '^'
	}
	if region.YOffset < region.MaxY {
		vDown = 'v'
	}
	status := fmt.Sprintf("x[%c%d/%d%c] y[%c%d/%d%c]", hLeft, region.XOffset, region.MaxX, hRight, vUp, region.YOffset, region.MaxY, vDown)
	if region.ContentTruncated {
		status += fmt.Sprintf(" !%d", region.ContentTruncatedCells)
	}
	return status
}

func padOrTruncateTableStatus(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) > width {
		return truncateTableLine(s, width)
	}
	return padRight(s, width)
}

func clipTableLine(line string, xOffset, width int) string {
	if width <= 0 {
		return ""
	}
	visible := ansi.Cut(line, xOffset, xOffset+width)
	visible = balanceTableHyperlinks(visible)
	visible = balanceTableSGR(visible)
	return padRight(visible, width)
}

func balanceTableSGR(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	active := false
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && s[j] != 'm' {
			j++
		}
		if j >= len(s) {
			break
		}
		active = sgrSequenceActivates(s[i+2 : j])
		i = j
	}
	if active {
		return s + "\x1b[0m"
	}
	return s
}

func sgrSequenceActivates(params string) bool {
	if params == "" || params == "0" {
		return false
	}
	for _, part := range strings.Split(params, ";") {
		if part == "" || part == "0" {
			continue
		}
		return true
	}
	return false
}

func tableCanvasWidth(widths []int) int {
	if len(widths) == 0 {
		return 0
	}
	total := len(widths) + 1
	for _, width := range widths {
		total += width
	}
	return total
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

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// DefaultTableMaxHeight returns the default table viewport height budget
// for a pane content height.
func DefaultTableMaxHeight(paneContentHeight int) int {
	if paneContentHeight <= 0 {
		return 5
	}
	h := paneContentHeight / 2
	if h < 5 {
		h = 5
	}
	if h > 12 {
		h = 12
	}
	if h > paneContentHeight {
		h = paneContentHeight
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (ctx Context) tableViewport(key TableKey) TableViewportInput {
	if ctx.TableViewports == nil {
		return TableViewportInput{}
	}
	return ctx.TableViewports[key]
}
