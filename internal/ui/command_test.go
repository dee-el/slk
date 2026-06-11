package ui

import (
	"strings"
	"testing"
)

func TestExecuteCommand_EmptyIsNoop(t *testing.T) {
	a := NewApp()
	if cmd := executeCommand(a, "   "); cmd != nil {
		t.Fatal("empty command line should be a no-op")
	}
	if a.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal", a.mode)
	}
}

func TestExecuteCommand_UnknownShowsToast(t *testing.T) {
	a := NewApp()
	cmd := executeCommand(a, "bogus")
	if cmd == nil {
		t.Fatal("unknown command should return the toast-clear cmd")
	}
	if out := a.statusbar.View(120); !strings.Contains(out, "Unknown command: bogus") {
		t.Fatalf("expected unknown-command toast, got:\n%s", out)
	}
}

func TestExecuteCommand_WSOpensWorkspaceFinder(t *testing.T) {
	a := NewApp()
	_ = executeCommand(a, "ws")
	if a.mode != ModeWorkspaceFinder {
		t.Fatalf("mode = %v, want ModeWorkspaceFinder", a.mode)
	}
}

func TestExecuteCommand_TrimsAndIgnoresArgs(t *testing.T) {
	a := NewApp()
	_ = executeCommand(a, "  ws   extra  ")
	if a.mode != ModeWorkspaceFinder {
		t.Fatalf("mode = %v, want ModeWorkspaceFinder", a.mode)
	}
}
