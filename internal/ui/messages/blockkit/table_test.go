package blockkit

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const testTableMessageTS = "1700000000.000100"

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

func testTableCtx() Context {
	return Context{
		MessageTS: testTableMessageTS,
		RenderText: func(s string, _ map[string]string) string {
			return s
		},
		WrapText: func(s string, width int) string {
			return ansi.Wrap(s, width, "")
		},
	}
}

func testTableKey(blockID, path string) TableKey {
	return TableKey{MessageTS: testTableMessageTS, Path: path, BlockID: blockID}
}

func TestMeasureTableColumnsNaturalCanvas(t *testing.T) {
	table := TableBlock{
		Rows: [][]TableCell{{
			{Text: strings.Repeat("x", 80)},
			{Text: "id"},
		}},
		Columns: []TableColumn{{}, {}},
	}
	got := measureTableColumns(table, Context{})
	if len(got) != 2 || got[0] != 30 || got[1] != 3 {
		t.Fatalf("widths = %v, want [30 3]", got)
	}
}

func TestDefaultTableMaxHeight(t *testing.T) {
	for _, tc := range []struct {
		height int
		want   int
	}{
		{height: 0, want: 5},
		{height: 3, want: 3},
		{height: 8, want: 5},
		{height: 24, want: 12},
	} {
		if got := DefaultTableMaxHeight(tc.height); got != tc.want {
			t.Fatalf("height=%d got %d want %d", tc.height, got, tc.want)
		}
	}
}

func TestRenderTableUsesContextDefaultMaxHeight(t *testing.T) {
	ctx := testTableCtx()
	ctx.TableMaxHeight = 6
	table := TableBlock{BlockID: "default-cap", Columns: []TableColumn{{}}}
	for i := 0; i < 6; i++ {
		table.Rows = append(table.Rows, []TableCell{{Text: fmt.Sprintf("R%d", i)}})
	}
	res := Render([]Block{table}, ctx, 20)
	if len(res.TableRegions) != 1 {
		t.Fatalf("table regions = %d, want 1", len(res.TableRegions))
	}
	region := res.TableRegions[0]
	if region.ViewHeight != 4 || region.MaxY != 9 || len(res.Lines) != 6 {
		t.Fatalf("region=%+v lines=%d", region, len(res.Lines))
	}
}

func TestRenderTableWideGridAndRegion(t *testing.T) {
	ctx := testTableCtx()
	table := TableBlock{
		BlockID: "tbl",
		Rows: [][]TableCell{
			{{Text: "Service"}, {Text: "Healthy"}, {Text: "Owner"}},
			{{Text: "API"}, {Text: "Healthy"}, {Text: "Alex"}},
		},
		Columns: []TableColumn{{}, {}, {}},
	}
	res := Render([]Block{table}, ctx, 23)
	got := plainLines(res.Lines)
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
	if len(res.TableRegions) != 1 {
		t.Fatalf("table regions = %d, want 1", len(res.TableRegions))
	}
	region := res.TableRegions[0]
	if region.Key != testTableKey("tbl", "blocks/0") {
		t.Fatalf("key = %+v", region.Key)
	}
	if region.LineStart != 0 || region.LineEnd != len(res.Lines) {
		t.Fatalf("line range = %d:%d, want 0:%d", region.LineStart, region.LineEnd, len(res.Lines))
	}
	if region.ViewWidth != 23 || region.ViewHeight != 5 || region.FullWidth != 23 || region.FullHeight != 5 || region.MaxX != 0 || region.MaxY != 0 || region.XOffset != 0 || region.YOffset != 0 {
		t.Fatalf("region = %+v", region)
	}
	assertLinesFitWidth(t, res.Lines, 23)
}

