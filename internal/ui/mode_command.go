// internal/ui/mode_command.go
//
// Command-mode key handler: the vi-style ":" prompt
// (window-management design §5).
//
// Entry: enterCommandMode (bound to ':' in normal mode). Printable
// keys append to App.cmdline, mirrored into the status bar's
// command-line slot. Backspace edits — and cancels when the buffer
// is already empty, matching vim's backspace-past-':' behavior.
// Esc cancels. Enter executes the line through the command
// registry (internal/ui/command.go) after returning to Normal, so
// commands that set their own mode (e.g. :ws) win.
package ui

import (
	tea "charm.land/bubbletea/v2"
)

// enterCommandMode switches to ModeCommand with an empty buffer
// and shows the bare ':' prompt in the status bar.
func (a *App) enterCommandMode() {
	a.cmdline = ""
	a.statusbar.SetCommandLine(":")
	a.SetMode(ModeCommand)
}

// exitCommandMode clears the buffer and prompt and returns to
// Normal. Shared by the cancel and execute paths.
func (a *App) exitCommandMode() {
	a.cmdline = ""
	a.statusbar.SetCommandLine("")
	a.SetMode(ModeNormal)
}

func handleCommandMode(a *App, msg tea.KeyMsg) tea.Cmd {
	switch msg.Key().Code {
	case tea.KeyEscape:
		a.exitCommandMode()
		return nil
	case tea.KeyEnter:
		line := a.cmdline
		a.exitCommandMode()
		return executeCommand(a, line)
	case tea.KeyBackspace:
		if a.cmdline == "" {
			a.exitCommandMode()
			return nil
		}
		a.cmdline = a.cmdline[:len(a.cmdline)-1]
		a.statusbar.SetCommandLine(":" + a.cmdline)
		return nil
	}
	s := msg.String()
	if s == "space" {
		s = " "
	}
	if len(s) == 1 && s[0] >= 32 && s[0] <= 126 {
		a.cmdline += s
		a.statusbar.SetCommandLine(":" + a.cmdline)
	}
	return nil
}
