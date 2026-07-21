package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gammons/slk/internal/ui/help"
	"github.com/gammons/slk/internal/ui/messages"
	"github.com/gammons/slk/internal/ui/messages/blockkit"
)

func TestAppTableModeRoutesKeysAndPreservesSelection(t *testing.T) {
	app := NewApp()
	app.activeChannelID = "C1"
	app.focusedPanel = PanelMessages
	app.messagepane.SetMessages([]messages.MessageItem{{
		TS: "1.0",
		Blocks: []blockkit.Block{
			blockkit.TableBlock{BlockID: "t0", Rows: [][]blockkit.TableCell{{{Text: "R0"}}, {{Text: "R1"}}, {{Text: "R2"}}, {{Text: "R3"}}, {{Text: "R4"}}, {{Text: "R5"}}}, Columns: []blockkit.TableColumn{{}}},
			blockkit.TableBlock{BlockID: "t1", Rows: [][]blockkit.TableCell{{{Text: "Alpha"}, {Text: "Bravo"}, {Text: "Charlie"}}}, Columns: []blockkit.TableColumn{{}, {}, {}}},
		},
	}})
	_ = app.messagepane.View(8, 18)
	selected := app.messagepane.SelectedIndex()

	if cmd := app.handleNormalMode(tea.KeyPressMsg{Code: 't', Text: "t"}); cmd != nil {
		_ = cmd
	}
	if app.mode != ModeTable {
		t.Fatalf("mode = %v, want ModeTable", app.mode)
	}
	if !strings.Contains(app.statusbar.View(80), "TABLE") {
		t.Fatalf("status bar should show TABLE mode, got %q", app.statusbar.View(80))
	}
	region, ok := app.messagepane.FocusedTableRegion()
	if !ok || region.Key.Path != "blocks/0" || region.MaxY == 0 {
		t.Fatalf("initial focused table = %+v ok=%v", region, ok)
	}
	app.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	region, _ = app.messagepane.FocusedTableRegion()
	if region.YOffset != 1 {
		t.Fatalf("j in table mode should scroll table, region=%+v", region)
	}
	if app.messagepane.SelectedIndex() != selected {
		t.Fatalf("table mode must not move outer message selection: %d -> %d", selected, app.messagepane.SelectedIndex())
	}
	app.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	region, _ = app.messagepane.FocusedTableRegion()
	if region.Key.Path != "blocks/1" {
		t.Fatalf("tab should focus next table, got %+v", region)
	}
	handleTableMode(app, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	region, _ = app.messagepane.FocusedTableRegion()
	if region.Key.Path != "blocks/0" || region.YOffset != 1 {
		t.Fatalf("shift+tab should return to first table with preserved offset, got %+v", region)
	}
	app.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if app.mode != ModeNormal {
		t.Fatalf("escape should leave table mode, got %v", app.mode)
	}
}

func TestAppTableModeNoTableUsesToast(t *testing.T) {
	app := NewApp()
	app.activeChannelID = "C1"
	app.focusedPanel = PanelMessages
	app.messagepane.SetMessages([]messages.MessageItem{{TS: "1.0", Text: "plain"}})
	_ = app.messagepane.View(8, 18)
	if cmd := app.handleNormalMode(tea.KeyPressMsg{Code: 't', Text: "t"}); cmd != nil {
		_ = cmd
	}
	if app.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal when no table exists", app.mode)
	}
	if !strings.Contains(app.statusbar.View(80), "No table in view") {
		t.Fatalf("expected no-table toast, got %q", app.statusbar.View(80))
	}
}

