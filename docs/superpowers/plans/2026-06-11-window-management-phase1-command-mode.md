# Window Management Phase 1: Command Mode + ctrl+w Reclaim — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire up the stubbed `:` command mode (prompt in the status bar, command registry with `ws`) and unbind `ctrl+w` from the workspace finder so it is free to become the window-command prefix in Phase 2.

**Architecture:** `:` in normal mode enters the existing-but-stubbed `ModeCommand`; typed text accumulates in a new `App.cmdline` buffer and renders in the status bar's left segment (replacing the channel/workspace segments while active). Enter parses the line and dispatches through a new command registry (`internal/ui/command.go`); unknown commands toast via the existing `toastWithClear` helper. The workspace finder's `ctrl+w` binding becomes keyless (so help still lists `:ws`), and a `ws` command replaces it.

**Tech Stack:** Go 1.26, bubbletea v2, bubbles v2 (`key`), lipgloss v2. Tests are plain `go test` table/assertion style following `internal/ui/*_test.go` conventions.

**Spec:** `docs/superpowers/specs/2026-06-11-window-management-design.md` (Goals 2–3, Design §5, Phasing item 1).

---

## Context for the implementer

- **Mode FSM:** `tea.KeyMsg` → `App.handleKey` → `dispatchModeKey` → `modeHandlers` map (`internal/ui/mode_handlers.go:50`). `ModeCommand` already exists (`internal/ui/mode.go:9`), already maps to `handleCommandMode` (`mode_handlers.go:53`), already renders "COMMAND" in the status bar (`mode.go:58`) with a dedicated style (`statusbar/model.go:183`, `styles.StatusModeCommand`). Only the handler body and the entry path are missing.
- **Key style:** handlers switch on `msg.Key().Code` for special keys (`tea.KeyEscape`, `tea.KeyEnter`, `tea.KeyBackspace`) and use `msg.String()` for printable runes — see `internal/ui/mode_presence_snooze.go:26-50`.
- **Toasts:** `toastWithClear(a, text, d)` (`internal/ui/reducer_io.go:84`) sets the status-bar toast and returns a `tea.Cmd` that clears it after `d`. Return that cmd from the handler.
- **Tests** construct keys as `tea.KeyPressMsg{Code: 'x', Text: "x"}` / `tea.KeyPressMsg{Code: tea.KeyEnter}` and call handlers directly (`handleNormalMode(a, msg)` or `a.handleKey(msg)`). `NewApp()` builds a usable App without I/O (see `newTestAppWithMessages`, `internal/ui/app_selection_test.go:14`).
- **Run tests from the repo root:** `go test ./internal/ui/...`

---

### Task 1: Status bar command-line slot

The status bar gets a `commandLine` field. While non-empty, the left side of the bar shows the prompt (e.g. `:vsp▌`) instead of the channel/workspace segments; the mode label stays.

**Files:**
- Modify: `internal/ui/statusbar/model.go`
- Test: `internal/ui/statusbar/commandline_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/ui/statusbar/commandline_test.go`:

```go
package statusbar

import (
	"strings"
	"testing"
)

func TestSetCommandLine_RendersPromptInView(t *testing.T) {
	m := New()
	m.SetCommandLine(":vsp")
	out := m.View(80)
	if !strings.Contains(out, ":vsp") {
		t.Fatalf("expected view to contain %q, got:\n%s", ":vsp", out)
	}
}

func TestSetCommandLine_HidesChannelSegmentWhileActive(t *testing.T) {
	m := New()
	m.SetChannel("general")
	m.SetCommandLine(":ws")
	if out := m.View(80); strings.Contains(out, "general") {
		t.Fatalf("channel segment should be hidden while command line active:\n%s", out)
	}
	m.SetCommandLine("")
	if out := m.View(80); !strings.Contains(out, "general") {
		t.Fatalf("channel segment should return after clearing command line:\n%s", out)
	}
}

func TestSetCommandLine_BumpsVersion(t *testing.T) {
	m := New()
	v0 := m.Version()
	m.SetCommandLine(":q")
	if m.Version() == v0 {
		t.Fatal("SetCommandLine should bump version")
	}
	v1 := m.Version()
	m.SetCommandLine(":q") // no change → no bump
	if m.Version() != v1 {
		t.Fatal("identical SetCommandLine should not bump version")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/statusbar/ -run TestSetCommandLine -v`
Expected: compile error — `m.SetCommandLine undefined`.

- [ ] **Step 3: Implement the command-line slot**

In `internal/ui/statusbar/model.go`, add the field to `Model` (after the `helpHint` field, before `version`):

