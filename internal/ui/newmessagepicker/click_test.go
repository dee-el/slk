package newmessagepicker

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestBoxSizeMatchesRender(t *testing.T) {
	m := New()
	m.SetUsers(testUsers())
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

func TestClickRowMovesHighlight(t *testing.T) {
	m := New()
	m.SetUsers(testUsers())
	m.Open() // 5 users, highlight starts at 0

	if !m.ClickRow(80, 24, listTopOffset+2) {
		t.Fatal("ClickRow on a populated row should return true")
	}
	if m.highlight != 2 {
		t.Errorf("ClickRow set highlight=%d, want 2", m.highlight)
	}
	if m.ClickRow(80, 24, listTopOffset-1) {
		t.Error("ClickRow above the list should return false")
	}
	if m.ClickRow(80, 24, listTopOffset+5) {
		t.Error("ClickRow past the last row should return false")
	}
}
