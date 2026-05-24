// internal/ui/reducer_reactions.go
//
// Reaction-family reducer for App.Update (Phase 4g).
//
// Owns the three Update arms for reaction WS echoes / API results:
//
//   ReactionAddedMsg    - server confirmed a reaction was added.
//                         WS echoes of our own optimistic updates
//                         are filtered by currentUserID; remote
//                         users' reactions are merged into the
//                         message's reaction list.
//   ReactionRemovedMsg  - server confirmed a reaction was removed
//                         (same WS-echo dedup as Added).
//   ReactionSentMsg     - our reaction API call completed. No-op
//                         today: the optimistic update is already
//                         on screen and a failed call has no surface.
//
// Free reducer (no dedicated controller) because reactions are a
// per-message annotation with no cross-message invariant and no
// in-flight state to track. The update helper itself
// (updateReactionOnMessage) stays on App because it touches
// messagepane / threadPanel caches.
package ui

import (
	tea "charm.land/bubbletea/v2"
)

var reduceReactions reducerFunc = func(a *App, msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case ReactionAddedMsg:
		// Skip WebSocket echo of our own optimistic updates: when
		// we add a reaction the UI updates immediately; the echo
		// arrives later with our own userID and is dropped.
		if m.UserID != a.currentUserID {
			a.updateReactionOnMessage(m.ChannelID, m.MessageTS, m.Emoji, m.UserID, false)
		}
		return nil, true

	case ReactionRemovedMsg:
		if m.UserID != a.currentUserID {
			a.updateReactionOnMessage(m.ChannelID, m.MessageTS, m.Emoji, m.UserID, true)
		}
		return nil, true

	case ReactionSentMsg:
		_ = m
		// API call completed. Optimistic update is already on
		// screen; a failed call has no user surface today.
		return nil, true
	}
	return nil, false
}
