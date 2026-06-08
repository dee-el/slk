// internal/ui/reducer_mouse_test.go
package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gammons/slk/internal/ui/help"
)

// TestReduceMouseWheel_ScrollsActiveModal verifies that when a modal
// overlay is open, a mouse-wheel notch scrolls the items inside the
// modal (advancing its selection by mouseWheelLines) instead of
// scrolling the panel under the cursor on the main tab behind it.
func TestReduceMouseWheel_ScrollsActiveModal(t *testing.T) {
	app := NewApp()

	// Populate the help modal with enough rows to scroll through.
	entries := make([]help.Entry, 0, 20)
	for i := 0; i < 20; i++ {
		entries = append(entries, help.Entry{Key: "k", Desc: "desc"})
	}
	app.help.SetEntries(entries)
	app.help.Open()
	app.SetMode(ModeHelp)

	if got := app.help.Selected(); got != 0 {
		t.Fatalf("precondition: help selection should start at 0, got %d", got)
	}

	// A wheel-down notch (X anywhere on screen) should move the modal
	// selection down by mouseWheelLines (default 3), not touch panels.
	reduceMouseWheel(app, tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 5})
	if got, want := app.help.Selected(), app.mouseWheelLines; got != want {
		t.Fatalf("wheel down: help selection = %d, want %d", got, want)
	}

	// A wheel-up notch should move the selection back up, clamping at 0.
	reduceMouseWheel(app, tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 5})
	if got := app.help.Selected(); got != 0 {
		t.Fatalf("wheel up: help selection = %d, want 0", got)
	}
}

// TestReduceMouseWheel_NoModalLeavesModalUntouched is a guard that the
// modal-routing branch only fires when a modal mode is active: with the
// app in normal mode, a wheel notch must not advance the (open) help
// modal's selection through the modal path.
func TestReduceMouseWheel_NoModalLeavesModalUntouched(t *testing.T) {
	app := NewApp()

	entries := make([]help.Entry, 0, 20)
	for i := 0; i < 20; i++ {
		entries = append(entries, help.Entry{Key: "k", Desc: "desc"})
	}
	app.help.SetEntries(entries)
	// Note: NOT opening the modal / not setting ModeHelp; mode stays Normal.

	reduceMouseWheel(app, tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 5})
	if got := app.help.Selected(); got != 0 {
		t.Fatalf("normal mode: help selection should stay 0, got %d", got)
	}
}
