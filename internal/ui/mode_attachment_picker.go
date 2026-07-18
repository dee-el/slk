package ui

import (
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/gammons/slk/internal/ui/attachmentpicker"
	"github.com/gammons/slk/internal/ui/compose"
)

func (a *App) openAttachmentPicker() tea.Cmd {
	if a.editing.IsActive() {
		return a.uploadToastCmd("Cannot attach files while editing", 2*time.Second)
	}
	target, panel, ok := a.attachmentCompose()
	if !ok {
		return a.uploadToastCmd("Cannot attach: no active compose", 2*time.Second)
	}
	attachments := target.Attachments()
	excluded := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.Path != "" {
			excluded = append(excluded, attachment.Path)
		}
	}
	startDirectory := a.attachmentPicker.LastDirectory()
	if startDirectory == "" {
		startDirectory, _ = os.UserHomeDir()
	}
	a.attachmentTarget = panel
	a.SetMode(ModeAttachmentPicker)
	return a.attachmentPicker.Open(startDirectory, len(attachments), excluded)
}

func handleAttachmentPickerMode(a *App, msg tea.KeyMsg) tea.Cmd {
	action, cmd := a.attachmentPicker.HandleKey(msg.String(), a.height)
	switch action {
	case attachmentpicker.ActionCancel:
		return a.restoreAttachmentCompose()
	case attachmentpicker.ActionAttach:
		return tea.Batch(cmd, a.attachSelectedFiles())
	default:
		return cmd
	}
}

func (a *App) attachSelectedFiles() tea.Cmd {
	target, ok := a.composeForPanel(a.attachmentTarget)
	if !ok {
		a.attachmentPicker.SetError(fmt.Errorf("originating compose is no longer available"))
		return nil
	}
	paths := a.attachmentPicker.SelectedPaths()
	if len(target.Attachments())+len(paths) > maxPendingAttachments {
		a.attachmentPicker.SetError(fmt.Errorf("maximum %d attachments", maxPendingAttachments))
		return nil
	}
	attachments := make([]compose.PendingAttachment, 0, len(paths))
	for _, path := range paths {
		attachment, err := pendingAttachmentFromPath(path)
		if err != nil {
			a.attachmentPicker.SetError(err)
			return nil
		}
		attachments = append(attachments, attachment)
	}
	for _, attachment := range attachments {
		target.AddAttachment(attachment)
	}
	a.attachmentPicker.Close()
	focus := a.restoreAttachmentCompose()
	return tea.Batch(
		focus,
		a.uploadToastCmd(fmt.Sprintf("Attached %d files", len(attachments)), 2*time.Second),
	)
}

func (a *App) restoreAttachmentCompose() tea.Cmd {
	a.SetMode(ModeInsert)
	target, ok := a.composeForPanel(a.attachmentTarget)
	if !ok {
		a.SetMode(ModeNormal)
		return nil
	}
	a.focusedPanel = a.attachmentTarget
	return target.Focus()
}

func (a *App) attachmentCompose() (*compose.Model, Panel, bool) {
	if a.focusedPanel == PanelThread && a.threadVisible {
		return &a.threadCompose, PanelThread, true
	}
	if a.activeChannelID == "" {
		return nil, PanelMessages, false
	}
	return &a.compose, PanelMessages, true
}

func (a *App) composeForPanel(panel Panel) (*compose.Model, bool) {
	switch panel {
	case PanelThread:
		if !a.threadVisible || a.threadPanel.ChannelID() == "" {
			return nil, false
		}
		return &a.threadCompose, true
	case PanelMessages:
		if a.activeChannelID == "" {
			return nil, false
		}
		return &a.compose, true
	default:
		return nil, false
	}
}
