package messages

import (
	"fmt"
	"testing"

	"github.com/gammons/slk/internal/ui/messages/blockkit"
)

func testTableBlock(id string, cols int, rows ...[]string) blockkit.TableBlock {
	table := blockkit.TableBlock{BlockID: id, Columns: make([]blockkit.TableColumn, cols)}
	for _, row := range rows {
		cells := make([]blockkit.TableCell, len(row))
		for i, text := range row {
			cells[i] = blockkit.TableCell{Text: text}
		}
		table.Rows = append(table.Rows, cells)
	}
	return table
}

func TestTableModeActivateSelectedThenNearestVisible(t *testing.T) {
	m := New([]MessageItem{
		{TS: "1.0", Text: "plain"},
		{TS: "2.0", Text: "table", Blocks: []blockkit.Block{testTableBlock("sel", 1, []string{"A"})}},
	}, "general")
	m.View(20, 40)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should focus selected message table")
	}
	region, ok := m.FocusedTableRegion()
	if !ok || region.Key.MessageTS != "2.0" || region.Key.Path != "blocks/0" {
		t.Fatalf("selected table region = %+v ok=%v", region, ok)
	}
	m.DeactivateTableMode()
	m.SelectByIndex(0)
	m.View(20, 40)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should pick nearest visible table")
	}
	region, ok = m.FocusedTableRegion()
	if !ok || region.Key.MessageTS != "2.0" {
		t.Fatalf("nearest visible table region = %+v ok=%v", region, ok)
	}
}

func TestTableModePreservesOffsetsAndTargetsSourceMessage(t *testing.T) {
	tableA := testTableBlock("a", 3, []string{"Alpha", "Bravo", "Charlie"}, []string{"one", "two", "three"})
	tableB := testTableBlock("b", 3, []string{"Delta", "Echo", "Foxtrot"}, []string{"four", "five", "six"})
	m := New([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{tableA, tableB}}}, "general")
	m.View(14, 18)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should succeed")
	}
	h0 := m.cache[0].height
	regionA, ok := m.FocusedTableRegion()
	if !ok || regionA.Key.Path != "blocks/0" || regionA.MaxX == 0 {
		t.Fatalf("focused table A = %+v ok=%v", regionA, ok)
	}
	if !m.ScrollFocusedTable(regionA.MaxX, 0) {
		t.Fatal("ScrollFocusedTable should stay active")
	}
	if len(m.staleEntries) != 1 {
		t.Fatalf("stale entries = %v, want one source ts", m.staleEntries)
	}
	if _, ok := m.staleEntries["1.0"]; !ok {
		t.Fatalf("stale entries missing source ts: %v", m.staleEntries)
	}
	m.View(14, 18)
	regionA, _ = m.FocusedTableRegion()
	if regionA.XOffset != regionA.MaxX {
		t.Fatalf("table A offset = %d, want %d", regionA.XOffset, regionA.MaxX)
	}
	if m.cache[0].height != h0 {
		t.Fatalf("entry height changed after table scroll: %d -> %d", h0, m.cache[0].height)
	}
	if !m.FocusNextTable() {
		t.Fatal("FocusNextTable should succeed")
	}
	regionB, _ := m.FocusedTableRegion()
	if regionB.Key.Path != "blocks/1" {
		t.Fatalf("focused table B path = %q", regionB.Key.Path)
	}
	if !m.ScrollFocusedTable(2, 0) {
		t.Fatal("ScrollFocusedTable on table B should stay active")
	}
	m.View(14, 18)
	if !m.FocusPrevTable() {
		t.Fatal("FocusPrevTable should succeed")
	}
	regionA, _ = m.FocusedTableRegion()
	if regionA.Key.Path != "blocks/0" || regionA.XOffset != regionA.MaxX {
		t.Fatalf("table A state lost after tab cycle: %+v", regionA)
	}
	if !m.FocusNextTable() {
		t.Fatal("FocusNextTable should succeed")
	}
	regionB, _ = m.FocusedTableRegion()
	if regionB.XOffset != 2 {
		t.Fatalf("table B offset = %d, want 2", regionB.XOffset)
	}
}

