package main

import (
	"sync"
	"testing"

	"github.com/gammons/slk/internal/ui/channelfinder"
	"github.com/gammons/slk/internal/ui/sidebar"
)

func TestWorkspaceMetadataConcurrentMergePreservesJoinedAndBrowseable(t *testing.T) {
	joinedChannels := []sidebar.ChannelItem{{ID: "D1", Name: "Helper Bot", Type: "app", DMUserID: "U1"}}
	joinedFinder := []channelfinder.Item{{ID: "D1", Name: "Helper Bot", Type: "app", Joined: true, LastVisited: 11}}
	browseable := []channelfinder.Item{{ID: "C9", Name: "random", Type: "channel", Joined: false, LastVisited: 33}}

	for i := 0; i < 25; i++ {
		wctx := &WorkspaceContext{}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			wctx.ReplaceJoinedMetadata(joinedChannels, joinedFinder)
		}()
		go func() {
			defer wg.Done()
			wctx.MergeBrowseableFinderItems(browseable)
		}()
		wg.Wait()

		channels := wctx.ChannelsSnapshot()
		finder := wctx.FinderItemsSnapshot()
		if len(channels) != 1 || channels[0].ID != "D1" || channels[0].Type != "app" {
			t.Fatalf("channels = %+v, want D1/app", channels)
		}
		if len(finder) != 2 {
			t.Fatalf("finder len = %d, want 2 (%+v)", len(finder), finder)
		}
		byID := make(map[string]channelfinder.Item, len(finder))
		for _, item := range finder {
			byID[item.ID] = item
		}
		if item, ok := byID["D1"]; !ok || !item.Joined || item.Type != "app" {
			t.Fatalf("joined finder item = %+v, ok=%v, want D1 joined app", item, ok)
		}
		if item, ok := byID["C9"]; !ok || item.Joined || item.Type != "channel" {
			t.Fatalf("browseable finder item = %+v, ok=%v, want C9 non-joined channel", item, ok)
		}
	}
}

func TestRTMEventHandlerCurrentChannelMetaUsesWorkspaceState(t *testing.T) {
	wctx := &WorkspaceContext{}
	wctx.ReplaceJoinedMetadata(
		[]sidebar.ChannelItem{{ID: "D1", Name: "old", Type: "dm", DMUserID: "U1"}},
		[]channelfinder.Item{{ID: "D1", Name: "old", Type: "dm", Joined: true}},
	)
	h := &rtmEventHandler{
		wsCtx:         wctx,
		channelNames:  map[string]string{"D1": "stale"},
		channelTypes:  map[string]string{"D1": "channel"},
		workspaceName: "Test",
	}

	wctx.ReplaceJoinedMetadata(
		[]sidebar.ChannelItem{{ID: "D1", Name: "Helper Bot", Type: "app", DMUserID: "U1"}},
		[]channelfinder.Item{{ID: "D1", Name: "Helper Bot", Type: "app", Joined: true}},
	)

	name, channelType := h.currentChannelMeta("D1")
	if name != "Helper Bot" || channelType != "app" {
		t.Fatalf("currentChannelMeta = (%q, %q), want (Helper Bot, app)", name, channelType)
	}
}

func TestLastVisitedStoreConcurrentAccess(t *testing.T) {
	store := newLastVisitedStore(map[string]int64{"C1": 1})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				store.Set("C1", int64(worker*1000+j))
				store.Get("C1")
				_ = store.Snapshot()
			}
		}(i)
	}
	wg.Wait()
	if _, ok := store.Get("C1"); !ok {
		t.Fatal("last visited store lost C1")
	}
}