func TestRenderTableViewportHorizontalOffsetRevealsHiddenColumns(t *testing.T) {
	ctx := testTableCtx()
	table := TableBlock{
		BlockID: "wide",
		Rows: [][]TableCell{{
			{Text: "Alpha"},
			{Text: "Bravo"},
			{Text: "Charlie"},
			{Text: "Delta"},
		}},
		Columns: []TableColumn{{}, {}, {}, {}},
	}
	res0 := Render([]Block{table}, ctx, 20)
	plain0 := strings.Join(plainLines(res0.Lines), "\n")
	if len(res0.TableRegions) != 1 {
		t.Fatalf("table regions = %d, want 1", len(res0.TableRegions))
	}
	region0 := res0.TableRegions[0]
	if region0.ViewWidth != 18 || region0.ViewHeight != 3 || region0.FullWidth != 27 || region0.FullHeight != 3 || region0.MaxX != 9 || region0.MaxY != 0 || region0.XOffset != 0 || region0.YOffset != 0 {
		t.Fatalf("unexpected left viewport region: %+v", region0)
	}
	if !strings.Contains(plain0, "Alpha") {
		t.Fatalf("left viewport missing first column: %q", plain0)
	}
	if strings.Contains(plain0, "Delta") {
		t.Fatalf("left viewport unexpectedly shows last column: %q", plain0)
	}
	if got := plainLines(res0.Lines)[0]; !strings.Contains(got, "x[ 0/9>] y[ 0/0 ]") {
		t.Fatalf("left viewport status = %q", got)
	}

	ctx.TableViewports = map[TableKey]TableViewportInput{
		testTableKey("wide", "blocks/0"): {XOffset: region0.MaxX},
	}
	res1 := Render([]Block{table}, ctx, 20)
	plain1 := strings.Join(plainLines(res1.Lines), "\n")
	region1 := res1.TableRegions[0]
	if region1.ViewWidth != 18 || region1.ViewHeight != 3 || region1.FullWidth != 27 || region1.FullHeight != 3 || region1.MaxX != 9 || region1.MaxY != 0 || region1.XOffset != 9 || region1.YOffset != 0 {
		t.Fatalf("unexpected right viewport region: %+v", region1)
	}
	if !strings.Contains(plain1, "Delta") {
		t.Fatalf("right viewport missing last column: %q", plain1)
	}
	if got := plainLines(res1.Lines)[0]; !strings.Contains(got, "x[<9/9 ] y[ 0/0 ]") {
		t.Fatalf("right viewport status = %q", got)
	}
	if len(res0.Lines) != len(res1.Lines) {
		t.Fatalf("viewport height changed with x offset: %d vs %d", len(res0.Lines), len(res1.Lines))
	}
	assertLinesFitWidth(t, res1.Lines, 20)
}

func TestRenderTableViewportVerticalOffsetStableHeight(t *testing.T) {
	ctx := testTableCtx()
	table := TableBlock{BlockID: "tall", Columns: []TableColumn{{}}}
	for i := 1; i <= 6; i++ {
		table.Rows = append(table.Rows, []TableCell{{Text: fmt.Sprintf("R%d", i)}})
	}
	key := testTableKey("tall", "blocks/0")
	ctx.TableViewports = map[TableKey]TableViewportInput{key: {MaxHeight: 7}}
	res0 := Render([]Block{table}, ctx, 20)
	region0 := res0.TableRegions[0]
	if region0.ViewWidth != 18 || region0.ViewHeight != 5 || region0.FullWidth != 5 || region0.FullHeight != 13 || region0.MaxX != 0 || region0.MaxY != 8 || region0.XOffset != 0 || region0.YOffset != 0 {
		t.Fatalf("unexpected top viewport region: %+v", region0)
	}
	if got := plainLines(res0.Lines)[0]; !strings.Contains(got, "x[ 0/0 ] y[ 0/8v]") {
		t.Fatalf("top viewport status = %q", got)
	}
	ctx.TableViewports[key] = TableViewportInput{MaxHeight: 7, YOffset: region0.MaxY}
	res1 := Render([]Block{table}, ctx, 20)
	plain1 := strings.Join(plainLines(res1.Lines), "\n")
	region1 := res1.TableRegions[0]
	if region1.ViewWidth != 18 || region1.ViewHeight != 5 || region1.FullWidth != 5 || region1.FullHeight != 13 || region1.MaxX != 0 || region1.MaxY != 8 || region1.XOffset != 0 || region1.YOffset != 8 {
		t.Fatalf("unexpected bottom viewport region: %+v", region1)
	}
	if got := plainLines(res1.Lines)[0]; !strings.Contains(got, "x[ 0/0 ] y[^8/8 ]") {
		t.Fatalf("bottom viewport status = %q", got)
	}
	if len(res0.Lines) != len(res1.Lines) {
		t.Fatalf("viewport height changed with y offset: %d vs %d", len(res0.Lines), len(res1.Lines))
	}
	if len(res1.Lines) != 7 {
		t.Fatalf("viewport height = %d, want 7", len(res1.Lines))
	}
	if !strings.Contains(plain1, "R6") {
		t.Fatalf("bottom viewport missing later row: %q", plain1)
	}
	assertLinesFitWidth(t, res1.Lines, 20)
}

