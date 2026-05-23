// internal/ui/services.go
//
// Service interfaces that group cohesive subsets of the App's
// collaborator callbacks. Wired by cmd/slk/main.go.
//
// Phase 3 of the SOLID refactor of internal/ui/app.go: introduces
// service interfaces (DIP + ISP) to replace the flat collection of
// XxxFunc callback fields that previously hung off App. Each interface
// groups related callbacks under one collaborator; App holds a single
// pointer per service instead of N raw functions.
//
// Migration strategy: one service per commit, smallest first. Each
// commit converts a related subset of XxxFunc fields + Set* methods
// to a single ServiceXxx interface + Set method. The XxxFunc type
// aliases stay alive as constructor parameter types (documentation
// value) and adapter input types until all services have migrated.
package ui

import "github.com/gammons/slk/internal/ui/reactionpicker"

// ReactionService is the App's interface to the Slack reaction API
// and the user's recent-emoji-use history (frecency). Implementations
// are wired by cmd/slk/main.go.
//
// All methods are best-effort and nil-safe at the adapter level: an
// implementation built via NewReactionService with a nil component
// silently no-ops that operation.
type ReactionService interface {
	// Add adds emoji to messageTS in channelID. Returns an error if
	// the Slack API call fails; App turns that into a status-bar toast.
	Add(channelID, messageTS, emoji string) error

	// Remove removes the current user's emoji reaction from messageTS
	// in channelID.
	Remove(channelID, messageTS, emoji string) error

	// LoadFrecent returns up to limit emoji entries from the user's
	// recent-use history, ordered by frecency. May return nil; the
	// reaction picker handles an empty slice as "no recents yet".
	LoadFrecent(limit int) []reactionpicker.EmojiEntry

	// RecordFrecent records emoji as recently used so future
	// LoadFrecent calls surface it. Called after every successful
	// reaction add.
	RecordFrecent(emoji string)
}

// NewReactionService builds a ReactionService from individual
// function closures. Any function may be nil; the resulting service
// no-ops that operation and returns the zero value for read paths.
// Used by both cmd/slk/main.go (production wiring) and tests (fake
// closures).
func NewReactionService(
	add ReactionAddFunc,
	remove ReactionRemoveFunc,
	loadFrecent FrecentLoadFunc,
	recordFrecent FrecentRecordFunc,
) ReactionService {
	return reactionAdapter{
		add:           add,
		remove:        remove,
		loadFrecent:   loadFrecent,
		recordFrecent: recordFrecent,
	}
}

// noopReactionService is the default ReactionService wired into App
// by NewApp so call sites can dispatch without nil-checks even when
// no service has been registered (typically in tests that don't
// exercise reaction paths).
var noopReactionService ReactionService = reactionAdapter{}

type reactionAdapter struct {
	add           ReactionAddFunc
	remove        ReactionRemoveFunc
	loadFrecent   FrecentLoadFunc
	recordFrecent FrecentRecordFunc
}

func (r reactionAdapter) Add(channelID, messageTS, emoji string) error {
	if r.add == nil {
		return nil
	}
	return r.add(channelID, messageTS, emoji)
}

func (r reactionAdapter) Remove(channelID, messageTS, emoji string) error {
	if r.remove == nil {
		return nil
	}
	return r.remove(channelID, messageTS, emoji)
}

func (r reactionAdapter) LoadFrecent(limit int) []reactionpicker.EmojiEntry {
	if r.loadFrecent == nil {
		return nil
	}
	return r.loadFrecent(limit)
}

func (r reactionAdapter) RecordFrecent(emoji string) {
	if r.recordFrecent == nil {
		return
	}
	r.recordFrecent(emoji)
}
