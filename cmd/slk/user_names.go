package main

import "sync"

type userNameStore struct {
	mu    sync.RWMutex
	names map[string]string
}

type externalUserStore struct {
	mu    sync.RWMutex
	users map[string]bool
}

type handleNameStore struct {
	mu    sync.RWMutex
	names map[string]string
}

type botUserIDStore struct {
	mu  sync.RWMutex
	ids map[string]struct{}
}

func newUserNameStore(names map[string]string) *userNameStore {
	return &userNameStore{names: cloneStringMap(names)}
}

func newExternalUserStore(users map[string]bool) *externalUserStore {
	return &externalUserStore{users: cloneBoolMap(users)}
}

func newHandleNameStore(names map[string]string) *handleNameStore {
	return &handleNameStore{names: cloneStringMap(names)}
}

func newBotUserIDStore(ids map[string]bool) *botUserIDStore {
	store := &botUserIDStore{ids: map[string]struct{}{}}
	store.Merge(ids)
	return store
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	if len(src) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (s *userNameStore) Get(id string) (string, bool) {
	if s == nil || id == "" {
		return "", false
	}
	s.mu.RLock()
	name, ok := s.names[id]
	s.mu.RUnlock()
	return name, ok
}

func (s *userNameStore) Set(id, name string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	if s.names == nil {
		s.names = map[string]string{}
	}
	s.names[id] = name
	s.mu.Unlock()
}

func (s *userNameStore) Merge(names map[string]string) {
	if s == nil || len(names) == 0 {
		return
	}
	s.mu.Lock()
	if s.names == nil {
		s.names = map[string]string{}
	}
	for id, name := range names {
		s.names[id] = name
	}
	s.mu.Unlock()
}

func (s *userNameStore) MergeIfUnchanged(names map[string]string, baseline map[string]string) {
	if s == nil || len(names) == 0 {
		return
	}
	s.mu.Lock()
	if s.names == nil {
		s.names = map[string]string{}
	}
	for id, name := range names {
		current, currentOK := s.names[id]
		base, baseOK := baseline[id]
		if currentOK != baseOK || current != base {
			continue
		}
		s.names[id] = name
	}
	s.mu.Unlock()
}

func (s *userNameStore) Snapshot() map[string]string {
	if s == nil {
		return map[string]string{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneStringMap(s.names)
}

func (s *externalUserStore) Set(id string, external bool) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	if s.users == nil {
		s.users = map[string]bool{}
	}
	if external {
		s.users[id] = true
	} else {
		delete(s.users, id)
	}
	s.mu.Unlock()
}

func (s *externalUserStore) Snapshot() map[string]bool {
	if s == nil {
		return map[string]bool{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneBoolMap(s.users)
}

func (s *externalUserStore) ReplaceIfUnchanged(users map[string]bool, baseline map[string]bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.users == nil {
		s.users = map[string]bool{}
	}
	keys := make(map[string]struct{}, len(users)+len(baseline))
	for id := range users {
		keys[id] = struct{}{}
	}
	for id := range baseline {
		keys[id] = struct{}{}
	}
	for id := range keys {
		current := s.users[id]
		base := baseline[id]
		if current != base {
			continue
		}
		if users[id] {
			s.users[id] = true
		} else {
			delete(s.users, id)
		}
	}
	s.mu.Unlock()
}

func (s *handleNameStore) Get(handle string) (string, bool) {
	if s == nil || handle == "" {
		return "", false
	}
	s.mu.RLock()
	name, ok := s.names[handle]
	s.mu.RUnlock()
	return name, ok
}

func (s *handleNameStore) Set(handle, name string) {
	if s == nil || handle == "" {
		return
	}
	s.mu.Lock()
	if s.names == nil {
		s.names = map[string]string{}
	}
	s.names[handle] = name
	s.mu.Unlock()
}

func (s *handleNameStore) Merge(names map[string]string) {
	if s == nil || len(names) == 0 {
		return
	}
	s.mu.Lock()
	if s.names == nil {
		s.names = map[string]string{}
	}
	for handle, name := range names {
		s.names[handle] = name
	}
	s.mu.Unlock()
}

func (s *handleNameStore) Snapshot() map[string]string {
	if s == nil {
		return map[string]string{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneStringMap(s.names)
}

func (s *botUserIDStore) Has(id string) bool {
	if s == nil || id == "" {
		return false
	}
	s.mu.RLock()
	_, ok := s.ids[id]
	s.mu.RUnlock()
	return ok
}

func (s *botUserIDStore) Set(id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	if s.ids == nil {
		s.ids = map[string]struct{}{}
	}
	s.ids[id] = struct{}{}
	s.mu.Unlock()
}

func (s *botUserIDStore) Merge(ids map[string]bool) {
	if s == nil || len(ids) == 0 {
		return
	}
	s.mu.Lock()
	if s.ids == nil {
		s.ids = map[string]struct{}{}
	}
	for id, isBot := range ids {
		if isBot {
			s.ids[id] = struct{}{}
		}
	}
	s.mu.Unlock()
}

func (s *botUserIDStore) Snapshot() map[string]bool {
	if s == nil {
		return map[string]bool{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.ids))
	for id := range s.ids {
		out[id] = true
	}
	return out
}