func TestRenderTableTinyWidthFallback(t *testing.T) {
	ctx := testTableCtx()
	table := TableBlock{
		BlockID: "tiny",
		Rows:    [][]TableCell{{{Text: "Service"}, {Text: "Status"}}},
		Columns: []TableColumn{{}, {}},
	}
	res := Render([]Block{table}, ctx, 3)
	plain := strings.Join(plainLines(res.Lines), "\n")
	if strings.Contains(plain, "┌") {
		t.Fatalf("tiny fallback should not render grid border: %q", plain)
	}
	if len(res.TableRegions) != 1 {
		t.Fatalf("table regions = %d, want 1", len(res.TableRegions))
	}
	assertLinesFitWidth(t, res.Lines, 3)
}

func TestRenderTableClampOffsetsAndBounds(t *testing.T) {
	ctx := testTableCtx()
	table := TableBlock{BlockID: "clamp", Columns: []TableColumn{{}, {}, {Wrapped: true}}}
	for i := 0; i < 5; i++ {
		table.Rows = append(table.Rows, []TableCell{{Text: "Alpha"}, {Text: "Bravo"}, {Text: strings.Repeat("cell ", 6)}})
	}
	key := testTableKey("clamp", "blocks/0")
	ctx.TableViewports = map[TableKey]TableViewportInput{key: {XOffset: -10, YOffset: -10, MaxHeight: 6}}
	res0 := Render([]Block{table}, ctx, 24)
	region0 := res0.TableRegions[0]
	if region0.ViewWidth != 22 || region0.ViewHeight != 4 || region0.FullWidth != 44 || region0.FullHeight != 11 || region0.MaxX != 22 || region0.MaxY != 7 || region0.XOffset != 0 || region0.YOffset != 0 {
		t.Fatalf("negative offsets not clamped to zero: %+v", region0)
	}
	if got := plainLines(res0.Lines)[0]; !strings.Contains(got, "x[ 0/22>] y[ 0/7v]") {
		t.Fatalf("negative-offset status = %q", got)
	}
	ctx.TableViewports[key] = TableViewportInput{XOffset: 999, YOffset: 999, MaxHeight: 6}
	res1 := Render([]Block{table}, ctx, 24)
	region1 := res1.TableRegions[0]
	if region1.ViewWidth != 22 || region1.ViewHeight != 4 || region1.FullWidth != 44 || region1.FullHeight != 11 || region1.MaxX != 22 || region1.MaxY != 7 || region1.XOffset != 22 || region1.YOffset != 7 {
		t.Fatalf("large offsets not clamped to max: %+v", region1)
	}
	if got := plainLines(res1.Lines)[0]; !strings.Contains(got, "x[<22/22 ] y[^7/7 ]") {
		t.Fatalf("max-offset status = %q", got)
	}
	assertLinesFitWidth(t, res1.Lines, 24)
}

func TestRenderTableUnicodeViewportClipping(t *testing.T) {
	ctx := testTableCtx()
	table := TableBlock{
		BlockID: "unicode",
		Rows:    [][]TableCell{{{Text: "界界界界界"}}},
		Columns: []TableColumn{{}},
	}
	ctx.TableViewports = map[TableKey]TableViewportInput{
		testTableKey("unicode", "blocks/0"): {XOffset: 2},
	}
	res := Render([]Block{table}, ctx, 8)
	plain := strings.Join(plainLines(res.Lines), "\n")
	if strings.Contains(plain, "�") {
		t.Fatalf("unicode clipping introduced replacement rune: %q", plain)
	}
	if !strings.Contains(plain, "界") {
		t.Fatalf("unicode viewport lost visible glyphs: %q", plain)
	}
	assertLinesFitWidth(t, res.Lines, 8)
}

func TestRenderTableANSIAndOSC8Clipping(t *testing.T) {
	ctx := testTableCtx()
	ctx.RenderText = func(s string, _ map[string]string) string {
		if s == "link" {
			return tableOSC8Open("https://example.com") + "ABCDEFGHIJKL" + tableOSC8Close()
		}
		return "\x1b[31m" + s + "\x1b[0m"
	}
	table := TableBlock{
		BlockID: "ansi",
		Rows:    [][]TableCell{{{Text: "link"}}},
		Columns: []TableColumn{{}},
	}
	ctx.TableViewports = map[TableKey]TableViewportInput{
		testTableKey("ansi", "blocks/0"): {XOffset: 3},
	}
	res := Render([]Block{table}, ctx, 12)
	assertLinesFitWidth(t, res.Lines, 12)
	found := false
	for _, line := range res.Lines {
		plain := ansi.Strip(line)
		if !strings.Contains(plain, "DEFG") {
			continue
		}
		found = true
		if got := strings.Count(line, osc8Prefix); got != 2 {
			t.Fatalf("hyperlink line open+close count = %d, want 2: %q", got, line)
		}
		if got := strings.Count(line, tableOSC8Close()); got != 1 {
			t.Fatalf("hyperlink line close count = %d, want 1: %q", got, line)
		}
		break
	}
	if !found {
		t.Fatalf("viewport clipping lost expected hyperlink text: %q", strings.Join(plainLines(res.Lines), "\n"))
	}
}

