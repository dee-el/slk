package workspacefinder

import (
	"testing"

	"charm.land/lipgloss/v2"
)

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

func TestClickRowSelectsItem(t *testing.T) {
	m := New()
	m.SetItems(testItems())
	m.Open() // 4 items, selected starts at 0

	if !m.ClickRow(80, 24, listTopOffset+2) {
		t.Fatal("ClickRow on a populated row should return true")
	}
	if m.selected != 2 {
		t.Errorf("ClickRow set selected=%d, want 2", m.selected)
	}
	if m.ClickRow(80, 24, listTopOffset-1) {
		t.Error("ClickRow above the list should return false")
	}
	if m.ClickRow(80, 24, listTopOffset+4) {
		t.Error("ClickRow past the last row should return false")
	}
}
