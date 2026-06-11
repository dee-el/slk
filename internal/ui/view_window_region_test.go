package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/gammons/slk/internal/ui/messages"
	"github.com/gammons/slk/internal/ui/wintree"
)

// renderRegion renders the messages region exactly as App.View does,
// without depending on the tea.View wrapper API.
func renderRegion(a *App) string {
	frame := a.layout.Compute(a.width, a.height, a.workspaceRail.Width(), a.sidebar.Width(), a.sidebarVisible, a.threadVisible)
	return a.renderWindowsRegion(frame, 0, false)
}

func TestRegion_SingleWindowUnchanged(t *testing.T) {
	a := newWideTestApp(t)
	_, _ = a.Update(ChannelSelectedMsg{ID: "C1", Name: "general", Type: "channel"})
	frame := a.layout.Compute(a.width, a.height, a.workspaceRail.Width(), a.sidebar.Width(), a.sidebarVisible, a.threadVisible)
	multi := a.renderWindowsRegion(frame, 0, false)
	direct := a.renderMessagesRegion(frame, 0, false)
	if multi != direct {
		t.Fatal("single-window region must be byte-identical to the direct messages render")
	}
}

func TestRegion_SplitRendersLiveContentInBothWindows(t *testing.T) {
	a, _, _ := twoWindowApp(t)
	_, _ = a.Update(MessagesLoadedMsg{ChannelID: "C1", Messages: testMessageItems(2)})
	out := ansi.Strip(renderRegion(a))
	// Unfocused window (C1/general) must show real message text, not a
	// placeholder; focused window shows ops.
	if !strings.Contains(out, "msg-1") {
		t.Fatalf("unfocused window should render its channel's messages:\n%s", out)
	}
	if strings.Contains(out, "(no channel)") {
		t.Fatal("no placeholders may remain in Phase 3")
	}
}

func TestRegion_UnfocusedWindowUpdatesOnNewMessage(t *testing.T) {
	a, _, _ := twoWindowApp(t)
	_, _ = a.Update(NewMessageMsg{ChannelID: "C1", Message: messages.MessageItem{
		TS: "9.0", UserID: "U9", UserName: "zoe", Text: "live-update-proof", Timestamp: "1:00 PM",
	}})
	out := ansi.Strip(renderRegion(a))
	if !strings.Contains(out, "live-update-proof") {
		t.Fatalf("unfocused window must re-render new content:\n%s", out)
	}
}

func TestRegion_UnfocusedPaneCacheInvalidatesOnContent(t *testing.T) {
	a, _, _ := twoWindowApp(t)
	_ = renderRegion(a) // warm caches
	_, _ = a.Update(NewMessageMsg{ChannelID: "C1", Message: messages.MessageItem{
		TS: "9.1", UserID: "U9", UserName: "zoe", Text: "second-proof", Timestamp: "1:01 PM",
	}})
	out := ansi.Strip(renderRegion(a))
	if !strings.Contains(out, "second-proof") {
		t.Fatal("stale cached frame served after content change")
	}
}

func TestRegion_SplitOutputDimensionsStable(t *testing.T) {
	a := newWideTestApp(t)
	_, _ = a.Update(ChannelSelectedMsg{ID: "C1", Name: "general", Type: "channel"})
	before := renderRegion(a)
	_ = a.splitWindow(wintree.SplitSideBySide)
	after := renderRegion(a)
	if lipgloss.Height(before) != lipgloss.Height(after) {
		t.Fatalf("row count changed after split: %d -> %d", lipgloss.Height(before), lipgloss.Height(after))
	}
	if lipgloss.Width(before) != lipgloss.Width(after) {
		t.Fatalf("width changed after split: %d -> %d", lipgloss.Width(before), lipgloss.Width(after))
	}
}

// TestRegion_SurvivesHardShrinkAfterSplits guards the resize-after-
// split crash: with several side-by-side columns, a hard terminal
// shrink produces leaf rects too narrow for the messages panel's
// chrome (W-2 < 2), which used to flow a negative width into
// borderedTopPane's strings.Repeat and panic. The render must
// survive AND keep the exact region dimensions.
func TestRegion_SurvivesHardShrinkAfterSplits(t *testing.T) {
	a := NewApp()
	a.width = 400
	a.height = 50
	for i := 0; i < 3; i++ {
		if cmd := a.splitWindow(wintree.SplitSideBySide); cmd != nil {
			t.Fatalf("split %d refused at width 400", i)
		}
	}
	// Hard shrink: must not panic, must keep exact region dimensions.
	a.width, a.height = 30, 10
	frame := a.layout.Compute(a.width, a.height, a.workspaceRail.Width(), a.sidebar.Width(), a.sidebarVisible, a.threadVisible)
	out := a.renderWindowsRegion(frame, 0, false) // panics before the fix
	wantW := frame.MsgWidth + frame.MsgBorder
	if lipgloss.Height(out) != frame.ContentHeight {
		t.Fatalf("height = %d, want %d", lipgloss.Height(out), frame.ContentHeight)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w != wantW {
			t.Fatalf("line %d width = %d, want %d", i, w, wantW)
		}
	}
}

// TestRegion_SurvivesHardShrinkAfterStackedSplits is the vertical
// twin: ContentHeight < window count yields zero-extent (H=0) rects,
// which exactSize treats as "unset" (natural height) — breaking the
// region height contract unless zero-extent leaves are skipped.
func TestRegion_SurvivesHardShrinkAfterStackedSplits(t *testing.T) {
	a := NewApp()
	a.width = 200
	a.height = 50
	for i := 0; i < 3; i++ {
		if cmd := a.splitWindow(wintree.SplitStacked); cmd != nil {
			t.Fatalf("split %d refused at height 50", i)
		}
	}
	// ContentHeight (3) < window count (4) → at least one H=0 rect.
	a.width, a.height = 30, 4
	frame := a.layout.Compute(a.width, a.height, a.workspaceRail.Width(), a.sidebar.Width(), a.sidebarVisible, a.threadVisible)
	out := a.renderWindowsRegion(frame, 0, false)
	wantW := frame.MsgWidth + frame.MsgBorder
	if lipgloss.Height(out) != frame.ContentHeight {
		t.Fatalf("height = %d, want %d", lipgloss.Height(out), frame.ContentHeight)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w != wantW {
			t.Fatalf("line %d width = %d, want %d", i, w, wantW)
		}
	}
}

func TestRegion_CloseRestoresSingleWindowPath(t *testing.T) {
	a := newWideTestApp(t)
	_, _ = a.Update(ChannelSelectedMsg{ID: "C1", Name: "general", Type: "channel"})
	_ = a.splitWindow(wintree.SplitStacked)
	_ = a.closeWindow()
	if a.wins.Len() != 1 {
		t.Fatalf("Len = %d, want 1", a.wins.Len())
	}
	frame := a.layout.Compute(a.width, a.height, a.workspaceRail.Width(), a.sidebar.Width(), a.sidebarVisible, a.threadVisible)
	multi := a.renderWindowsRegion(frame, 0, false)
	direct := a.renderMessagesRegion(frame, 0, false)
	if multi != direct {
		t.Fatal("after closing back to one window the region must take the direct single-window path")
	}
}