func TestTableModeResizeClampAndInactiveSource(t *testing.T) {
	var rows [][]string
	for i := 0; i < 12; i++ {
		rows = append(rows, []string{fmt.Sprintf("R%02d", i)})
	}
	table := blockkit.TableBlock{BlockID: "tall", Columns: []blockkit.TableColumn{{}}}
	for _, row := range rows {
		table.Rows = append(table.Rows, []blockkit.TableCell{{Text: row[0]}})
	}
	m := New([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{table}}}, "general")
	m.View(10, 20)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should succeed")
	}
	region, _ := m.FocusedTableRegion()
	if !m.ScrollFocusedTable(0, region.MaxY) {
		t.Fatal("ScrollFocusedTable should stay active")
	}
	m.View(10, 20)
	region, _ = m.FocusedTableRegion()
	oldOffset := region.YOffset
	m.View(30, 20)
	region, _ = m.FocusedTableRegion()
	if region.YOffset != region.MaxY || region.YOffset > oldOffset {
		t.Fatalf("resize clamp failed: old=%d region=%+v", oldOffset, region)
	}
	m.RemoveMessageByTS("1.0")
	if m.TableModeActive() {
		t.Fatal("table mode should report inactive once source message is removed")
	}
}

func TestTableModeDeactivatesOnSourceDrift(t *testing.T) {
	m := New([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{testTableBlock("t", 1, []string{"A"})}}}, "general")
	m.View(12, 20)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should succeed")
	}
	m.AppendMessage(MessageItem{TS: "2.0", Text: "newest"})
	if m.TableModeActive() || !tableKeyZero(m.focusedTable) {
		t.Fatalf("append should deactivate table mode, focused=%+v", m.focusedTable)
	}
	m = New([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{testTableBlock("t", 1, []string{"A"})}}}, "general")
	m.View(12, 20)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should succeed")
	}
	m.SetMessages([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{testTableBlock("t", 1, []string{"A"})}}, {TS: "2.0", Text: "later"}})
	if m.TableModeActive() || !tableKeyZero(m.focusedTable) {
		t.Fatalf("refresh selecting different message should deactivate table mode, focused=%+v", m.focusedTable)
	}
}

func TestFocusNextTableStaysWithinFocusedMessage(t *testing.T) {
	m := New([]MessageItem{
		{TS: "1.0", Blocks: []blockkit.Block{testTableBlock("a0", 1, []string{"A0"}), testTableBlock("a1", 1, []string{"A1"})}},
		{TS: "2.0", Blocks: []blockkit.Block{testTableBlock("b0", 1, []string{"B0"}), testTableBlock("b1", 1, []string{"B1"})}},
	}, "general")
	m.SelectByIndex(1)
	m.View(20, 30)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should succeed")
	}
	region, _ := m.FocusedTableRegion()
	if region.Key.MessageTS != "2.0" || region.Key.Path != "blocks/0" {
		t.Fatalf("initial focused region = %+v", region)
	}
	if !m.FocusNextTable() {
		t.Fatal("FocusNextTable should succeed")
	}
	region, _ = m.FocusedTableRegion()
	if region.Key.MessageTS != "2.0" || region.Key.Path != "blocks/1" {
		t.Fatalf("cycle crossed message boundary: %+v", region)
	}
	if !m.FocusNextTable() {
		t.Fatal("FocusNextTable should wrap within message")
	}
	region, _ = m.FocusedTableRegion()
	if region.Key.MessageTS != "2.0" || region.Key.Path != "blocks/0" {
		t.Fatalf("cycle wrap wrong: %+v", region)
	}
}

func TestTableBlockIDRemapAndPrune(t *testing.T) {
	m := New([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{
		testTableBlock("left", 3, []string{"A", "B", "C"}),
		testTableBlock("right", 1, []string{"D"}),
	}}}, "general")
	m.View(12, 12)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should succeed")
	}
	if !m.ScrollFocusedTable(2, 0) {
		t.Fatal("ScrollFocusedTable should succeed")
	}
	m.View(12, 12)
	m.SetMessages([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{
		testTableBlock("right", 1, []string{"D"}),
		testTableBlock("left", 3, []string{"A", "B", "C"}),
	}}})
	m.View(12, 12)
	region, ok := m.FocusedTableRegion()
	if !ok || region.Key.Path != "blocks/1" || region.XOffset != 2 {
		t.Fatalf("unique blockID remap failed: %+v ok=%v", region, ok)
	}
	if _, ok := m.tableViewports[blockkit.TableKey{MessageTS: "1.0", Path: "blocks/0", BlockID: "left"}]; ok {
		t.Fatal("old path state should be pruned after remap")
	}
	if _, ok := m.tableViewports[blockkit.TableKey{MessageTS: "1.0", Path: "blocks/1", BlockID: "left"}]; !ok {
		t.Fatal("new path state missing after remap")
	}
	m.pruneTableStateForRemovedMessage("1.0")
	if len(m.tableViewports) != 0 || !tableKeyZero(m.focusedTable) {
		t.Fatalf("removed message should prune state, map=%v focused=%+v", m.tableViewports, m.focusedTable)
	}
}

