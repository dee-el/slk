package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/gammons/slk/internal/ui/attachmentpicker"
)

var reduceAttachmentPicker reducerFunc = func(a *App, msg tea.Msg) (tea.Cmd, bool) {
	loaded, ok := msg.(attachmentpicker.DirectoryLoadedMsg)
	if !ok {
		return nil, false
	}
	a.attachmentPicker.Apply(loaded)
	return nil, true
}
