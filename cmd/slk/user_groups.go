package main

import (
	"context"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gammons/slk/internal/notify"
	slackclient "github.com/gammons/slk/internal/slack"
	"github.com/gammons/slk/internal/ui"
	"github.com/slack-go/slack"
)

const workspaceUserGroupFetchTimeout = 10 * time.Second

type userGroupNameStore struct {
	mu    sync.RWMutex
	names map[string]string
}

func newUserGroupNameStore(names map[string]string) *userGroupNameStore {
	store := &userGroupNameStore{}
	store.Replace(names)
	return store
}

func normalizeUserGroupID(id string) string {
	return strings.TrimSpace(id)
}

func normalizeUserGroupHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	handle = strings.TrimPrefix(handle, "@")
	return strings.TrimSpace(handle)
}

func (s *userGroupNameStore) Get(id string) (string, bool) {
	if s == nil {
		return "", false
	}
	id = normalizeUserGroupID(id)
	if id == "" {
		return "", false
	}
	s.mu.RLock()
	name, ok := s.names[id]
	s.mu.RUnlock()
	return name, ok
}

func (s *userGroupNameStore) Set(id, handle string) {
	if s == nil {
		return
	}
	id = normalizeUserGroupID(id)
	handle = normalizeUserGroupHandle(handle)
	if id == "" || handle == "" {
		return
	}
	s.mu.Lock()
	if s.names == nil {
		s.names = map[string]string{}
	}
	s.names[id] = handle
	s.mu.Unlock()
}

func (s *userGroupNameStore) Replace(names map[string]string) {
	if s == nil {
		return
	}
	normalized := map[string]string{}
	for id, handle := range names {
		id = normalizeUserGroupID(id)
		handle = normalizeUserGroupHandle(handle)
		if id == "" || handle == "" {
			continue
		}
		normalized[id] = handle
	}
	s.mu.Lock()
	s.names = normalized
	s.mu.Unlock()
}

func (s *userGroupNameStore) Snapshot() map[string]string {
	if s == nil {
		return map[string]string{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneStringMap(s.names)
}

func userGroupNamesFromList(groups []slack.UserGroup) map[string]string {
	if len(groups) == 0 {
		return map[string]string{}
	}
	names := make(map[string]string, len(groups))
	for _, group := range groups {
		id := normalizeUserGroupID(group.ID)
		if id == "" || normalizeUserGroupHandle(group.Handle) == "" {
			continue
		}
		names[id] = group.Handle
	}
	return names
}

func refreshWorkspaceUserGroups(
	ctx context.Context,
	fetch func(context.Context) ([]slack.UserGroup, error),
	store *userGroupNameStore,
) (map[string]string, error) {
	if fetch == nil || store == nil {
		return map[string]string{}, nil
	}
	groups, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	names := userGroupNamesFromList(groups)
	store.Replace(names)
	return store.Snapshot(), nil
}

func formatNotificationBody(senderName, text string, userNames, userGroupNames map[string]string) string {
	return senderName + ": " + notify.StripSlackMarkupWithUserGroups(text, userNames, userGroupNames)
}

func sendWorkspaceUserGroupUpdate(send func(tea.Msg), teamID string) {
	if send == nil || teamID == "" {
		return
	}
	send(ui.WorkspaceUserGroupsUpdatedMsg{TeamID: teamID})
}

func (h *rtmEventHandler) userGroupStore() *userGroupNameStore {
	if h == nil {
		return nil
	}
	if h.userGroupNames != nil {
		return h.userGroupNames
	}
	if h.wsCtx != nil {
		return h.wsCtx.UserGroupNames
	}
	return nil
}

func (h *rtmEventHandler) applyUserGroupChanged(group slack.UserGroup) bool {
	store := h.userGroupStore()
	if store == nil {
		return false
	}
	id := normalizeUserGroupID(group.ID)
	if id == "" {
		return false
	}
	teamID := strings.TrimSpace(group.TeamID)
	if teamID != "" && h.workspaceID != "" && teamID != h.workspaceID {
		return false
	}
	if normalizeUserGroupHandle(group.Handle) == "" {
		return false
	}
	store.Set(id, group.Handle)
	return h.isActive == nil || h.isActive()
}

func (h *rtmEventHandler) OnUserGroupChanged(group slack.UserGroup) {
	if !h.applyUserGroupChanged(group) {
		return
	}
	sendWorkspaceUserGroupUpdate(h.send, h.workspaceID)
}

var _ slackclient.EventHandler = (*rtmEventHandler)(nil)
