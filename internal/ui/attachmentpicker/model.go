// Package attachmentpicker provides the multi-select file picker used by the
// message composer.
package attachmentpicker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Action reports a picker-level action to the owning UI mode.
type Action int

const (
	ActionNone Action = iota
	ActionCancel
	ActionAttach
)

// Item is one directory entry.
type Item struct {
	Name  string
	Path  string
	IsDir bool
	Size  int64
}

// DirectoryLoadedMsg carries one asynchronous directory read back to Model.
// Fields stay private because callers only need to route the message to Apply.
type DirectoryLoadedMsg struct {
	generation uint64
	directory  string
	items      []Item
	err        error
}

// Model owns picker navigation and multi-selection state.
type Model struct {
	visible          bool
	loading          bool
	currentDirectory string
	lastDirectory    string
	items            []Item
	cursor           int
	selected         map[string]Item
	selectedOrder    []string
	excluded         map[string]struct{}
	maxSelected      int
	maxFileSize      int64
	reservedCount    int
	errText          string
	readGeneration   uint64
}

// New creates a hidden picker with selection and file-size limits.
func New(maxSelected int, maxFileSize int64) *Model {
	return &Model{
		maxSelected: maxSelected,
		maxFileSize: maxFileSize,
		selected:    make(map[string]Item),
		excluded:    make(map[string]struct{}),
	}
}

// Open resets transient selection and asynchronously reads directory.
func (m *Model) Open(directory string, reservedCount int, excludedPaths []string) tea.Cmd {
	m.visible = true
	m.items = nil
	m.cursor = 0
	m.selected = make(map[string]Item)
	m.selectedOrder = nil
	m.excluded = make(map[string]struct{}, len(excludedPaths))
	for _, path := range excludedPaths {
		if path == "" {
			continue
		}
		m.excluded[canonicalPath(path)] = struct{}{}
	}
	m.reservedCount = reservedCount
	m.errText = ""

	if directory == "" {
		directory = m.lastDirectory
	}
	if directory == "" {
		if home, err := os.UserHomeDir(); err == nil {
			directory = home
		} else {
			directory = "."
		}
	}
	return m.load(directory)
}

// Close hides the picker while retaining the last successful directory.
func (m *Model) Close() {
	m.visible = false
	m.loading = false
	m.items = nil
	m.cursor = 0
	m.selected = make(map[string]Item)
	m.selectedOrder = nil
	m.excluded = make(map[string]struct{})
	m.errText = ""
}

// IsVisible reports whether the picker is open.
func (m *Model) IsVisible() bool { return m.visible }

// LastDirectory returns the most recent successfully loaded directory.
func (m *Model) LastDirectory() string { return m.lastDirectory }

// CurrentDirectory returns the directory currently displayed or loading.
func (m *Model) CurrentDirectory() string { return m.currentDirectory }

// Items returns a copy of current directory entries.
func (m *Model) Items() []Item { return append([]Item(nil), m.items...) }

// Cursor returns the highlighted row index.
func (m *Model) Cursor() int { return m.cursor }

// Error returns the visible picker error.
func (m *Model) Error() string { return m.errText }

// SetError shows an error without closing the picker.
func (m *Model) SetError(err error) {
	if err == nil {
		m.errText = ""
		return
	}
	m.errText = err.Error()
}

// SelectedPaths returns selected files in toggle order.
func (m *Model) SelectedPaths() []string {
	paths := make([]string, 0, len(m.selectedOrder))
	for _, key := range m.selectedOrder {
		if item, ok := m.selected[key]; ok {
			paths = append(paths, item.Path)
		}
	}
	return paths
}

// SelectedCount returns the number of files selected in this picker session.
func (m *Model) SelectedCount() int { return len(m.selected) }