func TestClipTableLineBalancesSGRAtViewportBoundary(t *testing.T) {
	cut := clipTableLine("\x1b[31mABCDEFGHIJKL\x1b[0m", 3, 4)
	if got := ansi.Strip(cut); got != "DEFG" {
		t.Fatalf("plain cut = %q, want %q", got, "DEFG")
	}
	if !strings.Contains(cut, "\x1b[0m") {
		t.Fatalf("cut missing reset: %q", cut)
	}
	if got := cut + "Z"; !strings.Contains(got, "\x1b[0mZ") {
		t.Fatalf("style reset must happen before following text: %q", got)
	}
}

func TestRenderTableRuneCapShowsVisibleEllipsisAndSummary(t *testing.T) {
	ctx := testTableCtx()
	table := TableBlock{
		BlockID: "rune-cap",
		Rows: [][]TableCell{{
			{Text: strings.Repeat("界", tableMaxCellRunes+5)},
		}},
		Columns: []TableColumn{{Wrapped: true}},
	}
	res := Render([]Block{table}, ctx, 80)
	plain := strings.Join(plainLines(res.Lines), "\n")
	region := res.TableRegions[0]
	if !region.ContentTruncated || region.ContentTruncatedCells != 1 {
		t.Fatalf("region = %+v, want 1 capped cell", region)
	}
	if !strings.Contains(plain, "...") {
		t.Fatalf("missing visible ellipsis in capped rune cell: %q", plain)
	}
	if !strings.Contains(plain, "1 cell capped") {
		t.Fatalf("missing capped summary in %q", plain)
	}
	assertLinesFitWidth(t, res.Lines, 80)
}

func TestRenderTablePhysicalLineCapWrappedCanvas(t *testing.T) {
	ctx := testTableCtx()
	table := TableBlock{BlockID: "phys-cap", Rows: [][]TableCell{{{Text: strings.Repeat("x", tableMaxCellRunes)}}}, Columns: []TableColumn{{Wrapped: true}}}
	res := Render([]Block{table}, ctx, 40)
	region := res.TableRegions[0]
	plain := plainLines(res.Lines)
	if region.FullHeight != 202 || len(res.Lines) != 203 {
		t.Fatalf("wrapped canvas height wrong: region=%+v lines=%d", region, len(res.Lines))
	}
	if !region.ContentTruncated || region.ContentTruncatedCells != 1 {
		t.Fatalf("region = %+v, want one truncated cell", region)
	}
	if !strings.Contains(plain[len(plain)-3], "...") {
		t.Fatalf("last visible cell line should end with ellipsis: %q", plain[len(plain)-3])
	}
	if !strings.Contains(plain[len(plain)-1], "1 cell") {
		t.Fatalf("summary missing single capped cell: %q", plain[len(plain)-1])
	}
}

func TestRenderTablePhysicalLineCapTinyFallback(t *testing.T) {
	ctx := testTableCtx()
	table := TableBlock{BlockID: "phys-cap-tiny", Rows: [][]TableCell{{{Text: strings.Repeat("x", tableMaxCellRunes)}}}, Columns: []TableColumn{{Wrapped: true}}}
	res := Render([]Block{table}, ctx, 3)
	region := res.TableRegions[0]
	plain := plainLines(res.Lines)
	if len(res.Lines) != 203 {
		t.Fatalf("tiny fallback total lines = %d, want 203", len(res.Lines))
	}
	if !region.ContentTruncated || region.ContentTruncatedCells != 1 {
		t.Fatalf("region = %+v, want one truncated cell", region)
	}
	if !strings.HasSuffix(plain[len(plain)-2], "...") {
		t.Fatalf("tiny fallback last content line should end with ellipsis: %q", plain[len(plain)-2])
	}
	if plain[len(plain)-1] != "..." {
		t.Fatalf("tiny fallback summary should truncate to dots at width 3: %q", plain[len(plain)-1])
	}
}

