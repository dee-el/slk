package channelfinder

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// TestBoxSizeMatchesRender locks BoxSize to the actual rendered box so the
// analytic geometry used for mouse hit-testing can't silently drift from
// what renderBox produces.
func TestBoxSizeMatchesRender(t *testing.T) {
	m := New()
	m.SetItems(testItems())
	m.Open()

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
func TestClickRowSelectsItem(t *testing.T) {
	m := New()
	m.SetItems(testItems())
	m.Open() // 6 items, empty query, selected starts at 0

	// Third visible row (offset 2 from the first list row).
	if !m.ClickRow(80, 24, listTopOffset+2) {
		t.Fatal("ClickRow on a populated row should return true")
	}
	if m.selected != 2 {
		t.Errorf("ClickRow set selected=%d, want 2", m.selected)
	}

	// Clicking the input/title area (above the list) is a no-op.
	if m.ClickRow(80, 24, listTopOffset-1) {
		t.Error("ClickRow above the list should return false")
	}

	// Clicking past the last row is a no-op.
	if m.ClickRow(80, 24, listTopOffset+6) {
		t.Error("ClickRow past the last row should return false")
	}
}
