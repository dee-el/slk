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
//
// Constructor shape:
//   - Services with ≤4 methods take positional func args
//     (NewReactionService(add, remove, loadFrecent, recordFrecent)).
//   - Services with ≥5 methods take a struct of named funcs
//     (NewThreadService(ThreadServiceFuncs{Fetch: fn, Mark: fn, ...})).
//     Lets tests omit unused methods without trailing nils and lets
//     readers see what each closure is doing at the call site.
package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/gammons/slk/internal/ui/messages"
	"github.com/gammons/slk/internal/ui/reactionpicker"
)

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

// ThreadService is the App's interface to Slack's thread surfaces:
// fetching replies, marking threads read, posting replies, and loading
// the involved-threads list for the user's threads view. Includes
// ChannelLastRead because the thread panel needs the parent channel's
// last_read_ts to render its unread boundary — that's a thread-display
// concern even though the data is channel-scoped.
//
// Implementations are wired by cmd/slk/main.go. Build one via
// NewThreadService from a ThreadServiceFuncs struct so unused
// methods can be left nil without trailing positional nils.
type ThreadService interface {
	// Fetch retrieves replies for threadTS in channelID from Slack.
	// Returns a tea.Msg (typically ThreadRepliesLoadedMsg).
	Fetch(channelID, threadTS string) tea.Msg

	// CacheRead returns cached replies (or nil) so the thread panel
	// can populate without waiting for the network. A non-empty
	// return causes immediate render; the subsequent Fetch result
	// overwrites with authoritative data.
	CacheRead(channelID, threadTS string) []messages.MessageItem

	// Mark marks the thread as read on Slack's servers
	// (subscriptions.thread.mark). channelID is the parent channel,
	// threadTS is the parent message ts, ts is the latest reply ts
	// the user has now seen. Best-effort and non-blocking.
	Mark(channelID, threadTS, ts string)

	// SendReply posts a reply to threadTS in channelID. Returns a
	// tea.Msg (typically ThreadReplySentMsg or ThreadReplySendFailedMsg).
	SendReply(channelID, threadTS, text string) tea.Msg

	// ListFetch loads the involved-threads list for the workspace
	// (Slack subscriptions.list). Returns a tea.Msg (typically
	// ThreadsListLoadedMsg).
	ListFetch(teamID string) tea.Msg

	// ChannelLastRead returns the parent channel's last_read_ts so
	// the thread panel can render a "── new ──" boundary. Optional;
	// returning "" disables the unread boundary in the thread panel.
	ChannelLastRead(channelID string) string
}

// ThreadServiceFuncs is the closure bundle accepted by
// NewThreadService. Any field may be nil; the resulting service
// no-ops that operation (and returns the zero value for read paths).
type ThreadServiceFuncs struct {
	Fetch           ThreadFetchFunc
	CacheRead       ThreadCacheReadFunc
	Mark            ThreadMarkFunc
	SendReply       ThreadReplySendFunc
	ListFetch       ThreadsListFetchFunc
	ChannelLastRead func(channelID string) string
}

// NewThreadService builds a ThreadService from a ThreadServiceFuncs
// bundle. Used by both cmd/slk/main.go (production wiring) and tests
// (fake closures).
func NewThreadService(fns ThreadServiceFuncs) ThreadService {
	return threadAdapter{fns: fns}
}

// noopThreadService is the default ThreadService wired into App by
// NewApp so call sites can dispatch without nil-checks even when
// SetThreadService hasn't been called.
var noopThreadService ThreadService = threadAdapter{}

type threadAdapter struct {
	fns ThreadServiceFuncs
}

func (t threadAdapter) Fetch(channelID, threadTS string) tea.Msg {
	if t.fns.Fetch == nil {
		return nil
	}
	return t.fns.Fetch(channelID, threadTS)
}

func (t threadAdapter) CacheRead(channelID, threadTS string) []messages.MessageItem {
	if t.fns.CacheRead == nil {
		return nil
	}
	return t.fns.CacheRead(channelID, threadTS)
}

func (t threadAdapter) Mark(channelID, threadTS, ts string) {
	if t.fns.Mark == nil {
		return
	}
	t.fns.Mark(channelID, threadTS, ts)
}