```go
	commandLine string    // "" == inactive; otherwise the :prompt shown in place of channel/workspace
```

Add the setter next to `SetHelpHint`:

```go
// SetCommandLine shows a vi-style command prompt (e.g. ":vsp") in the
// left segment of the bar, replacing the channel/workspace segments
// while non-empty. Pass "" to restore the normal segments. The caller
// owns the ':' prefix and any cursor glyph.
func (m *Model) SetCommandLine(s string) {
	if m.commandLine != s {
		m.commandLine = s
		m.dirty()
	}
}
```

In `View` (`model.go:255`), replace:

```go
	left := lipgloss.JoinHorizontal(lipgloss.Center, modeLabel, channelInfo, wsInfo)
```

with:

```go
	var left string
	if m.commandLine != "" {
		cmdInfo := styles.StatusBar.Render(fmt.Sprintf(" %s▌ ", m.commandLine))
		left = lipgloss.JoinHorizontal(lipgloss.Center, modeLabel, cmdInfo)
	} else {
		left = lipgloss.JoinHorizontal(lipgloss.Center, modeLabel, channelInfo, wsInfo)
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/statusbar/ -v`
Expected: all PASS (new tests and existing statusbar tests).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/statusbar/model.go internal/ui/statusbar/commandline_test.go
git commit -m "feat(statusbar): command-line prompt slot for vi-style : commands"
```

---

### Task 2: Command registry

A registry mapping command names to handlers, plus `executeCommand` to parse a line and dispatch. v1 has one command: `ws` (workspace finder). Later phases register `sp`, `vsp`, `q`, `only`.

**Files:**
- Create: `internal/ui/command.go`
- Test: `internal/ui/command_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/ui/command_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestExecuteCommand -v`
Expected: compile error — `executeCommand` undefined.

- [ ] **Step 3: Implement the registry**

Create `internal/ui/command.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run TestExecuteCommand -v`
Expected: 4 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/command.go internal/ui/command_test.go
git commit -m "feat(ui): vi-style :command registry with :ws"
```

---

### Task 3: Command-mode prompt handler

Replace the `handleCommandMode` stub: printable keys append to a new `App.cmdline` buffer (mirrored to the status bar), Backspace edits (and cancels when the buffer is empty, like vim), Esc cancels, Enter executes via `executeCommand`.

**Files:**
- Modify: `internal/ui/mode_command.go` (full rewrite of the stub)
- Modify: `internal/ui/app.go` (one new field)
- Test: `internal/ui/mode_command_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/ui/mode_command_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestCommandMode -v`
Expected: compile error — `a.enterCommandMode` / `a.cmdline` undefined.

- [ ] **Step 3: Add the buffer field to App**

In `internal/ui/app.go`, in the `// State` block of the `App` struct (after `keys KeyMap`, app.go:111), add:

```go
	// cmdline accumulates the text typed at the vi-style ':' prompt
	// while in ModeCommand. Owned by mode_command.go; always "" in
	// every other mode.
	cmdline string
```

- [ ] **Step 4: Rewrite the command-mode handler**

Replace the entire contents of `internal/ui/mode_command.go` with:

```go
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
```

Note: the old stub imported `charm.land/bubbles/v2/key`; that import goes away.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run TestCommandMode -v`
Expected: 6 PASS.

- [ ] **Step 6: Run the full ui package to catch regressions**

Run: `go test ./internal/ui/...`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/mode_command.go internal/ui/app.go internal/ui/mode_command_test.go
git commit -m "feat(ui): wire vi-style : command prompt through ModeCommand"
```

---

### Task 4: Normal-mode entry + ctrl+w reclaim

Bind `:` in normal mode to `enterCommandMode`. Unbind `ctrl+w` from the workspace finder: the binding becomes keyless-with-help so the help overlay still advertises `:ws`, and the normal-mode case is removed. Update the two stale doc mentions.

**Files:**
- Modify: `internal/ui/keys.go:90`
- Modify: `internal/ui/mode_normal.go` (doc comment lines 6-7, add `:` case, remove ctrl+w case at lines 159-161)
- Modify: `docs/STATUS.md:14,147`
- Test: `internal/ui/mode_normal_command_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/ui/mode_normal_command_test.go`:

