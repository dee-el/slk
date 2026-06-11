package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typeCommand(a *App, s string) {
	for _, r := range s {
		_ = handleCommandMode(a, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestCommandMode_TypingBuildsBufferAndPrompt(t *testing.T) {
	a := NewApp()
	a.enterCommandMode()
	if a.mode != ModeCommand {
		t.Fatalf("mode = %v, want ModeCommand", a.mode)
	}
	typeCommand(a, "vsp")
	if a.cmdline != "vsp" {
		t.Fatalf("cmdline = %q, want %q", a.cmdline, "vsp")
	}
	if out := a.statusbar.View(120); !strings.Contains(out, ":vsp") {
		t.Fatalf("status bar missing prompt :vsp:\n%s", out)
	}
}

func TestCommandMode_EscapeCancels(t *testing.T) {
	a := NewApp()
	a.enterCommandMode()
	typeCommand(a, "ws")
	_ = handleCommandMode(a, tea.KeyPressMsg{Code: tea.KeyEscape})
	if a.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal", a.mode)
	}
	if a.cmdline != "" {
		t.Fatalf("cmdline = %q, want empty after cancel", a.cmdline)
	}
	if out := a.statusbar.View(120); strings.Contains(out, ":ws") {
		t.Fatalf("prompt should be cleared from status bar:\n%s", out)
	}
}

func TestCommandMode_BackspaceEditsAndCancelsAtEmpty(t *testing.T) {
	a := NewApp()
	a.enterCommandMode()
	typeCommand(a, "ab")
	_ = handleCommandMode(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if a.cmdline != "a" {
		t.Fatalf("cmdline = %q, want %q", a.cmdline, "a")
	}
	_ = handleCommandMode(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if a.cmdline != "" {
		t.Fatalf("cmdline = %q, want empty", a.cmdline)
	}
	// Backspace past the ':' cancels, like vim.
	_ = handleCommandMode(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if a.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal after backspace on empty buffer", a.mode)
	}
}

func TestCommandMode_EnterExecutesUnknownCommandToast(t *testing.T) {
	a := NewApp()
	a.enterCommandMode()
	typeCommand(a, "bogus")
	cmd := handleCommandMode(a, tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal after Enter", a.mode)
	}
	if cmd == nil {
		t.Fatal("expected toast-clear cmd for unknown command")
	}
	if out := a.statusbar.View(120); !strings.Contains(out, "Unknown command: bogus") {
		t.Fatalf("expected unknown-command toast:\n%s", out)
	}
}

func TestCommandMode_EnterExecutesWS(t *testing.T) {
	a := NewApp()
	a.enterCommandMode()
	typeCommand(a, "ws")
	_ = handleCommandMode(a, tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.mode != ModeWorkspaceFinder {
		t.Fatalf("mode = %v, want ModeWorkspaceFinder", a.mode)
	}
}

func TestCommandMode_EnterOnEmptyJustExits(t *testing.T) {
	a := NewApp()
	a.enterCommandMode()
	cmd := handleCommandMode(a, tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal", a.mode)
	}
	if cmd != nil {
		t.Fatal("empty Enter should produce no cmd")
	}
}
