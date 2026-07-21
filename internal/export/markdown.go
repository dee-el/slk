// Package export converts slk message data into portable file formats.
// It has no UI dependencies (no lipgloss, no bubbletea) and operates
// entirely on the messages.MessageItem data model.
package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	emojiutil "github.com/gammons/slk/internal/emoji"
	"github.com/gammons/slk/internal/ui/messages"
)

// ThreadToMarkdown converts a parent message and its replies into a
// CommonMark markdown string. userNames and channelNames resolve
// @mentions and #channel references in message bodies.
func ThreadToMarkdown(parent messages.MessageItem, replies []messages.MessageItem, userNames, channelNames map[string]string) string {
	return ThreadToMarkdownWithUserGroups(parent, replies, userNames, channelNames, nil)
}

// ThreadToMarkdownWithUserGroups is the user-group-aware form of
// ThreadToMarkdown.
func ThreadToMarkdownWithUserGroups(parent messages.MessageItem, replies []messages.MessageItem, userNames, channelNames, userGroupNames map[string]string) string {
	var b strings.Builder
	b.WriteString(formatMessageWithUserGroups(parent, userNames, channelNames, userGroupNames))
	for _, r := range replies {
		b.WriteString(formatMessageWithUserGroups(r, userNames, channelNames, userGroupNames))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func formatMessage(msg messages.MessageItem, userNames, channelNames map[string]string) string {
	return formatMessageWithUserGroups(msg, userNames, channelNames, nil)
}

func formatMessageWithUserGroups(msg messages.MessageItem, userNames, channelNames, userGroupNames map[string]string) string {
	var b strings.Builder

	b.WriteString("**" + msg.UserName + "**")
	b.WriteString(" — ")
	b.WriteString(msg.DateStr + " " + msg.Timestamp)
	b.WriteByte('\n')

	body := messages.SlackMrkdwnToCommonMarkWith(messages.MessageTextSource(msg), messages.CommonMarkOpts{
		UserNames:      userNames,
		ChannelNames:   channelNames,
		UserGroupNames: userGroupNames,
	})
	b.WriteString(body)
	b.WriteByte('\n')

	if len(msg.Attachments) > 0 {
		for _, att := range msg.Attachments {
			label := att.Name
			if label == "" {
				if att.Kind == "image" {
					label = "Image"
				} else {
					label = "File"
				}
			}
			b.WriteString("[" + label + "](" + att.URL + ")\n")
		}
	}

	if len(msg.Reactions) > 0 {
		parts := make([]string, 0, len(msg.Reactions))
		for _, r := range msg.Reactions {
			e := emojiutil.Sprint(":" + emojiutil.StripSkinTone(r.Emoji) + ":")
			parts = append(parts, fmt.Sprintf("%s %d", strings.TrimSpace(e), r.Count))
		}
		b.WriteString(strings.Join(parts, "  "))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	return b.String()
}

// ExportDir returns the default directory for saved exports,
// honoring XDG_DATA_HOME. Creates nothing — callers must MkdirAll.
func ExportDir() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "slk", "exports"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "slk", "exports"), nil
}
