package blockkit

import (
	"math/rand"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func plainLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = ansi.Strip(line)
	}
	return out
}

func assertLinesFitWidth(t *testing.T, lines []string, width int) {
	t.Helper()
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, ansi.Strip(line))
		}
	}
}

func TestMeasureTableColumnsCapsDesiredWidth(t *testing.T) {
	table := TableBlock{
		Rows: [][]TableCell{{
			{Text: strings.Repeat("x", 80)},
			{Text: "id"},
		}},
		Columns: []TableColumn{{}, {}},
	}
	if got := measureTableColumns(table, Context{}, 36); len(got) != 2 || got[0] != 30 || got[1] != 3 {
		t.Fatalf("widths = %v, want [30 3]", got)
	}
}

func TestMeasureTableColumnsWideWidthStopsAtDesiredCap(t *testing.T) {
	table := TableBlock{
		Rows:    [][]TableCell{{{Text: strings.Repeat("x", 80)}}},
		Columns: []TableColumn{{}},
	}
	if got := measureTableColumns(table, Context{}, 120); len(got) != 1 || got[0] != 30 {
		t.Fatalf("widths = %v, want [30]", got)
	}
	res := Render([]Block{table}, Context{}, 120)
	if got := lipgloss.Width(res.Lines[0]); got != 32 {
		t.Fatalf("table width = %d, want 32 (30 cols + borders)", got)
	}
}

func TestMeasureTableColumnsWideWidthKeepsNaturalWidths(t *testing.T) {
	table := TableBlock{
		Rows:    [][]TableCell{{{Text: "cat"}, {Text: "id"}}},
		Columns: []TableColumn{{}, {}},
	}
	if got := measureTableColumns(table, Context{}, 120); len(got) != 2 || got[0] != 3 || got[1] != 3 {
		t.Fatalf("widths = %v, want [3 3]", got)
	}
	res := Render([]Block{table}, Context{}, 120)
	if got := lipgloss.Width(res.Lines[0]); got != 9 {
		t.Fatalf("table width = %d, want 9 (natural width, no extra padding)", got)
	}
}