```go
package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gammons/slk/internal/ui/help"
)

func TestNormalMode_ColonEntersCommandMode(t *testing.T) {
	a := NewApp()
	_ = handleNormalMode(a, tea.KeyPressMsg{Code: ':', Text: ":"})
	if a.mode != ModeCommand {
		t.Fatalf("mode = %v, want ModeCommand", a.mode)
	}
}

func TestNormalMode_CtrlWNoLongerOpensWorkspaceFinder(t *testing.T) {
	a := NewApp()
	_ = handleNormalMode(a, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if a.mode == ModeWorkspaceFinder {
		t.Fatal("ctrl+w must not open the workspace finder (reclaimed as window prefix)")
	}
	if a.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal (ctrl+w is a no-op in phase 1)", a.mode)
	}
}

func TestHelp_StillListsWorkspaceFinderViaWS(t *testing.T) {
	entries := help.FromKeyMap(DefaultKeyMap())
	for _, e := range entries {
		if e.Key == ":ws" && e.Desc == "switch workspace" {
			return
		}
	}
	t.Fatal("help entries missing {Key: \":ws\", Desc: \"switch workspace\"}")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestNormalMode_Colon|TestNormalMode_CtrlW|TestHelp_StillLists' -v`
Expected: `TestNormalMode_ColonEntersCommandMode` FAILs (mode stays Normal), `TestNormalMode_CtrlWNoLongerOpensWorkspaceFinder` FAILs (finder opens), `TestHelp_StillListsWorkspaceFinderViaWS` FAILs (help says "ctrl+w").

- [ ] **Step 3: Update the keymap**

In `internal/ui/keys.go:90`, replace:

```go
		WorkspaceFinder:     key.NewBinding(key.WithKeys("ctrl+w"), key.WithHelp("ctrl+w", "switch workspace")),
```

with:

```go
		// Keyless: ctrl+w is reserved as the window-command prefix
		// (window-management design §4). The keyless binding never
		// matches but keeps the help-overlay entry pointing at :ws
		// (1-9 also switch workspaces directly).
		WorkspaceFinder:     key.NewBinding(key.WithHelp(":ws", "switch workspace")),
```

- [ ] **Step 4: Update the normal-mode handler**

In `internal/ui/mode_normal.go`, delete the workspace-finder case (lines 159-161):

```go
	case key.Matches(msg, a.keys.WorkspaceFinder):
		a.workspaceFinder.Open()
		a.SetMode(ModeWorkspaceFinder)
```

and add the command-mode entry case directly after the `InsertMode` case (after line 55):

```go
	case key.Matches(msg, a.keys.CommandMode):
		a.enterCommandMode()
```

In the file's doc comment, update line 6-7's mode-entry list: replace `Ctrl-W (workspace finder)` with `: (command prompt)` so the comment reads:

```go
//   - mode entry: i (insert), Ctrl-T (channel finder), : (command
//     prompt), Ctrl-T (theme switcher), ? (help),
```

- [ ] **Step 5: Update stale docs**

In `docs/STATUS.md`:
- Line 14: change `(1-9 number keys + Ctrl+w picker)` to `(1-9 number keys + :ws picker)`
- Line 147: change `# Ctrl+w workspace picker overlay` to `# :ws workspace picker overlay`

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run 'TestNormalMode_Colon|TestNormalMode_CtrlW|TestHelp_StillLists' -v`
Expected: 3 PASS.

- [ ] **Step 7: Run the full ui package**

Run: `go test ./internal/ui/...`
Expected: all PASS. If any existing test exercised ctrl+w → workspace finder, update it to use `executeCommand(a, "ws")` or direct mode entry instead (search: `rg -l 'WorkspaceFinder' internal/ui --glob '*_test.go'`).

- [ ] **Step 8: Commit**

```bash
git add internal/ui/keys.go internal/ui/mode_normal.go internal/ui/mode_normal_command_test.go docs/STATUS.md
git commit -m "feat(ui): bind : to command mode, reclaim ctrl+w from workspace finder"
```

---

### Task 5: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Full test suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 2: Vet and gofmt**

Run: `go vet ./... && gofmt -l internal/ cmd/`
Expected: vet clean; gofmt prints no file names.

- [ ] **Step 3: Build**

Run: `go build ./cmd/slk`
Expected: clean build.

- [ ] **Step 4: Manual smoke (requires a configured slk)**

Run `./slk` and verify:
1. `:` in normal mode → status bar shows `:▌`, mode pill shows COMMAND
2. type `ws`, Enter → workspace finder opens; Esc closes it
3. `:bogus` Enter → "Unknown command: bogus" toast appears, clears after ~2s
4. `:` then Esc → back to NORMAL, prompt gone; `:` then Backspace → same
5. `ctrl+w` in normal mode → nothing happens (no workspace finder)
6. `?` help overlay → lists ":ws switch workspace", no "ctrl+w" entry

- [ ] **Step 5: Commit any fixups, then report Phase 1 complete**

Phase 2 (window tree package + multi-window rendering) gets its own plan once this lands.
