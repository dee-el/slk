package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gammons/slk/internal/ui"
	"github.com/slack-go/slack"
)

func TestUserGroupNameStoreSetNormalizesHandle(t *testing.T) {
	store := newUserGroupNameStore(nil)
	store.Set(" S1 ", " @eng ")
	store.Set("S2", "@@ops")

	if got, ok := store.Get("S1"); !ok || got != "eng" {
		t.Fatalf("Get(S1) = %q, %v; want eng, true", got, ok)
	}
	if got, ok := store.Get("S2"); !ok || got != "@ops" {
		t.Fatalf("Get(S2) = %q, %v; want @ops, true", got, ok)
	}
}

func TestUserGroupNameStoreIgnoresEmptyIDOrHandle(t *testing.T) {
	store := newUserGroupNameStore(map[string]string{"S1": "eng"})
	store.Set("", "ops")
	store.Set("S2", "   ")
	store.Set("S3", "@")

	got := store.Snapshot()
	if len(got) != 1 || got["S1"] != "eng" {
		t.Fatalf("snapshot = %#v, want only S1=eng", got)
	}
}

func TestUserGroupNameStoreReplaceFiltersAndNormalizes(t *testing.T) {
	store := newUserGroupNameStore(map[string]string{"S1": "eng", "S2": "ops"})
	store.Replace(map[string]string{
		" S3 ": " @platform ",
		"":     "ignored",
		"S4":   "   ",
	})

	got := store.Snapshot()
	if len(got) != 1 {
		t.Fatalf("len(snapshot) = %d, want 1 (%#v)", len(got), got)
	}
	if got["S3"] != "platform" {
		t.Fatalf("S3 = %q, want platform", got["S3"])
	}
	if _, ok := got["S1"]; ok {
		t.Fatal("Replace did not replace previous contents")
	}
}

func TestUserGroupNameStoreSnapshotImmutable(t *testing.T) {
	store := newUserGroupNameStore(map[string]string{"S1": "eng"})

	snapshot := store.Snapshot()
	snapshot["S1"] = "changed"
	snapshot["S2"] = "ops"

	if got, _ := store.Get("S1"); got != "eng" {
		t.Fatalf("store S1 = %q, want eng", got)
	}
	if _, ok := store.Get("S2"); ok {
		t.Fatal("snapshot mutation leaked into store")
	}
}

func TestUserGroupNameStoreConcurrentAccess(t *testing.T) {
	store := newUserGroupNameStore(nil)

	const workers = 8
	const iterations = 200

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				id := fmt.Sprintf("S%d", j%16)
				store.Set(id, fmt.Sprintf("@group-%d-%d", worker, j))
				store.Get(id)
				_ = store.Snapshot()
				if j%25 == 0 {
					store.Replace(map[string]string{id: fmt.Sprintf(" @replaced-%d-%d ", worker, j)})
				}
			}
		}(i)
	}
	wg.Wait()

	if len(store.Snapshot()) == 0 {
		t.Fatal("store stayed empty after concurrent writes")
	}
}

func TestRefreshWorkspaceUserGroupsSuccess(t *testing.T) {
	store := newUserGroupNameStore(map[string]string{"S0": "old"})
	called := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := refreshWorkspaceUserGroups(ctx, func(ctx context.Context) ([]slack.UserGroup, error) {
		called++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("refresh context missing deadline")
		}
		return []slack.UserGroup{{ID: " S1 ", Handle: " @eng "}, {ID: "", Handle: "skip"}}, nil
	}, store)
	if err != nil {
		t.Fatalf("refreshWorkspaceUserGroups: %v", err)
	}
	if called != 1 {
		t.Fatalf("fetch called %d times, want 1", called)
	}
	if len(snapshot) != 1 || snapshot["S1"] != "eng" {
		t.Fatalf("snapshot = %#v, want S1=eng", snapshot)
	}
	if got := store.Snapshot(); len(got) != 1 || got["S1"] != "eng" {
		t.Fatalf("store = %#v, want S1=eng", got)
	}
}