func TestRenderPreparedTableCellTrackedBoundsWrapInputBeforeWrapping(t *testing.T) {
	cell := prepareTableCell(TableCell{Text: strings.Repeat("x", tableMaxCellRunes)}, Context{})
	var gotWidth, gotRunes int
	spyWrap := func(s string, width int) string {
		gotWidth = lipgloss.Width(s)
		gotRunes = tableRuneCount(s)
		return ansi.Wrap(s, width, "")
	}
	lines, truncated := renderPreparedTableCellTracked(cell, TableColumn{Wrapped: true}, spyWrap, 3, false)
	if !truncated {
		t.Fatal("expected tracked truncation")
	}
	if gotWidth > 600 || gotRunes > 600 {
		t.Fatalf("wrap input too large: width=%d runes=%d", gotWidth, gotRunes)
	}
	if len(lines) != tableMaxCellLines {
		t.Fatalf("wrapped lines = %d, want %d", len(lines), tableMaxCellLines)
	}
	if lines[len(lines)-1] != "..." {
		t.Fatalf("last wrapped line = %q, want ellipsis", lines[len(lines)-1])
	}
	lines, truncated = renderPreparedTableCellTracked(cell, TableColumn{Wrapped: true}, nil, 1, false)
	if !truncated {
		t.Fatal("expected truncation with nil WrapText fallback")
	}
	if len(lines) != tableMaxCellLines {
		t.Fatalf("nil WrapText lines = %d, want %d", len(lines), tableMaxCellLines)
	}
	if lines[len(lines)-1] != "." {
		t.Fatalf("nil WrapText last line = %q, want dot ellipsis", lines[len(lines)-1])
	}
}

func TestRenderTableLineCapShowsVisibleEllipsisAndSummary(t *testing.T) {
	ctx := testTableCtx()
	var lineBuilder strings.Builder
	for i := 0; i < tableMaxCellLines+5; i++ {
		fmt.Fprintf(&lineBuilder, "L%03d\n", i)
	}
	table := TableBlock{
		BlockID: "line-cap",
		Rows: [][]TableCell{{
			{Text: lineBuilder.String()},
		}},
		Columns: []TableColumn{{Wrapped: true}},
	}
	res := Render([]Block{table}, ctx, 80)
	plain := strings.Join(plainLines(res.Lines), "\n")
	region := res.TableRegions[0]
	if !region.ContentTruncated || region.ContentTruncatedCells != 1 {
		t.Fatalf("region = %+v, want 1 capped cell", region)
	}
	if !strings.Contains(plain, "L199...") {
		t.Fatalf("line-capped cell missing visible ellipsis: %q", plain)
	}
	if !strings.Contains(plain, "1 cell capped") {
		t.Fatalf("missing capped summary in %q", plain)
	}
	if strings.Contains(plain, fmt.Sprintf("L%03d", tableMaxCellLines)) {
		t.Fatalf("line cap leaked later logical lines: %q", plain)
	}
	assertLinesFitWidth(t, res.Lines, 80)
}

func TestRenderMultipleTableIdentities(t *testing.T) {
	ctx := testTableCtx()
	tables := []Block{
		TableBlock{BlockID: "dup", Rows: [][]TableCell{{{Text: "A"}}}, Columns: []TableColumn{{}}},
		TableBlock{BlockID: "dup", Rows: [][]TableCell{{{Text: "B"}}}, Columns: []TableColumn{{}}},
	}
	res := Render(tables, ctx, 8)
	if len(res.TableRegions) != 2 {
		t.Fatalf("table regions = %d, want 2", len(res.TableRegions))
	}
	if res.TableRegions[0].Key != testTableKey("dup", "blocks/0") {
		t.Fatalf("first key = %+v", res.TableRegions[0].Key)
	}
	if res.TableRegions[1].Key != testTableKey("dup", "blocks/1") {
		t.Fatalf("second key = %+v", res.TableRegions[1].Key)
	}
	if res.TableRegions[0].LineEnd > res.TableRegions[1].LineStart {
		t.Fatalf("regions overlap: %+v", res.TableRegions)
	}
}

func TestRenderTableWidthInvariantRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	ctx := testTableCtx()
	ctx.RenderText = func(s string, _ map[string]string) string {
		if strings.Contains(s, "link") {
			return tableOSC8Open("https://example.com") + "linklabel" + tableOSC8Close()
		}
		return "\x1b[32m" + s + "\x1b[0m"
	}
	parts := []string{"alpha", "beta", "gamma", "delta", "wide界", "line1\nline2", "link", "tab\tcell", "carriage\rreturn", ""}
	for width := 1; width <= 120; width++ {
		for iter := 0; iter < 12; iter++ {
			cols := rng.Intn(5) + 1
			rows := rng.Intn(6) + 1
			table := TableBlock{BlockID: fmt.Sprintf("rnd-%d-%d", width, iter), Columns: make([]TableColumn, cols)}
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