func (t threadAdapter) SendReply(channelID, threadTS, text string) tea.Msg {
	if t.fns.SendReply == nil {
		return nil
	}
	return t.fns.SendReply(channelID, threadTS, text)
}

func (t threadAdapter) ListFetch(teamID string) tea.Msg {
	if t.fns.ListFetch == nil {
		return nil
	}
	return t.fns.ListFetch(teamID)
}

func (t threadAdapter) ChannelLastRead(channelID string) string {
	if t.fns.ChannelLastRead == nil {
		return ""
	}
	return t.fns.ChannelLastRead(channelID)
}

// MessageService is the App's interface to Slack's per-message
// operations: send, edit, delete, mark-unread, and permalink lookup.
// Implementations are wired by cmd/slk/main.go.
//
// All methods are best-effort and nil-safe at the adapter level: an
// implementation built via NewMessageService with a nil component
// silently no-ops that operation (returning nil tea.Msg or
// ("", nil) for Permalink).
type MessageService interface {
	// Send dispatches chat.postMessage for channelID with text.
	// Returns a tea.Msg (typically MessageSentMsg or
	// MessageSendFailedMsg).
	Send(channelID, text string) tea.Msg

	// Edit dispatches chat.update for the message identified by
	// (channelID, ts), replacing its text with newText.
	// Returns a tea.Msg (typically MessageEditedMsg).
	Edit(channelID, ts, newText string) tea.Msg

	// Delete dispatches chat.delete for the message identified by
	// (channelID, ts). Returns a tea.Msg (typically MessageDeletedMsg).
	Delete(channelID, ts string) tea.Msg

	// MarkUnread dispatches conversations.mark (channel-level) or
	// subscriptions.thread.mark (when threadTS != "") with the
	// rolled-back boundaryTS. unreadCount is forwarded to the result
	// for the sidebar's badge update. Returns a tea.Msg (typically
	// MessageMarkedUnreadMsg).
	MarkUnread(channelID, threadTS, boundaryTS string, unreadCount int) tea.Msg

	// Permalink resolves the Slack permalink URL for the message
	// identified by (channelID, ts). Used by the copy-permalink
	// keybind. Synchronous (HTTP); callers wrap in a goroutine to
	// avoid blocking the Update loop.
	Permalink(ctx context.Context, channelID, ts string) (string, error)
}

// MessageServiceFuncs is the closure bundle accepted by
// NewMessageService. Any field may be nil; the resulting service
// no-ops that operation.
type MessageServiceFuncs struct {
	Send       MessageSendFunc
	Edit       MessageEditFunc
	Delete     MessageDeleteFunc
	MarkUnread MarkUnreadFunc
	Permalink  PermalinkFetchFunc
}

// NewMessageService builds a MessageService from a MessageServiceFuncs
// bundle. Used by cmd/slk/main.go (production wiring) and tests.
func NewMessageService(fns MessageServiceFuncs) MessageService {
	return messageAdapter{fns: fns}
}

// noopMessageService is the default MessageService wired into App by
// NewApp so call sites can dispatch without nil-checks even when
// SetMessageService hasn't been called.
var noopMessageService MessageService = messageAdapter{}

type messageAdapter struct {
	fns MessageServiceFuncs
}

func (m messageAdapter) Send(channelID, text string) tea.Msg {
	if m.fns.Send == nil {
		return nil
	}
	return m.fns.Send(channelID, text)
}

func (m messageAdapter) Edit(channelID, ts, newText string) tea.Msg {
	if m.fns.Edit == nil {
		return nil
	}
	return m.fns.Edit(channelID, ts, newText)
}

func (m messageAdapter) Delete(channelID, ts string) tea.Msg {
	if m.fns.Delete == nil {
		return nil
	}
	return m.fns.Delete(channelID, ts)
}

func (m messageAdapter) MarkUnread(channelID, threadTS, boundaryTS string, unreadCount int) tea.Msg {
	if m.fns.MarkUnread == nil {
		return nil
	}
	return m.fns.MarkUnread(channelID, threadTS, boundaryTS, unreadCount)
}

func (m messageAdapter) Permalink(ctx context.Context, channelID, ts string) (string, error) {
	if m.fns.Permalink == nil {
		return "", nil
	}
	return m.fns.Permalink(ctx, channelID, ts)
}
