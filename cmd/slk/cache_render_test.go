package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gammons/slk/internal/cache"
	uimessages "github.com/gammons/slk/internal/ui/messages"
	"github.com/gammons/slk/internal/ui/messages/blockkit"
	"github.com/slack-go/slack"
)

// newCacheForTest returns a fresh in-memory cache.DB seeded with a
// workspace row so foreign-key constraints on messages/reactions are
// satisfied.
func newCacheForTest(t *testing.T) *cache.DB {
	t.Helper()
	db, err := cache.New(":memory:")
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	if err := db.UpsertWorkspace(cache.Workspace{ID: "T1", Name: "Test", Domain: "test"}); err != nil {
		t.Fatalf("UpsertWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestLoadCachedMessagesEnrichesFromCache verifies that loadCachedMessages
// reconstructs MessageItems with full fidelity — including a file
// attachment carried in raw_json and a reaction with HasReacted set
// against the caller's selfUserID — when a channel has cached rows.
func TestLoadCachedMessagesEnrichesFromCache(t *testing.T) {
	db := newCacheForTest(t)

	const (
		channelID  = "C1"
		selfUserID = "USELF"
		msgTS      = "1700000001.000000"
	)

	// Build the upstream slack.Message we want to round-trip. It has
	// a single image file attached so we can assert the raw_json
	// reconstruction surfaces it as messages.Attachment.
	upstream := slack.Message{
		Msg: slack.Msg{
			Timestamp: msgTS,
			User:      "UAUTHOR",
			Text:      "hello with file",
			Files: []slack.File{{
				ID:        "F123",
				Name:      "screenshot.png",
				Title:     "Screenshot",
				Mimetype:  "image/png",
				Permalink: "https://team.slack.com/files/UAUTHOR/F123/screenshot.png",
				Thumb360:  "https://files.slack.com/files-tmb/.../thumb_360.png",
				Thumb360W: 360,
				Thumb360H: 240,
			}},
		},
	}
	rawBytes, err := json.Marshal(upstream)
	if err != nil {
		t.Fatalf("marshal upstream: %v", err)
	}

	if err := db.UpsertMessage(cache.Message{
		TS:          msgTS,
		ChannelID:   channelID,
		WorkspaceID: "T1",
		UserID:      "UAUTHOR",
		Text:        "hello with file",
		RawJSON:     string(rawBytes),
		CreatedAt:   1700000001,
	}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	// One reaction: thumbsup, count 2, includes selfUserID -> HasReacted=true.
	if err := db.UpsertReaction(msgTS, channelID, "thumbsup", []string{"UOTHER", selfUserID}, 2); err != nil {
		t.Fatalf("UpsertReaction: %v", err)
	}

	userNames := map[string]string{"UAUTHOR": "alice"}

	got := loadCachedMessages(db, selfUserID, channelID, userNames, "3:04 PM", nil)
	if got == nil {
		t.Fatal("loadCachedMessages returned nil; expected one message")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	mi := got[0]

	if mi.TS != msgTS {
		t.Errorf("TS: got %q, want %q", mi.TS, msgTS)
	}
	if mi.UserID != "UAUTHOR" {
		t.Errorf("UserID: got %q, want %q", mi.UserID, "UAUTHOR")
	}
	if mi.UserName != "alice" {
		t.Errorf("UserName: got %q, want %q", mi.UserName, "alice")
	}
	if mi.Text != "hello with file" {
		t.Errorf("Text: got %q", mi.Text)
	}
	if mi.Timestamp == "" {
		t.Errorf("Timestamp: expected non-empty formatted time")
	}

	// Reactions
	if len(mi.Reactions) != 1 {
		t.Fatalf("Reactions: got %d, want 1", len(mi.Reactions))
	}
	r := mi.Reactions[0]
	if r.Emoji != "thumbsup" || r.Count != 2 {
		t.Errorf("reaction: got %+v, want emoji=thumbsup count=2", r)
	}
	if !r.HasReacted {
		t.Errorf("reaction.HasReacted: got false; expected true (selfUserID %q in users list)", selfUserID)
	}

	// Attachments (from raw_json -> Files).
	if len(mi.Attachments) != 1 {
		t.Fatalf("Attachments: got %d, want 1", len(mi.Attachments))
	}
	a := mi.Attachments[0]
	if a.Kind != "image" {
		t.Errorf("attachment Kind: got %q, want %q", a.Kind, "image")
	}
	if a.Name != "Screenshot" {
		t.Errorf("attachment Name: got %q, want %q (Title preferred over filename)", a.Name, "Screenshot")
	}
	if a.FileID != "F123" {
		t.Errorf("attachment FileID: got %q, want %q", a.FileID, "F123")
	}
}

// TestLoadCachedMessagesReturnsNilOnEmptyChannel ensures cache misses
// return nil so callers can fall through to the network fetch path.
func TestLoadCachedMessagesReturnsNilOnEmptyChannel(t *testing.T) {
	db := newCacheForTest(t)

	got := loadCachedMessages(db, "USELF", "C-empty", map[string]string{}, "3:04 PM", nil)
	if got != nil {
		t.Errorf("expected nil for channel with no cached rows, got %d items", len(got))
	}
}

// TestLoadCachedMessagesHandlesMissingRawJSON ensures a row with an
// empty raw_json column renders text-only (no attachments / blocks /
// legacy attachments) without failing the whole load. This is the
// pre-Task-2 backfill case: messages persisted before raw_json was
// populated should still be reconstructable from the row's scalar
// columns.
func TestLoadCachedMessagesHandlesMissingRawJSON(t *testing.T) {
	db := newCacheForTest(t)

	const (
		channelID = "C2"
		msgTS     = "1700000010.000000"
	)
	if err := db.UpsertMessage(cache.Message{
		TS:          msgTS,
		ChannelID:   channelID,
		WorkspaceID: "T1",
		UserID:      "UAUTHOR",
		Text:        "legacy row, no raw_json",
		// RawJSON intentionally empty.
		CreatedAt: 1700000010,
	}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	got := loadCachedMessages(db, "USELF", channelID, map[string]string{"UAUTHOR": "alice"}, "3:04 PM", nil)
	if got == nil {
		t.Fatal("expected one cached message, got nil")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	mi := got[0]
	if mi.Text != "legacy row, no raw_json" {
		t.Errorf("Text: got %q", mi.Text)
	}
	if mi.UserName != "alice" {
		t.Errorf("UserName: got %q, want %q", mi.UserName, "alice")
	}
	if len(mi.Attachments) != 0 {
		t.Errorf("Attachments: got %d, want 0 for row without raw_json", len(mi.Attachments))
	}
	if len(mi.Blocks) != 0 {
		t.Errorf("Blocks: got %d, want 0", len(mi.Blocks))
	}
	if len(mi.LegacyAttachments) != 0 {
		t.Errorf("LegacyAttachments: got %d, want 0", len(mi.LegacyAttachments))
	}
}

// TestLoadCachedThreadRepliesEnrichesFromCache verifies that the
// thread-cache reader returns the parent + replies in chronological
// order, mirroring the channel reader's enrichment pattern.
func TestLoadCachedThreadRepliesEnrichesFromCache(t *testing.T) {
	db := newCacheForTest(t)

	// Parent + 2 replies, all in the same thread.
	for _, m := range []cache.Message{
		{TS: "100.0", ChannelID: "C1", WorkspaceID: "T1", UserID: "U1", Text: "parent", ThreadTS: "100.0", CreatedAt: 1},
		{TS: "101.0", ChannelID: "C1", WorkspaceID: "T1", UserID: "U2", Text: "reply 1", ThreadTS: "100.0", CreatedAt: 2},
		{TS: "102.0", ChannelID: "C1", WorkspaceID: "T1", UserID: "U1", Text: "reply 2", ThreadTS: "100.0", CreatedAt: 3},
	} {
		if err := db.UpsertMessage(m); err != nil {
			t.Fatal(err)
		}
	}

	items := loadCachedThreadReplies(db, "USELF", "C1", "100.0", nil, "3:04 PM", nil)
	if len(items) != 3 {
		t.Fatalf("want 3 thread items, got %d", len(items))
	}
	if items[0].Text != "parent" || items[2].Text != "reply 2" {
		t.Errorf("unexpected ordering: %+v", items)
	}
}

func TestLoadCachedMessagesRestoresTableBlocks(t *testing.T) {
	db := newCacheForTest(t)

	const (
		channelID = "C3"
		msgTS     = "1700000100.000000"
	)
	upstream := slack.Message{
		Msg: slack.Msg{
			Timestamp: msgTS,
			User:      "UAUTHOR",
			Text:      "table payload",
			Blocks: slack.Blocks{BlockSet: []slack.Block{slack.NewTableBlock("tbl").
				WithColumnSettings(
					slack.ColumnSetting{Align: slack.ColumnAlignmentRight},
					slack.ColumnSetting{Align: slack.ColumnAlignmentCenter, IsWrapped: true},
					slack.ColumnSetting{Align: slack.ColumnAlignmentLeft},
				).
				AddRow(
					slack.NewTableRichTextCell(slack.NewRichTextSection(slack.NewRichTextSectionTextElement("Service", &slack.RichTextSectionTextStyle{Bold: true}))),
					slack.NewTableRichTextCell(slack.NewRichTextSection(slack.NewRichTextSectionTextElement("Count", &slack.RichTextSectionTextStyle{Bold: true}))),
					slack.NewTableRichTextCell(slack.NewRichTextSection(slack.NewRichTextSectionTextElement("Rate", &slack.RichTextSectionTextStyle{Bold: true}))),
				).AddRow(
				slack.NewTableRawTextCell("Builds"),
				slack.NewTableRawNumberCell(42.5),
				slack.NewTableRawNumberCell(0.125).WithText("12.5%"),
			)}},
		},
	}
	rawBytes, err := json.Marshal(upstream)
	if err != nil {
		t.Fatalf("marshal upstream: %v", err)
	}
	if err := db.UpsertMessage(cache.Message{
		TS:          msgTS,
		ChannelID:   channelID,
		WorkspaceID: "T1",
		UserID:      "UAUTHOR",
		Text:        "table payload",
		RawJSON:     string(rawBytes),
		CreatedAt:   1700000100,
	}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	got := loadCachedMessages(db, "USELF", channelID, map[string]string{"UAUTHOR": "alice"}, "3:04 PM", nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	table, ok := got[0].Blocks[0].(blockkit.TableBlock)
	if !ok {
		t.Fatalf("block type = %T, want blockkit.TableBlock", got[0].Blocks[0])
	}
	if table.BlockID != "tbl" {
		t.Fatalf("BlockID = %q, want %q", table.BlockID, "tbl")
	}
	if len(table.Rows) != 2 || len(table.Rows[0]) != 3 || len(table.Rows[1]) != 3 {
		t.Fatalf("rows = %#v", table.Rows)
	}
	if table.Rows[0][0].Text != "*Service*" || table.Rows[0][1].Text != "*Count*" || table.Rows[0][2].Text != "*Rate*" {
		t.Fatalf("header rows = %#v", table.Rows[0])
	}
	if table.Rows[1][0].Text != "Builds" || table.Rows[1][1].Text != "42.5" || table.Rows[1][2].Text != "12.5%" {
		t.Fatalf("table rows = %#v", table.Rows)
	}
	if table.Columns[0].Align != blockkit.TableAlignRight || table.Columns[0].Wrapped {
		t.Fatalf("column[0] = %+v", table.Columns[0])
	}
	if table.Columns[1].Align != blockkit.TableAlignCenter || !table.Columns[1].Wrapped {
		t.Fatalf("column[1] = %+v", table.Columns[1])
	}
	if table.Columns[2].Align != blockkit.TableAlignLeft || table.Columns[2].Wrapped {
		t.Fatalf("column[2] = %+v", table.Columns[2])
	}
}

func TestLoadCachedMessagesRichTextUserGroupResolvesWithSnapshot(t *testing.T) {
	db := newCacheForTest(t)

	const (
		channelID = "C4"
		msgTS     = "1700000200.000000"
	)
	upstream := slack.Message{Msg: slack.Msg{
		Timestamp: msgTS,
		User:      "UAUTHOR",
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			slack.NewRichTextBlock("rt",
				slack.NewRichTextSection(
					slack.NewRichTextSectionTextElement("team ", nil),
					slack.NewRichTextSectionUserGroupElement("S123"),
				),
			),
		}},
	}}
	rawBytes, err := json.Marshal(upstream)
	if err != nil {
		t.Fatalf("marshal upstream: %v", err)
	}
	if err := db.UpsertMessage(cache.Message{
		TS:          msgTS,
		ChannelID:   channelID,
		WorkspaceID: "T1",
		UserID:      "UAUTHOR",
		Text:        "team <!subteam^S123>",
		RawJSON:     string(rawBytes),
		CreatedAt:   1700000200,
	}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	got := loadCachedMessages(db, "USELF", channelID, map[string]string{"UAUTHOR": "alice"}, "3:04 PM", nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	rt, ok := got[0].Blocks[0].(blockkit.RichTextBlock)
	if !ok {
		t.Fatalf("block type = %T, want blockkit.RichTextBlock", got[0].Blocks[0])
	}
	mrkdwn := blockkit.RichTextToMrkdwn(rt)
	if mrkdwn != "team <!subteam^S123>" {
		t.Fatalf("RichTextToMrkdwn = %q, want team <!subteam^S123>", mrkdwn)
	}
	rendered := uimessages.RenderSlackMarkdownWith(mrkdwn, uimessages.RenderSlackMarkdownOpts{
		UserGroupNames: map[string]string{"S123": "eng"},
	})
	if !strings.Contains(rendered, "@eng") {
		t.Fatalf("rendered = %q, want @eng", rendered)
	}
	if strings.Contains(rendered, "<!subteam^") {
		t.Fatalf("rendered leaked raw token: %q", rendered)
	}
}