func TestRenderTableWideGrid(t *testing.T) {
	table := TableBlock{
		Rows: [][]TableCell{
			{{Text: "Service"}, {Text: "Healthy"}, {Text: "Owner"}},
			{{Text: "API"}, {Text: "Healthy"}, {Text: "Alex"}},
		},
		Columns: []TableColumn{{}, {}, {}},
	}
	got := plainLines(Render([]Block{table}, Context{}, 23).Lines)
	want := []string{
		"┌───────┬───────┬─────┐",
		"│Service│Healthy│Owner│",
		"├───────┼───────┼─────┤",
		"│API    │Healthy│Alex │",
		"└───────┴───────┴─────┘",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("wide table mismatch\nwant:\n%s\n\ngot:\n%s", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func TestRenderTableCellTruncateAlignAndMultiline(t *testing.T) {
	got := renderTableCell(TableCell{Text: "abcdef\nx"}, TableColumn{Align: TableAlignRight}, Context{}, 4)
	plain := plainLines(got)
	want := []string{"a...", "   x"}
	if strings.Join(plain, "\n") != strings.Join(want, "\n") {
		t.Fatalf("renderTableCell mismatch\nwant:\n%s\n\ngot:\n%s", strings.Join(want, "\n"), strings.Join(plain, "\n"))
	}
}

func TestRenderTableGridHandlesANSIWidth(t *testing.T) {
	ctx := Context{
		RenderText: func(s string, _ map[string]string) string {
			if strings.Contains(s, "link") {
				return "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\"
			}
			return "\x1b[31m" + s + "\x1b[0m"
		},
	}
	table := TableBlock{
		Rows:    [][]TableCell{{{Text: "red"}, {Text: "link"}}},
		Columns: []TableColumn{{}, {}},
	}
	res := Render([]Block{table}, ctx, 12)
	assertLinesFitWidth(t, res.Lines, 12)
	plain := strings.Join(plainLines(res.Lines), "\n")
	if !strings.Contains(plain, "red") || !strings.Contains(plain, "link") {
		t.Fatalf("rendered table missing ANSI-wrapped cell text: %q", plain)
	}
}

func TestRenderTableRaggedAndEmptyCells(t *testing.T) {
	table := TableBlock{
		Rows: [][]TableCell{
			{{Text: "A"}, {Text: ""}},
			{{Text: "B"}},
		},
		Columns: []TableColumn{{}, {}},
	}
	got := plainLines(Render([]Block{table}, Context{}, 9).Lines)
	want := []string{
		"┌───┬───┐",
		"│A  │   │",
		"├───┼───┤",
		"│B  │   │",
		"└───┴───┘",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ragged table mismatch\nwant:\n%s\n\ngot:\n%s", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func TestRenderTableWrapAndControlNormalization(t *testing.T) {
	table := TableBlock{
		Rows: [][]TableCell{{
			{Text: "one\ttwo\r\nthree four five"},
		}},
		Columns: []TableColumn{{Wrapped: true}},
	}
	res := Render([]Block{table}, Context{}, 8)
	assertLinesFitWidth(t, res.Lines, 8)
	plain := strings.Join(plainLines(res.Lines), "\n")
	if strings.Contains(plain, "\t") || strings.Contains(plain, "\r") {
		t.Fatalf("control chars should be normalized: %q", plain)
	}
	for _, want := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("wrapped table missing %q in %q", want, plain)
		}
	}
}

func TestRenderTableNarrowFallback(t *testing.T) {
	table := TableBlock{
		Rows: [][]TableCell{{
			{Text: "Service"},
			{Text: "Status"},
			{Text: "Owner"},
		}},
		Columns: []TableColumn{{}, {}, {}},
	}
	res := Render([]Block{table}, Context{}, 11)
	assertLinesFitWidth(t, res.Lines, 11)
	plain := strings.Join(plainLines(res.Lines), "\n")
	for _, want := range []string{"Row 1", "C1:", "C2:", "C3:", "Service", "Status", "Owner"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("narrow fallback missing %q in %q", want, plain)
		}
	}
	if strings.Contains(plain, "┌") {
		t.Fatalf("narrow fallback should not render grid borders: %q", plain)
	}
}

func TestRenderTableNarrowFallbackWrapsNonWrappedContentFully(t *testing.T) {
	table := TableBlock{
		Rows: [][]TableCell{{
			{Text: "one two six ten"},
		}},
		Columns: []TableColumn{{Wrapped: false}, {}},
	}
	res := Render([]Block{table}, Context{}, 8)
	assertLinesFitWidth(t, res.Lines, 8)
	plain := strings.Join(plainLines(res.Lines), "\n")
	for _, want := range []string{"one", "two", "six", "ten"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("stacked fallback lost %q in %q", want, plain)
		}
	}
	if strings.Contains(plain, "...") {
		t.Fatalf("stacked fallback should wrap, not ellipsize: %q", plain)
	}
}

func TestRenderTableNarrowFallbackHandlesNilAndZeroCellRows(t *testing.T) {
	table := TableBlock{
		Rows: [][]TableCell{
			nil,
			{},
			{{Text: "A"}, {Text: ""}},
		},
		Columns: []TableColumn{{}, {}},
	}
	res := Render([]Block{table}, Context{}, 6)
	assertLinesFitWidth(t, res.Lines, 6)
	got := plainLines(res.Lines)
	if strings.Join(got[:3], "\n") != strings.Join([]string{"Row 1", "Row 2", "Row 3"}, "\n") {
		t.Fatalf("first rows = %q, want row labels for nil/empty rows", got[:3])
	}
	if !strings.Contains(strings.Join(got, "\n"), "C1: A") {
		t.Fatalf("stacked fallback should preserve populated later row: %q", got)
	}
	if !strings.Contains(strings.Join(got, "\n"), "C2:") {
		t.Fatalf("stacked fallback should preserve explicit blank cell label: %q", got)
	}
}

