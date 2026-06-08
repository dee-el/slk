package presencemenu

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func TestBoxSizeMatchesRender(t *testing.T) {
	m := New()
	m.OpenWith("Workspace", "active", false, time.Time{})

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
	m.OpenWith("Workspace", "active", false, time.Time{})

	if !m.ClickRow(80, 24, listTopOffset+3) {
		t.Fatal("ClickRow on a populated row should return true")
	}
	if m.selected != 3 {
		t.Errorf("ClickRow set selected=%d, want 3", m.selected)
	}
	if m.ClickRow(80, 24, listTopOffset-1) {
		t.Error("ClickRow above the list should return false")
	}
	if m.ClickRow(80, 24, listTopOffset+len(m.filtered)) {
		t.Error("ClickRow past the last row should return false")
	}
}
