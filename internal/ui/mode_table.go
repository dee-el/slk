package ui

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

type tablePane interface {
	ActivateTableMode() bool
	DeactivateTableMode()
	TableModeActive() bool
	ScrollFocusedTable(dx, dy int) bool
	PageFocusedTable(page int, half bool) bool
	FocusNextTable() bool
	FocusPrevTable() bool
}

func (a *App) focusedTablePane() tablePane {
	switch a.focusedPanel {
	case PanelMessages:
		if a.view != ViewThreads {
			return a.messagepane
		}
	case PanelThread:
		if a.threadVisible {
			return a.threadPanel
		}
	}
	return nil
}

func (a *App) enterTableMode() tea.Cmd {
	pane := a.focusedTablePane()
	if pane == nil || !pane.ActivateTableMode() {
		return toastWithClear(a, "No table in view", 2*time.Second)
	}
	a.SetMode(ModeTable)
	return nil
}

func (a *App) syncTableMode() {
	if a.mode != ModeTable {
		return
	}
	pane := a.focusedTablePane()
	if pane != nil && pane.TableModeActive() {
		return
	}
	a.SetMode(ModeNormal)
}

func handleTableMode(a *App, msg tea.KeyMsg) tea.Cmd {
	pane := a.focusedTablePane()
	if pane == nil || !pane.TableModeActive() {
		a.SetMode(ModeNormal)
		return nil
	}

	switch {
	case key.Matches(msg, a.keys.Escape), key.Matches(msg, a.keys.CloseThreadView):
		a.SetMode(ModeNormal)
	case key.Matches(msg, a.keys.Left):
		pane.ScrollFocusedTable(-1, 0)
	case key.Matches(msg, a.keys.Right):
		pane.ScrollFocusedTable(1, 0)
	case key.Matches(msg, a.keys.Up):
		pane.ScrollFocusedTable(0, -1)
	case key.Matches(msg, a.keys.Down):
		pane.ScrollFocusedTable(0, 1)
	case key.Matches(msg, a.keys.PageUp):
		pane.PageFocusedTable(-1, false)
	case key.Matches(msg, a.keys.PageDown):
		pane.PageFocusedTable(1, false)
	case key.Matches(msg, a.keys.HalfPageUp):
		pane.PageFocusedTable(-1, true)
	case key.Matches(msg, a.keys.HalfPageDown):
		pane.PageFocusedTable(1, true)
	case key.Matches(msg, a.keys.Tab):
		pane.FocusNextTable()
	case key.Matches(msg, a.keys.ShiftTab):
		pane.FocusPrevTable()
	}
	return nil
}