func TestRenderTableSummary(t *testing.T) {
	table := TableBlock{
		Rows:          [][]TableCell{{{Text: "A"}}},
		Columns:       []TableColumn{{}},
		RowsTruncated: true,
		ColsTruncated: true,
		SourceRows:    101,
		SourceCols:    21,
	}
	got := plainLines(Render([]Block{table}, Context{}, 60).Lines)
	if got[len(got)-1] != "[table truncated: showing 1 rows x 1 columns]" {
		t.Fatalf("summary = %q", got[len(got)-1])
	}
}

func TestRenderTableCallsRenderTextOncePerVisibleCell(t *testing.T) {
	count := 0
	ctx := Context{
		RenderText: func(s string, _ map[string]string) string {
			count++
			return s
		},
	}
	table := TableBlock{Columns: make([]TableColumn, tableMaxCols)}
	for row := 0; row < tableMaxRows; row++ {
		cells := make([]TableCell, tableMaxCols)
		for col := range cells {
			cells[col] = TableCell{Text: "cell"}
		}
		table.Rows = append(table.Rows, cells)
	}
	res := Render([]Block{table}, ctx, 120)
	assertLinesFitWidth(t, res.Lines, 120)
	if want := tableMaxRows * tableMaxCols; count != want {
		t.Fatalf("RenderText calls = %d, want %d (once per visible cell)", count, want)
	}
}

func TestRenderTableWrappedOSC8LinksBalancePerPhysicalLine(t *testing.T) {
	const open = "\x1b]8;;https://example.com/docs\x1b\\"
	const close = "\x1b]8;;\x1b\\"
	ctx := Context{
		WrapText: func(s string, width int) string { return ansi.Wrap(s, width, "") },
	}
	cell := preparedTableCell{Text: open + "multi word label" + close, Lines: []string{open + "multi word label" + close}}
	lines := renderPreparedTableCell(cell, TableColumn{Wrapped: true}, ctx.WrapText, 8, false)
	plainJoined := strings.Join(plainLines(lines), " ")
	if !strings.Contains(plainJoined, "multi") || !strings.Contains(plainJoined, "word") || !strings.Contains(plainJoined, "label") {
		t.Fatalf("wrapped hyperlink label lost text: %q", plainJoined)
	}
	for i, line := range lines {
		if got := strings.Count(line, open); got != 1 {
			t.Fatalf("line %d open count = %d, want 1: %q", i, got, line)
		}
		if got := strings.Count(line, close); got != 1 {
			t.Fatalf("line %d close count = %d, want 1: %q", i, got, line)
		}
	}
}

func TestRenderTableWidthInvariantRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	ctx := Context{
		RenderText: func(s string, _ map[string]string) string {
			if strings.Contains(s, "link") {
				return "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\"
			}
			return "\x1b[32m" + s + "\x1b[0m"
		},
	}
	parts := []string{"alpha", "beta", "gamma", "delta", "wide界", "line1\nline2", "link", "tab\tcell", "carriage\rreturn", ""}
	for width := 1; width <= 200; width++ {
		for iter := 0; iter < 20; iter++ {
			cols := rng.Intn(5) + 1
			rows := rng.Intn(6) + 1
			table := TableBlock{Columns: make([]TableColumn, cols)}
			for col := range table.Columns {
				table.Columns[col] = TableColumn{
					Align:   TableAlignment(rng.Intn(3)),
					Wrapped: rng.Intn(2) == 0,
				}
			}
			for row := 0; row < rows; row++ {
				cellCount := rng.Intn(cols + 1)
				cells := make([]TableCell, cellCount)
				for col := 0; col < cellCount; col++ {
					cells[col] = TableCell{Text: parts[rng.Intn(len(parts))]}
				}
				table.Rows = append(table.Rows, cells)
			}
			res := Render([]Block{table}, ctx, width)
			assertLinesFitWidth(t, res.Lines, width)
		}
	}
}
