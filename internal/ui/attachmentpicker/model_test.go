package attachmentpicker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openForTest(t *testing.T, m *Model, directory string, reserved int, excluded []string) {
	t.Helper()
	cmd := m.Open(directory, reserved, excluded)
	if cmd == nil {
		t.Fatal("Open returned nil command")
	}
	msg, ok := cmd().(DirectoryLoadedMsg)
	if !ok {
		t.Fatalf("Open command returned unexpected message")
	}
	m.Apply(msg)
}

func TestOpenSortsDirectoriesBeforeFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "z-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "A.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := New(10, 10*1024*1024)
	openForTest(t, m, dir, 0, nil)
	items := m.Items()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if !items[0].IsDir || items[0].Name != "z-dir" || items[1].Name != "A.txt" || items[2].Name != "b.txt" {
		t.Fatalf("unexpected sort order: %#v", items)
	}
}

func TestMultiSelectPreservesToggleOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := New(10, 10*1024*1024)
	openForTest(t, m, dir, 0, nil)

	_, _ = m.HandleKey("j", 30)
	_, _ = m.HandleKey("space", 30)
	_, _ = m.HandleKey("k", 30)
	_, _ = m.HandleKey("space", 30)
	action, _ := m.HandleKey("a", 30)
	if action != ActionAttach {
		t.Fatalf("action = %v, want ActionAttach", action)
	}
	paths := m.SelectedPaths()
	if len(paths) != 2 || filepath.Base(paths[0]) != "b.txt" || filepath.Base(paths[1]) != "a.txt" {
		t.Fatalf("selection order = %#v", paths)
	}

	_, _ = m.HandleKey("space", 30)
	if got := m.SelectedPaths(); len(got) != 1 || filepath.Base(got[0]) != "b.txt" {
		t.Fatalf("selection after toggle = %#v", got)
	}
}

func TestSelectionLimitsAndExcludedPaths(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.txt")
	second := filepath.Join(dir, "b.txt")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m := New(10, 10*1024*1024)
	openForTest(t, m, dir, 9, []string{first})
	_, _ = m.HandleKey("space", 30)
	if m.SelectedCount() != 0 || m.Error() != "File already attached" {
		t.Fatalf("excluded select: count=%d error=%q", m.SelectedCount(), m.Error())
	}
	_, _ = m.HandleKey("j", 30)
	_, _ = m.HandleKey("space", 30)
	if m.SelectedCount() != 1 {
		t.Fatalf("selected count = %d, want 1", m.SelectedCount())
	}
	_, _ = m.HandleKey("k", 30)
	_, _ = m.HandleKey("space", 30)
	if m.SelectedCount() != 1 || m.Error() != "File already attached" {
		t.Fatalf("limit/excluded state: count=%d error=%q", m.SelectedCount(), m.Error())
	}
}

func TestRejectsEmptyAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(10, 4)
	openForTest(t, m, dir, 0, nil)

	_, _ = m.HandleKey("space", 30)
	if m.Error() != "Empty file" {
		t.Fatalf("empty error = %q", m.Error())
	}
	_, _ = m.HandleKey("j", 30)
	_, _ = m.HandleKey("space", 30)
	if m.Error() != "File too large (>10 MB limit)" {
		t.Fatalf("large error = %q", m.Error())
	}
}

func TestStaleDirectoryResultIsIgnored(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	if err := os.WriteFile(filepath.Join(first, "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(10, 10*1024*1024)
	oldCmd := m.Open(first, 0, nil)
	newCmd := m.load(second)
	newMsg := newCmd().(DirectoryLoadedMsg)
	oldMsg := oldCmd().(DirectoryLoadedMsg)
	m.Apply(newMsg)
	m.Apply(oldMsg)

	items := m.Items()
	if len(items) != 1 || items[0].Name != "new.txt" {
		t.Fatalf("stale result replaced current items: %#v", items)
	}
}

func TestRenderMarksSelectedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(10, 10*1024*1024)
	openForTest(t, m, dir, 0, nil)
	_, _ = m.HandleKey("space", 30)

	view := m.renderBox(100, 30)
	for _, want := range []string{"Attach files", "[x]", "report.pdf", "1/10 selected"} {
		if !strings.Contains(view, want) {
			t.Fatalf("render missing %q:\n%s", want, view)
		}
	}
}