func TestTableBlockIDAmbiguousOrEmptyDoesNotRemap(t *testing.T) {
	m := New([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{
		testTableBlock("dup", 1, []string{"A"}),
		testTableBlock("dup", 1, []string{"B"}),
	}}}, "general")
	m.View(12, 20)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should succeed")
	}
	m.SetMessages([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{
		testTableBlock("dup", 1, []string{"B"}),
		testTableBlock("dup", 1, []string{"A"}),
	}}})
	m.View(12, 20)
	if m.TableModeActive() || !tableKeyZero(m.focusedTable) {
		t.Fatalf("duplicate block IDs should clear focus, focused=%+v", m.focusedTable)
	}

	m = New([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{
		testTableBlock("", 1, []string{"A"}),
		testTableBlock("", 1, []string{"B"}),
	}}}, "general")
	m.View(12, 20)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should succeed")
	}
	m.SetMessages([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{
		testTableBlock("", 1, []string{"B"}),
		testTableBlock("", 1, []string{"A"}),
	}}})
	m.View(12, 20)
	if m.TableModeActive() || !tableKeyZero(m.focusedTable) {
		t.Fatalf("empty block IDs should clear focus, focused=%+v", m.focusedTable)
	}
}

func TestHeightChangeWithoutTablesSkipsCacheRebuild(t *testing.T) {
	m := New([]MessageItem{{TS: "1.0", Text: "plain"}}, "general")
	m.View(10, 20)
	if m.cacheHasTables {
		t.Fatal("plain message should not mark cacheHasTables")
	}
	oldTableH := m.cacheTableH
	m.View(30, 20)
	if m.cacheTableH != oldTableH {
		t.Fatalf("height-only view without tables should not rebuild cache: old=%d new=%d", oldTableH, m.cacheTableH)
	}
	m.SetMessages([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{testTableBlock("t", 1, []string{"A"})}}})
	m.View(30, 20)
	if !m.cacheHasTables || m.cacheTableH != blockkit.DefaultTableMaxHeight(m.lastViewHeight) {
		t.Fatalf("table render should refresh cache table height: hasTables=%v cacheTableH=%d", m.cacheHasTables, m.cacheTableH)
	}
}

func TestSetChannelNamePreservesActiveTableState(t *testing.T) {
	m := New([]MessageItem{{TS: "1.0", Blocks: []blockkit.Block{testTableBlock("t", 3, []string{"A", "B", "C"})}}}, "old")
	m.View(12, 12)
	if !m.ActivateTableMode() {
		t.Fatal("ActivateTableMode should succeed")
	}
	if !m.ScrollFocusedTable(2, 0) {
		t.Fatal("ScrollFocusedTable should succeed")
	}
	m.SetChannelName("renamed")
	region, ok := m.FocusedTableRegion()
	if !ok || region.Key.MessageTS != "1.0" || region.XOffset != 2 {
		t.Fatalf("metadata refresh should preserve table focus/offset: %+v ok=%v", region, ok)
	}
}

func TestReconcileTableStateAfterRenderFastPathNoTables(t *testing.T) {
	m := New(nil, "general")
	m.cacheHasTables = true
	m.cache = []viewEntry{{tableRegions: []blockkit.TableRegion{{Key: blockkit.TableKey{MessageTS: "1.0", Path: "blocks/0", BlockID: "x"}}}}}
	allocs := testing.AllocsPerRun(1000, func() {
		if m.reconcileTableStateAfterRender() {
			panic("unexpected change")
		}
	})
	if allocs != 0 {
		t.Fatalf("fast path allocs = %v, want 0", allocs)
	}
}