func TestRefreshWorkspaceUserGroupsFailurePreservesOldMap(t *testing.T) {
	store := newUserGroupNameStore(map[string]string{"S0": "old"})
	_, err := refreshWorkspaceUserGroups(context.Background(), func(context.Context) ([]slack.UserGroup, error) {
		return nil, errors.New("nope")
	}, store)
	if err == nil {
		t.Fatal("refreshWorkspaceUserGroups err = nil, want failure")
	}
	if got := store.Snapshot(); len(got) != 1 || got["S0"] != "old" {
		t.Fatalf("store = %#v, want preserved S0=old", got)
	}
}

func TestSendWorkspaceUserGroupUpdateUsesDedicatedMsg(t *testing.T) {
	var got tea.Msg
	sendWorkspaceUserGroupUpdate(func(msg tea.Msg) { got = msg }, "T1")
	update, ok := got.(ui.WorkspaceUserGroupsUpdatedMsg)
	if !ok {
		t.Fatalf("msg = %T, want ui.WorkspaceUserGroupsUpdatedMsg", got)
	}
	if update.TeamID != "T1" {
		t.Fatalf("TeamID = %q, want T1", update.TeamID)
	}
}

func TestFormatNotificationBodyKnownUserGroup(t *testing.T) {
	got := formatNotificationBody("alice", "paging <!subteam^S123>", nil, map[string]string{"S123": "eng"})
	if got != "alice: paging @eng" {
		t.Fatalf("body = %q, want %q", got, "alice: paging @eng")
	}
}

func TestFormatNotificationBodyUnknownUserGroupFallsBack(t *testing.T) {
	got := formatNotificationBody("alice", "paging <!subteam^S999>", nil, nil)
	if got != "alice: paging @group" {
		t.Fatalf("body = %q, want %q", got, "alice: paging @group")
	}
}

func TestTriggerUserGroupRefreshSuccessUpdatesStoreAndSends(t *testing.T) {
	store := newUserGroupNameStore(map[string]string{"S0": "old"})
	sent := make(chan tea.Msg, 1)
	h := &rtmEventHandler{
		workspaceID:          "T1",
		userGroupNames:       store,
		userGroupRefreshGate: dedupeGate{window: time.Hour},
		isActive:             func() bool { return true },
		sendMsg:              func(msg tea.Msg) { sent <- msg },
		getUserGroups: func(ctx context.Context) ([]slack.UserGroup, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("triggerUserGroupRefresh context missing deadline")
			}
			return []slack.UserGroup{{ID: "S1", Handle: " @eng "}}, nil
		},
	}

	if !h.triggerUserGroupRefresh("reconnect") {
		t.Fatal("triggerUserGroupRefresh returned false, want true")
	}

	select {
	case msg := <-sent:
		update, ok := msg.(ui.WorkspaceUserGroupsUpdatedMsg)
		if !ok {
			t.Fatalf("msg = %T, want ui.WorkspaceUserGroupsUpdatedMsg", msg)
		}
		if update.TeamID != "T1" {
			t.Fatalf("TeamID = %q, want T1", update.TeamID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for refresh update")
	}

	if got := store.Snapshot(); len(got) != 1 || got["S1"] != "eng" {
		t.Fatalf("store = %#v, want replaced S1=eng", got)
	}
}

func TestTriggerUserGroupRefreshFailurePreservesStore(t *testing.T) {
	store := newUserGroupNameStore(map[string]string{"S0": "old"})
	done := make(chan struct{})
	sent := make(chan tea.Msg, 1)
	h := &rtmEventHandler{
		workspaceID:          "T1",
		userGroupNames:       store,
		userGroupRefreshGate: dedupeGate{window: time.Hour},
		isActive:             func() bool { return true },
		sendMsg:              func(msg tea.Msg) { sent <- msg },
		getUserGroups: func(ctx context.Context) ([]slack.UserGroup, error) {
			defer close(done)
			return nil, errors.New("boom")
		},
	}

	if !h.triggerUserGroupRefresh("reconnect") {
		t.Fatal("triggerUserGroupRefresh returned false, want true")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed refresh")
	}

	if got := store.Snapshot(); len(got) != 1 || got["S0"] != "old" {
		t.Fatalf("store = %#v, want preserved S0=old", got)
	}
	select {
	case msg := <-sent:
		t.Fatalf("unexpected send on failure: %T", msg)
	default:
	}
}
