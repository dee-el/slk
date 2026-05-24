// internal/ui/mode_help.go
//
// Help-mode key handler (Phase 5c).
//
// Help mode forwards normalised keys to the help overlay
// (handles q/?/esc for self-dismiss + Up/Down for scrolling),
// then drops back to Normal when the overlay reports invisible.
package ui

import (
	tea "charm.land/bubbletea/v2"
)

func handleHelpMode(a *App, msg tea.KeyMsg) tea.Cmd {
	keyStr := msg.String()
	switch msg.Key().Code {
	case tea.KeyEnter:
		keyStr = "enter"
	case tea.KeyEscape:
		keyStr = "esc"
	case tea.KeyUp:
		keyStr = "up"
	case tea.KeyDown:
		keyStr = "down"
	case tea.KeyBackspace:
		keyStr = "backspace"
	}
	a.help.HandleKey(keyStr)
	if !a.help.IsVisible() {
		a.SetMode(ModeNormal)
	}
	return nil
}
