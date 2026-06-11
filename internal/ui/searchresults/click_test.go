package searchresults

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// TestBoxSizeMatchesRender locks BoxSize to the actual rendered box so the
// analytic geometry used for mouse hit-testing can't silently drift from
// what renderBox produces. Covers the footer row (server total > fetched)
// and the scrollbar (fetched > visible window) at the same time.
func TestBoxSizeMatchesRender(t *testing.T) {
	m := New()
	m.Open()
	submitQuery(&m, "deploy")
	m.SetResults(manyItems(50), 80)

	w, h := m.BoxSize(80, 24)
	box := m.renderBox(80)
	if gw := lipgloss.Width(box); w != gw {
		t.Errorf("BoxSize width = %d, rendered width = %d", w, gw)
	}
	if gh := lipgloss.Height(box); h != gh {
		t.Errorf("BoxSize height = %d, rendered height = %d", h, gh)
	}
}

// TestClickRowSelectsItem verifies a box-local click on a list row moves the
// selection to that item, and that clicks above/below the list are no-ops.
// Rows are two lines tall: row k spans lines listTopOffset+2k and +2k+1.
func TestClickRowSelectsItem(t *testing.T) {
	m := New()
	m.Open()
	submitQuery(&m, "deploy")
	m.SetResults(manyItems(6), 6)

	// Third visible row: its first line is at offset 4.
	if !m.ClickRow(80, 24, listTopOffset+4) {
		t.Fatal("ClickRow on a populated row should return true")
	}
	if m.selected != 2 {
		t.Errorf("ClickRow set selected=%d, want 2", m.selected)
	}

	// Clicking the input/title area (above the list) is a no-op.
	if m.ClickRow(80, 24, listTopOffset-1) {
		t.Error("ClickRow above the list should return false")
	}

	// Clicking past the last row's second line is a no-op.
	if m.ClickRow(80, 24, listTopOffset+12) {
		t.Error("ClickRow past the last row should return false")
	}
}

// TestClickSecondLineSelectsRow verifies a click on either line of a
// two-line row selects that row.
func TestClickSecondLineSelectsRow(t *testing.T) {
	m := New()
	m.Open()
	submitQuery(&m, "deploy")
	m.SetResults(manyItems(6), 6)

	// Second line of row 0.
	if !m.ClickRow(80, 24, listTopOffset+1) {
		t.Fatal("ClickRow on row 0 line 2 should return true")
	}
	if m.selected != 0 {
		t.Errorf("selected = %d, want 0", m.selected)
	}

	// Second line of row 3 (lines 6 and 7).
	if !m.ClickRow(80, 24, listTopOffset+7) {
		t.Fatal("ClickRow on row 3 line 2 should return true")
	}
	if m.selected != 3 {
		t.Errorf("selected = %d, want 3", m.selected)
	}
}

// TestClickRowScrolledWindow verifies hit-testing agrees with the scroll
// window: with the selection at the bottom of a long list, row k maps to
// window start + k, not absolute index k.
func TestClickRowScrolledWindow(t *testing.T) {
	m := New()
	m.Open()
	submitQuery(&m, "deploy")
	m.SetResults(manyItems(15), 15)
	m.selected = 14 // window is [7, 15)

	if !m.ClickRow(80, 24, listTopOffset+0) {
		t.Fatal("ClickRow on first visible row should return true")
	}
	if m.selected != 7 {
		t.Errorf("ClickRow set selected=%d, want 7 (window start)", m.selected)
	}
}

// TestClickRowOnlyInResultsState verifies clicks on body rows in the
// input/loading/error states do not fabricate a selection.
func TestClickRowOnlyInResultsState(t *testing.T) {
	m := New()
	m.Open()
	if m.ClickRow(80, 24, listTopOffset) {
		t.Error("ClickRow in input state should return false")
	}
	submitQuery(&m, "deploy") // now loading
	if m.ClickRow(80, 24, listTopOffset) {
		t.Error("ClickRow in loading state should return false")
	}
	m.SetError("boom")
	if m.ClickRow(80, 24, listTopOffset) {
		t.Error("ClickRow in error state should return false")
	}
}
