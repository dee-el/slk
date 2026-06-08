// internal/ui/reducer_modal_click_test.go
package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gammons/slk/internal/ui/channelfinder"
	"github.com/gammons/slk/internal/ui/help"
)

func openChannelFinder(t *testing.T) *App {
	t.Helper()
	app := NewApp()
	app.width = 80
	app.height = 24
	// Descending LastVisited keeps these in declared order under the
	// empty-query sort, so row N maps to items[N].
	app.channelFinder.SetItems([]channelfinder.Item{
		{ID: "C1", Name: "alpha", Type: "channel", Joined: true, LastVisited: 300},
		{ID: "C2", Name: "bravo", Type: "channel", Joined: true, LastVisited: 200},
		{ID: "C3", Name: "charlie", Type: "channel", Joined: true, LastVisited: 100},
	})
	app.channelFinder.Open()
	app.SetMode(ModeChannelFinder)
	return app
}

// boxOrigin returns the top-left screen coords of the active channel
// finder box, mirroring overlay.DimmedOverlay's centering.
func channelFinderOrigin(app *App) (int, int) {
	w, h := app.channelFinder.BoxSize(app.width, app.height)
	x := (app.width - w) / 2
	y := (app.height - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

// TestModalClick_OnRowActivates verifies that clicking a list row selects
// that item and activates it (Enter), closing the modal.
func TestModalClick_OnRowActivates(t *testing.T) {
	app := openChannelFinder(t)
	startX, startY := channelFinderOrigin(app)

	// Row offset 2 from the first list row. NewApp pins a synthetic
	// "Threads" row at the top, so offset 0=Threads, 1=C1, 2=C2.
	clickY := startY + 5 + 2
	clickX := startX + 3

	cmd := reduceMouseClick(app, tea.MouseClickMsg{Button: tea.MouseLeft, X: clickX, Y: clickY})
	if cmd == nil {
		t.Fatal("clicking a row should return an activation cmd")
	}
	msg := cmd()
	sel, ok := msg.(ChannelSelectedMsg)
	if !ok {
		t.Fatalf("expected ChannelSelectedMsg, got %T", msg)
	}
	if sel.ID != "C2" {
		t.Errorf("clicked second row, selected ID = %q, want C2", sel.ID)
	}
	if app.channelFinder.IsVisible() {
		t.Error("finder should close after activating a row")
	}
	if app.mode != ModeNormal {
		t.Errorf("mode = %v after activation, want ModeNormal", app.mode)
	}
}

// TestModalClick_OutsideDismisses verifies that clicking outside the modal
// box dismisses it (Esc path) without selecting anything.
func TestModalClick_OutsideDismisses(t *testing.T) {
	app := openChannelFinder(t)

	// Top-left corner is outside the centered box.
	reduceMouseClick(app, tea.MouseClickMsg{Button: tea.MouseLeft, X: 0, Y: 0})

	if app.channelFinder.IsVisible() {
		t.Error("finder should be dismissed by an outside click")
	}
	if app.mode != ModeNormal {
		t.Errorf("mode = %v after outside click, want ModeNormal", app.mode)
	}
}

// TestModalClick_InsideNonRowIsNoop verifies that clicking inside the box
// but not on a list row (e.g. the title area) neither activates nor
// dismisses the modal.
func TestModalClick_InsideNonRowIsNoop(t *testing.T) {
	app := openChannelFinder(t)
	startX, startY := channelFinderOrigin(app)

	// Title row sits at box-local y=2, above the list (y>=5).
	cmd := reduceMouseClick(app, tea.MouseClickMsg{Button: tea.MouseLeft, X: startX + 3, Y: startY + 2})
	if cmd != nil {
		t.Errorf("clicking the title area should not return a cmd, got %v", cmd)
	}
	if !app.channelFinder.IsVisible() {
		t.Error("finder should stay open when clicking inside but not on a row")
	}
	if app.mode != ModeChannelFinder {
		t.Errorf("mode = %v, want ModeChannelFinder (unchanged)", app.mode)
	}
}

// TestModalClick_HelpOutsideDismisses guards the no-activation modal path:
// help has no Enter action, but an outside click must still dismiss it.
func TestModalClick_HelpOutsideDismisses(t *testing.T) {
	app := NewApp()
	app.width = 80
	app.height = 24
	app.help.SetEntries([]help.Entry{{Key: "j", Desc: "down"}, {Key: "k", Desc: "up"}})
	app.help.Open()
	app.SetMode(ModeHelp)

	reduceMouseClick(app, tea.MouseClickMsg{Button: tea.MouseLeft, X: 0, Y: 0})

	if app.help.IsVisible() {
		t.Error("help should be dismissed by an outside click")
	}
	if app.mode != ModeNormal {
		t.Errorf("mode = %v after outside click, want ModeNormal", app.mode)
	}
}
