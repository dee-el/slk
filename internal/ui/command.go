// internal/ui/command.go
//
// The vi-style ":" command registry (window-management design §5).
//
// executeCommand parses a command line (without the leading ':')
// and dispatches through the commands map — the designated
// extension point for future :commands. Later phases of the
// window-management plan register sp / vsp / q / only here.
package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// commandFunc executes a named :command. args holds the
// whitespace-separated tokens after the command name (unused by
// v1 commands, reserved for e.g. ":sp #channel").
type commandFunc func(a *App, args []string) tea.Cmd

// commands maps a command name to its handler. Names are matched
// exactly (no prefix matching); aliases get their own entries.
var commands = map[string]commandFunc{
	"ws": cmdWorkspaceFinder,
}

// cmdWorkspaceFinder opens the workspace finder overlay —
// the :command replacement for the finder's old ctrl+w binding.
func cmdWorkspaceFinder(a *App, _ []string) tea.Cmd {
	a.workspaceFinder.Open()
	a.SetMode(ModeWorkspaceFinder)
	return nil
}

// executeCommand parses and runs one command line (without the
// leading ':'). Empty input is a no-op; unknown commands show a
// transient status-bar toast.
func executeCommand(a *App, line string) tea.Cmd {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	fn, ok := commands[fields[0]]
	if !ok {
		return toastWithClear(a, "Unknown command: "+fields[0], 2*time.Second)
	}
	return fn(a, fields[1:])
}
