package main

import (
	"sync"

	"github.com/gammons/slk/internal/ui/channelfinder"
	"github.com/gammons/slk/internal/ui/sidebar"
)

type lastVisitedStore struct {
	mu     sync.RWMutex
	visits map[string]int64
}

func newLastVisitedStore(visits map[string]int64) *lastVisitedStore {
	return &lastVisitedStore{visits: cloneInt64Map(visits)}
}

func cloneInt64Map(src map[string]int64) map[string]int64 {
	if len(src) == 0 {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneChannelItems(src []sidebar.ChannelItem) []sidebar.ChannelItem {
	if src == nil {
		return nil
	}
	out := make([]sidebar.ChannelItem, len(src))
	copy(out, src)
	return out
}

func cloneFinderItems(src []channelfinder.Item) []channelfinder.Item {
	if src == nil {
		return nil
	}
	out := make([]channelfinder.Item, len(src))
	copy(out, src)
	return out
}

func splitFinderItems(items []channelfinder.Item) (joined []channelfinder.Item, browseable []channelfinder.Item) {
	for _, item := range items {
		if item.Joined {
			joined = append(joined, item)
			continue
		}
		browseable = append(browseable, item)
	}
	return joined, browseable
}

func (s *lastVisitedStore) Get(channelID string) (int64, bool) {
	if s == nil || channelID == "" {
		return 0, false
	}
	s.mu.RLock()
	visit, ok := s.visits[channelID]
	s.mu.RUnlock()
	return visit, ok
}

func (s *lastVisitedStore) Set(channelID string, ts int64) {
	if s == nil || channelID == "" {
		return
	}
	s.mu.Lock()
	if s.visits == nil {
		s.visits = map[string]int64{}
	}
	s.visits[channelID] = ts
	s.mu.Unlock()
}

func (s *lastVisitedStore) Replace(visits map[string]int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.visits = cloneInt64Map(visits)
	s.mu.Unlock()
}

func (s *lastVisitedStore) Snapshot() map[string]int64 {
	if s == nil {
		return map[string]int64{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneInt64Map(s.visits)
}

func mergeFinderItems(joined []channelfinder.Item, browseable []channelfinder.Item) []channelfinder.Item {
	out := make([]channelfinder.Item, 0, len(joined)+len(browseable))
	seen := make(map[string]struct{}, len(joined)+len(browseable))
	for _, item := range joined {
		item.Joined = true
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item)
	}
	for _, item := range browseable {
		item.Joined = false
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item)
	}
	return out
}

func (wctx *WorkspaceContext) ChannelsSnapshot() []sidebar.ChannelItem {
	wctx.metadataMu.RLock()
	defer wctx.metadataMu.RUnlock()
	return cloneChannelItems(wctx.Channels)
}

func (wctx *WorkspaceContext) FinderItemsSnapshot() []channelfinder.Item {
	wctx.metadataMu.RLock()
	defer wctx.metadataMu.RUnlock()
	return cloneFinderItems(wctx.FinderItems)
}

func (wctx *WorkspaceContext) JoinedMetadataSnapshot() ([]sidebar.ChannelItem, []channelfinder.Item) {
	wctx.metadataMu.RLock()
	defer wctx.metadataMu.RUnlock()
	joined, _ := splitFinderItems(wctx.FinderItems)
	return cloneChannelItems(wctx.Channels), cloneFinderItems(joined)
}

func (wctx *WorkspaceContext) ReplaceJoinedMetadata(channels []sidebar.ChannelItem, joinedFinder []channelfinder.Item) {
	wctx.metadataMu.Lock()
	defer wctx.metadataMu.Unlock()
	_, browseable := splitFinderItems(wctx.FinderItems)
	wctx.Channels = cloneChannelItems(channels)
	wctx.FinderItems = mergeFinderItems(cloneFinderItems(joinedFinder), browseable)
}

func (wctx *WorkspaceContext) UpsertJoinedMetadata(item sidebar.ChannelItem, finder channelfinder.Item) {
	wctx.metadataMu.Lock()
	defer wctx.metadataMu.Unlock()

	replaced := false
	for i := range wctx.Channels {
		if wctx.Channels[i].ID == item.ID {
			wctx.Channels[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		wctx.Channels = append(wctx.Channels, item)
	}

	joined, browseable := splitFinderItems(wctx.FinderItems)
	finder.Joined = true
	replaced = false
	for i := range joined {
		if joined[i].ID == finder.ID {
			if finder.LastVisited == 0 {
				finder.LastVisited = joined[i].LastVisited
			}
			joined[i] = finder
			replaced = true
			break
		}
	}
	if !replaced {
		joined = append(joined, finder)
	}
	wctx.FinderItems = mergeFinderItems(joined, browseable)
}

func (wctx *WorkspaceContext) MergeBrowseableFinderItems(items []channelfinder.Item) {
	wctx.metadataMu.Lock()
	defer wctx.metadataMu.Unlock()

	joined, browseable := splitFinderItems(wctx.FinderItems)
	index := make(map[string]int, len(browseable))
	for i := range browseable {
		browseable[i].Joined = false
		index[browseable[i].ID] = i
	}
	for _, item := range items {
		item.Joined = false
		if i, ok := index[item.ID]; ok {
			browseable[i] = item
			continue
		}
		index[item.ID] = len(browseable)
		browseable = append(browseable, item)
	}
	wctx.FinderItems = mergeFinderItems(joined, browseable)
}

func (wctx *WorkspaceContext) MutateChannels(fn func([]sidebar.ChannelItem)) []sidebar.ChannelItem {
	wctx.metadataMu.Lock()
	defer wctx.metadataMu.Unlock()
	if fn != nil {
		fn(wctx.Channels)
	}
	return cloneChannelItems(wctx.Channels)
}

func (wctx *WorkspaceContext) ChannelLookup(channelID string) (name string, channelType string, ok bool) {
	if wctx == nil || channelID == "" {
		return "", "", false
	}
	wctx.metadataMu.RLock()
	defer wctx.metadataMu.RUnlock()
	for _, ch := range wctx.Channels {
		if ch.ID == channelID {
			return ch.Name, ch.Type, true
		}
	}
	for _, item := range wctx.FinderItems {
		if item.ID == channelID {
			return item.Name, item.Type, true
		}
	}
	return "", "", false
}
