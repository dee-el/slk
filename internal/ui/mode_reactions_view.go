// internal/ui/mode_reactions_view.go
//
// Reactions-view key handler. Forwards normalised keys to the
// reactionsview overlay (esc/q/L close, up/down scroll), then drops back
// to Normal mode when the overlay reports itself invisible.
package ui

import (
	tea "charm.land/bubbletea/v2"
)

func handleReactionsViewMode(a *App, msg tea.KeyMsg) tea.Cmd {
	keyStr := msg.String()
	switch msg.Key().Code {
	case tea.KeyEscape:
		keyStr = "esc"
	case tea.KeyUp:
		keyStr = "up"
	case tea.KeyDown:
		keyStr = "down"
	}
	a.reactionsView.HandleKey(keyStr)
	if !a.reactionsView.IsVisible() {
		a.SetMode(ModeNormal)
	}
	return nil
}
