package debuglog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnabled_DefaultFalse(t *testing.T) {
	if Enabled() {
		t.Fatalf("Enabled() should be false before Init")
	}
}

func TestInit_TruncatesExisting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("SLK_DEBUG", "1")

	// Pre-populate slk-debug.log with content.
	preexisting := filepath.Join(dir, "slk-debug.log")
	if err := os.WriteFile(preexisting, []byte("old content from a previous session"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Reset the package-level flag so this test is order-independent.
	enabled.Store(false)

	f, err := Init()
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if f == nil {
		t.Fatalf("Init returned nil file when SLK_DEBUG was set")
	}
	defer f.Close()

	info, err := os.Stat(preexisting)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("slk-debug.log should be truncated, got size %d", info.Size())
	}
	if !Enabled() {
		t.Fatalf("Enabled() should be true after Init with SLK_DEBUG set")
	}
}

func TestInit_NoFileWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Explicitly unset SLK_DEBUG (defensive — env may bleed in from CI).
	t.Setenv("SLK_DEBUG", "")
	enabled.Store(false)

	f, err := Init()
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if f != nil {
		t.Fatalf("Init should return nil file when SLK_DEBUG is unset, got %v", f.Name())
		_ = f.Close()
	}
	if Enabled() {
		t.Fatalf("Enabled() should be false after Init with SLK_DEBUG unset")
	}

	if _, err := os.Stat(filepath.Join(dir, "slk-debug.log")); !os.IsNotExist(err) {
		t.Fatalf("slk-debug.log should not exist when disabled, got err=%v", err)
	}
}
