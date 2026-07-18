package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gammons/slk/internal/cache"
	"github.com/gammons/slk/internal/config"
	"github.com/gammons/slk/internal/ui"
	"github.com/gammons/slk/internal/ui/channelfinder"
	"github.com/gammons/slk/internal/ui/sidebar"
)

func TestResolveUnresolvedDMs_SendsUserAndDMUpdates(t *testing.T) {
	unresolved := []UnresolvedDM{{ChannelID: "D1", UserID: "U1"}}
	var msgs []tea.Msg
	botStore := newBotUserIDStore(nil)

	resolveUnresolvedDMs(
		unresolved,
		func(userID string) (string, bool) { return "alice", true },
		func(userID string) { botStore.Set(userID) },
		func(msg tea.Msg) { msgs = append(msgs, msg) },
		"T1",
	)

	if !botStore.Has("U1") {
		t.Fatal("bot store missing U1 after bot resolution")
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	userMsg, ok := msgs[0].(ui.UserResolvedMsg)
	if !ok {
		t.Fatalf("msgs[0] = %T, want ui.UserResolvedMsg", msgs[0])
	}
	if userMsg.TeamID != "T1" || userMsg.UserID != "U1" || userMsg.DisplayName != "alice" || !userMsg.IsBot {
		t.Fatalf("user msg = %+v", userMsg)
	}
	dmMsg, ok := msgs[1].(ui.DMNameResolvedMsg)
	if !ok {
		t.Fatalf("msgs[1] = %T, want ui.DMNameResolvedMsg", msgs[1])
	}
	if dmMsg.ChannelID != "D1" || dmMsg.DisplayName != "alice" || !dmMsg.IsBot {
		t.Fatalf("dm msg = %+v", dmMsg)
	}
}

func TestResolveUnresolvedDMs_SkipsUnchangedIDs(t *testing.T) {
	unresolved := []UnresolvedDM{{ChannelID: "D1", UserID: "U1"}}
	called := false

	resolveUnresolvedDMs(
		unresolved,
		func(userID string) (string, bool) { return userID, false },
		func(userID string) { called = true },
		func(msg tea.Msg) { t.Fatalf("unexpected msg: %T", msg) },
		"T1",
	)

	if called {
		t.Fatal("markBot called on unchanged non-bot resolution")
	}
}

func TestSendWorkspaceUserMetadataUpdated_RebuildsClassification(t *testing.T) {
	db, err := cache.New(":memory:")
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	defer db.Close()
	if err := db.UpsertWorkspace(cache.Workspace{ID: "T1", Name: "Test"}); err != nil {
		t.Fatalf("UpsertWorkspace: %v", err)
	}
	if err := db.UpsertChannel(cache.Channel{ID: "D1", WorkspaceID: "T1", Name: "", Type: "dm", IsMember: true}); err != nil {
		t.Fatalf("UpsertChannel D1: %v", err)
	}
	if err := db.UpsertChannel(cache.Channel{ID: "G1", WorkspaceID: "T1", Name: "mpdm-alice--bob-1", Type: "group_dm", IsMember: true}); err != nil {
		t.Fatalf("UpsertChannel G1: %v", err)
	}

	wctx := &WorkspaceContext{
		TeamID:            "T1",
		UserNames:         newUserNameStore(map[string]string{"U1": "Helper Bot"}),
		UserNamesByHandle: newHandleNameStore(map[string]string{"alice": "Alice", "bob": "Robert"}),
		BotUserIDs:        newBotUserIDStore(map[string]bool{"U1": true}),
		LastVisitedByChannel: newLastVisitedStore(map[string]int64{
			"D1": 11,
			"G1": 22,
		}),
		Channels: []sidebar.ChannelItem{
			{ID: "D1", Name: "U1", Type: "dm", DMUserID: "U1"},
			{ID: "G1", Name: "alice, bob", Type: "group_dm"},
		},
		FinderItems: []channelfinder.Item{
			{ID: "D1", Name: "U1", Type: "dm", Joined: true, LastVisited: 11},
			{ID: "G1", Name: "alice, bob", Type: "group_dm", Joined: true, LastVisited: 22},
			{ID: "C9", Name: "random", Type: "channel", Joined: false, LastVisited: 33},
		},
	}
	joinedChannels, joinedFinderItems := rebuildWorkspaceUserMetadata(db, wctx, config.Config{})
	wctx.ReplaceJoinedMetadata(joinedChannels, joinedFinderItems)

	var msg tea.Msg
	sendWorkspaceUserMetadataUpdated(func(m tea.Msg) { msg = m }, wctx)

	bulk, ok := msg.(ui.WorkspaceUserMetadataUpdatedMsg)
	if !ok {
		t.Fatalf("msg = %T, want ui.WorkspaceUserMetadataUpdatedMsg", msg)
	}
	if bulk.TeamID != "T1" {
		t.Fatalf("TeamID = %q, want T1", bulk.TeamID)
	}
	channels := wctx.ChannelsSnapshot()
	if channels[0].Type != "app" || channels[0].Name != "Helper Bot" {
		t.Fatalf("DM channel = %+v, want type=app name=Helper Bot", channels[0])
	}
	if channels[1].Name != "Alice, Robert" {
		t.Fatalf("MPDM channel name = %q, want Alice, Robert", channels[1].Name)
	}
	finderItems := wctx.FinderItemsSnapshot()
	if finderItems[0].Type != "app" || finderItems[0].Name != "Helper Bot" {
		t.Fatalf("finder DM = %+v, want type=app name=Helper Bot", finderItems[0])
	}
	if finderItems[1].Name != "Alice, Robert" {
		t.Fatalf("finder MPDM name = %q, want Alice, Robert", finderItems[1].Name)
	}
	if finderItems[2].ID != "C9" || finderItems[2].Joined {
		t.Fatalf("browseable finder item changed unexpectedly: %+v", finderItems[2])
	}
}