// Apply installs an asynchronous directory result. Stale results are ignored.
func (m *Model) Apply(msg DirectoryLoadedMsg) {
	if msg.generation != m.readGeneration || !m.visible {
		return
	}
	m.loading = false
	if msg.err != nil {
		m.items = nil
		m.cursor = 0
		m.errText = "Cannot open directory: " + msg.err.Error()
		return
	}
	m.currentDirectory = msg.directory
	m.lastDirectory = msg.directory
	m.items = msg.items
	m.cursor = 0
	m.errText = ""
}

// HandleKey processes one normalized Bubble Tea key string.
func (m *Model) HandleKey(key string, termHeight int) (Action, tea.Cmd) {
	if !m.visible {
		return ActionNone, nil
	}
	page := visibleRows(termHeight)
	switch key {
	case "esc":
		m.Close()
		return ActionCancel, nil
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g":
		m.cursor = 0
	case "G":
		if len(m.items) > 0 {
			m.cursor = len(m.items) - 1
		}
	case "pgup":
		m.move(-page)
	case "pgdown":
		m.move(page)
	case "h", "left", "backspace":
		parent := filepath.Dir(m.currentDirectory)
		if parent != "" && parent != m.currentDirectory {
			return ActionNone, m.load(parent)
		}
	case "l", "right", "enter":
		if item, ok := m.highlighted(); ok && item.IsDir {
			return ActionNone, m.load(item.Path)
		}
	case "space", " ":
		m.toggleHighlighted()
	case "a":
		if len(m.selected) == 0 {
			m.errText = "Select at least one file"
			return ActionNone, nil
		}
		return ActionAttach, nil
	}
	return ActionNone, nil
}

func (m *Model) load(directory string) tea.Cmd {
	abs, err := filepath.Abs(directory)
	if err != nil {
		m.errText = "Cannot resolve directory: " + err.Error()
		return nil
	}
	directory = filepath.Clean(abs)
	m.readGeneration++
	generation := m.readGeneration
	m.currentDirectory = directory
	m.loading = true
	m.errText = ""
	return func() tea.Msg {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return DirectoryLoadedMsg{generation: generation, directory: directory, err: err}
		}
		items := make([]Item, 0, len(entries))
		for _, entry := range entries {
			path := filepath.Join(directory, entry.Name())
			info, statErr := os.Stat(path)
			if statErr != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
				continue
			}
			items = append(items, Item{
				Name:  entry.Name(),
				Path:  path,
				IsDir: info.IsDir(),
				Size:  info.Size(),
			})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].IsDir != items[j].IsDir {
				return items[i].IsDir
			}
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		})
		return DirectoryLoadedMsg{generation: generation, directory: directory, items: items}
	}
}

func (m *Model) highlighted() (Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return Item{}, false
	}
	return m.items[m.cursor], true
}

func (m *Model) move(delta int) {
	if len(m.items) == 0 {
		m.cursor = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
}

func (m *Model) toggleHighlighted() {
	item, ok := m.highlighted()
	if !ok || item.IsDir {
		return
	}
	key := canonicalPath(item.Path)
	if _, exists := m.selected[key]; exists {
		delete(m.selected, key)
		for i, selectedKey := range m.selectedOrder {
			if selectedKey == key {
				m.selectedOrder = append(m.selectedOrder[:i], m.selectedOrder[i+1:]...)
				break
			}
		}
		m.errText = ""
		return
	}
	if _, exists := m.excluded[key]; exists {
		m.errText = "File already attached"
		return
	}
	if m.reservedCount+len(m.selected) >= m.maxSelected {
		m.errText = fmt.Sprintf("Maximum %d attachments", m.maxSelected)
		return
	}
	if item.Size == 0 {
		m.errText = "Empty file"
		return
	}
	if item.Size > m.maxFileSize {
		m.errText = "File too large (>10 MB limit)"
		return
	}
	f, err := os.Open(item.Path)
	if err != nil {
		m.errText = "Cannot read file: " + err.Error()
		return
	}
	_ = f.Close()
	m.selected[key] = item
	m.selectedOrder = append(m.selectedOrder, key)
	m.errText = ""
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}
