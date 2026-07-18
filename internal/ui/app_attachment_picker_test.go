package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gammons/slk/internal/ui/attachmentpicker"
	"github.com/gammons/slk/internal/ui/compose"
	"github.com/gammons/slk/internal/ui/messages"
	"golang.design/x/clipboard"
)

func loadAppPicker(t *testing.T, a *App, directory string, reserved int, excluded []string) {
	t.Helper()
	cmd := a.attachmentPicker.Open(directory, reserved, excluded)
	if cmd == nil {
		t.Fatal("picker Open returned nil command")
	}
	msg, ok := cmd().(attachmentpicker.DirectoryLoadedMsg)
	if !ok {
		t.Fatalf("picker command returned unexpected message")
	}
	a.attachmentPicker.Apply(msg)
}

func TestCtrlOOpensPickerWithoutChangingDraft(t *testing.T) {
	a := newTestAppWithMessages(t)
	a.activeChannelID = "C1"
	a.focusedPanel = PanelMessages
	a.compose.SetValue("draft text")
	a.SetMode(ModeInsert)

	cmd := handleInsertMode(a, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+O returned nil directory-load command")
	}
	if a.mode != ModeAttachmentPicker || a.attachmentTarget != PanelMessages || !a.attachmentPicker.IsVisible() {
		t.Fatalf("picker state: mode=%v target=%v visible=%v", a.mode, a.attachmentTarget, a.attachmentPicker.IsVisible())
	}
	if got := a.compose.Value(); got != "draft text" {
		t.Fatalf("draft changed: %q", got)
	}
}

func TestAttachmentPickerCancelRestoresOriginatingCompose(t *testing.T) {
	a := newTestAppWithMessages(t)
	a.activeChannelID = "C1"
	a.focusedPanel = PanelMessages
	a.attachmentTarget = PanelMessages
	a.SetMode(ModeAttachmentPicker)
	loadAppPicker(t, a, t.TempDir(), 0, nil)

	_ = handleAttachmentPickerMode(a, tea.KeyPressMsg{Code: tea.KeyEscape})
	if a.mode != ModeInsert || a.focusedPanel != PanelMessages || a.attachmentPicker.IsVisible() {
		t.Fatalf("cancel state: mode=%v panel=%v visible=%v", a.mode, a.focusedPanel, a.attachmentPicker.IsVisible())
	}
}

func TestAttachmentPickerAddsMultipleFilesToChannelCompose(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestAppWithMessages(t)
	a.activeChannelID = "C1"
	a.attachmentTarget = PanelMessages
	a.SetMode(ModeAttachmentPicker)
	loadAppPicker(t, a, dir, 0, nil)
	_, _ = a.attachmentPicker.HandleKey("space", a.height)
	_, _ = a.attachmentPicker.HandleKey("j", a.height)
	_, _ = a.attachmentPicker.HandleKey("space", a.height)

	_ = handleAttachmentPickerMode(a, tea.KeyPressMsg{Code: 'a'})
	attachments := a.compose.Attachments()
	if len(attachments) != 2 {
		t.Fatalf("attachments = %d, want 2", len(attachments))
	}
	if a.mode != ModeInsert || a.attachmentPicker.IsVisible() {
		t.Fatalf("attach state: mode=%v visible=%v", a.mode, a.attachmentPicker.IsVisible())
	}
}

func TestAttachmentPickerRoutesToThreadCompose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thread.txt")
	if err := os.WriteFile(path, []byte("thread"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestAppWithMessages(t)
	a.threadVisible = true
	a.threadPanel.SetThread(messages.MessageItem{TS: "1.0"}, nil, "C1", "1.0")
	a.attachmentTarget = PanelThread
	a.SetMode(ModeAttachmentPicker)
	loadAppPicker(t, a, dir, 0, nil)
	_, _ = a.attachmentPicker.HandleKey("space", a.height)

	_ = handleAttachmentPickerMode(a, tea.KeyPressMsg{Code: 'a'})
	if len(a.threadCompose.Attachments()) != 1 || len(a.compose.Attachments()) != 0 {
		t.Fatalf("channel=%d thread=%d", len(a.compose.Attachments()), len(a.threadCompose.Attachments()))
	}
}

func TestAttachmentPickerCountsExistingAttachments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestAppWithMessages(t)
	a.activeChannelID = "C1"
	for i := 0; i < maxPendingAttachments; i++ {
		a.compose.AddAttachment(compose.PendingAttachment{Filename: "existing", Bytes: []byte("x"), Size: 1})
	}
	loadAppPicker(t, a, dir, len(a.compose.Attachments()), nil)
	_, _ = a.attachmentPicker.HandleKey("space", a.height)
	if a.attachmentPicker.SelectedCount() != 0 || a.attachmentPicker.Error() != "Maximum 10 attachments" {
		t.Fatalf("count=%d error=%q", a.attachmentPicker.SelectedCount(), a.attachmentPicker.Error())
	}
}

func TestAttachmentPickerConsumesClicksInsideModal(t *testing.T) {
	a := newTestAppWithMessages(t)
	a.activeChannelID = "C1"
	a.attachmentTarget = PanelMessages
	a.SetMode(ModeAttachmentPicker)
	loadAppPicker(t, a, t.TempDir(), 0, nil)
	w, h := a.attachmentPicker.BoxSize(a.width, a.height)
	x := (a.width-w)/2 + 1
	y := (a.height-h)/2 + 1

	_ = reduceModalClick(a, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if a.mode != ModeAttachmentPicker || !a.attachmentPicker.IsVisible() {
		t.Fatalf("inside click dismissed picker: mode=%v visible=%v", a.mode, a.attachmentPicker.IsVisible())
	}
}

func TestPendingAttachmentFromPathValidatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(path, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachment, err := pendingAttachmentFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Path != path || attachment.Filename != "report.pdf" || attachment.Size != 3 {
		t.Fatalf("unexpected attachment: %#v", attachment)
	}
	if _, err := pendingAttachmentFromPath(dir); err == nil {
		t.Fatal("directory should be rejected")
	}
}

func TestSmartPasteInvalidPathStillFallsBackToText(t *testing.T) {
	a := newTestAppWithMessages(t)
	a.SetClipboardAvailable(true)
	a.SetClipboardReader(func(format clipboard.Format) []byte {
		if format == clipboard.FmtText {
			return []byte("/path/that/does/not/exist")
		}
		return nil
	})
	a.smartPaste()
	if got := a.compose.Value(); got != "/path/that/does/not/exist" {
		t.Fatalf("invalid path fallback = %q", got)
	}
}