func TestAppSyncTableModeReturnsToNormalOnModelDrift(t *testing.T) {
	app := NewApp()
	app.width = 80
	app.height = 24
	app.activeChannelID = "C1"
	app.focusedPanel = PanelMessages
	app.messagepane.SetMessages([]messages.MessageItem{{TS: "1.0", Blocks: []blockkit.Block{blockkit.TableBlock{BlockID: "t", Rows: [][]blockkit.TableCell{{{Text: "A"}}}, Columns: []blockkit.TableColumn{{}}}}}})
	_ = app.messagepane.View(8, 20)
	app.enterTableMode()
	app.messagepane.AppendMessage(messages.MessageItem{TS: "2.0", Text: "later"})
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if app.mode != ModeNormal {
		t.Fatalf("main drift should return app to normal mode, got %v", app.mode)
	}

	parent := messages.MessageItem{TS: "P1", UserName: "alice", Text: "parent"}
	replies := []messages.MessageItem{{TS: "R1", UserName: "bob", Blocks: []blockkit.Block{blockkit.TableBlock{BlockID: "reply", Rows: [][]blockkit.TableCell{{{Text: "A"}}}, Columns: []blockkit.TableColumn{{}}}}}}
	app.threadPanel.SetThread(parent, replies, "C1", "P1")
	app.threadVisible = true
	app.focusedPanel = PanelThread
	_ = app.threadPanel.View(12, 20)
	app.enterTableMode()
	app.threadPanel.AddReply(messages.MessageItem{TS: "R2", UserName: "carol", Text: "later"})
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if app.mode != ModeNormal {
		t.Fatalf("thread drift should return app to normal mode, got %v", app.mode)
	}
}

func TestHelpIncludesTableModeEntries(t *testing.T) {
	entries := help.FromKeyMap(DefaultKeyMap())
	plain := make([]string, 0, len(entries))
	for _, entry := range entries {
		plain = append(plain, entry.Key+"|"+entry.Desc)
	}
	joined := strings.Join(plain, "\n")
	for _, want := range []string{"TABLE h/j/k/l|pan table", "TABLE PgUp/PgDn|page table", "TABLE ctrl+u/d|half-page table", "TABLE Tab/shift+tab|next/prev table", "TABLE esc/q|exit table mode"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("help entries missing %q in %q", want, joined)
		}
	}
}

func TestRefreshActiveChannelMetadataPreservesTableMode(t *testing.T) {
	app := NewApp()
	app.activeChannelID = "C1"
	app.focusedPanel = PanelMessages
	app.mode = ModeNormal
	app.messagepane.SetChannel("old", "")
	app.messagepane.SetMessages([]messages.MessageItem{{TS: "1.0", Blocks: []blockkit.Block{blockkit.TableBlock{BlockID: "t", Rows: [][]blockkit.TableCell{{{Text: "A"}, {Text: "B"}, {Text: "C"}}}, Columns: []blockkit.TableColumn{{}, {}, {}}}}}})
	_ = app.messagepane.View(12, 12)
	app.enterTableMode()
	if !app.messagepane.ScrollFocusedTable(2, 0) {
		t.Fatal("ScrollFocusedTable should succeed")
	}
	app.refreshActiveChannelMetadata("C1", "renamed", "channel")
	if app.mode != ModeTable {
		t.Fatalf("metadata refresh should keep app in table mode, got %v", app.mode)
	}
	region, ok := app.messagepane.FocusedTableRegion()
	if !ok || region.XOffset != 2 {
		t.Fatalf("metadata refresh should preserve table offset: %+v ok=%v", region, ok)
	}
}

func TestAppThreadTableModeNearestVisibleReplyRetargetsSelection(t *testing.T) {
	app := NewApp()
	app.width = 80
	app.height = 24
	app.threadVisible = true
	app.focusedPanel = PanelThread
	parent := messages.MessageItem{TS: "P1", UserName: "alice", Text: "parent"}
	replies := []messages.MessageItem{
		{TS: "R1", UserName: "bob", Text: "plain"},
		{TS: "R2", UserName: "carol", Blocks: []blockkit.Block{blockkit.TableBlock{BlockID: "t", Rows: [][]blockkit.TableCell{{{Text: "A"}}}, Columns: []blockkit.TableColumn{{}}}}},
	}
	app.threadPanel.SetThread(parent, replies, "C1", "P1")
	app.threadPanel.SelectByIndex(0)
	_ = app.threadPanel.View(18, 24)
	if cmd := app.enterTableMode(); cmd != nil {
		_ = cmd
	}
	if app.mode != ModeTable {
		t.Fatalf("app mode = %v, want ModeTable", app.mode)
	}
	if sel := app.threadPanel.SelectedReply(); sel == nil || sel.TS != "R2" {
		t.Fatalf("thread selection should retarget to visible reply table, got %+v", sel)
	}
	if !app.threadPanel.TableModeActive() {
		t.Fatal("thread table mode should remain active after retarget")
	}
}
