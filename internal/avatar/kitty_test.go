package avatar

import (
	"bytes"
	"image"
	imgcolor "image/color"
	imgpng "image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	imgpkg "github.com/gammons/slk/internal/image"
)

// TestRender_KittyAvatarUploadsAndPlaceholders asserts that, when the
// cache is configured for kitty rendering, PreloadSync writes a kitty
// graphics upload escape (\x1b_G ... \x1b\\) to the side-channel
// writer and stores a render string composed of unicode-placeholder
// cells (U+10EEEE) at the avatar's 4x2 cell footprint.
//
// We don't assert on byte-exact contents — kitty rendering depends on
// the registry's auto-assigned image ID, which varies between test
// runs — but we do verify the structural shape:
//   - the upload escape appears on the kitty side channel,
//   - the rendered string contains the placeholder rune,
//   - the rendered string spans exactly AvatarRows lines.
func TestRender_KittyAvatarUploadsAndPlaceholders(t *testing.T) {
	t.Setenv("TMUX", "")
	src := image.NewRGBA(image.Rect(0, 0, 32, 32))
	state := uint32(1)
	next := func() uint8 {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		return uint8(state)
	}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			src.Set(x, y, imgcolor.RGBA{
				R: next(),
				G: next(),
				B: next(),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	imgpng.Encode(&buf, src)
	pngBytes := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
	defer srv.Close()

	cache, err := imgpkg.NewCache(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	fetcher := imgpkg.NewFetcher(cache, http.DefaultClient)

	// Capture the kitty side channel for assertions. The renderer
	// writes the upload escape directly to imgpkg.KittyOutput
	// because lipgloss/bubbletea strip APC sequences embedded in
	// rendered strings.
	saved := imgpkg.KittyOutput
	defer func() { imgpkg.KittyOutput = saved }()
	sideCh := &writeCapture{}
	imgpkg.KittyOutput = imgpkg.SerializeOutput(sideCh)

	kitty := imgpkg.NewKittyRenderer(imgpkg.NewRegistry())
	c := NewCache(fetcher, kitty, true)
	c.PreloadSync("U_KITTY", srv.URL)
	got := c.Get("U_KITTY")

	if got == "" {
		t.Fatal("expected non-empty kitty avatar render")
	}
	lines := strings.Split(got, "\n")
	if len(lines) != AvatarRows {
		t.Fatalf("expected %d lines, got %d", AvatarRows, len(lines))
	}
	for i, line := range lines {
		if strings.Count(line, string(imgpkg.PlaceholderRune)) != AvatarCols {
			t.Fatalf("line %d placeholder cells = %d, want %d", i, strings.Count(line, string(imgpkg.PlaceholderRune)), AvatarCols)
		}
	}
	if len(sideCh.writes) != 1 {
		t.Fatalf("kitty upload writes = %d, want 1", len(sideCh.writes))
	}
	upload := string(sideCh.writes[0])
	if !strings.HasPrefix(upload, "\x1b_G") {
		t.Errorf("expected kitty graphics upload (\\e_G) on side channel, got %d bytes starting with %q",
			len(sideCh.writes[0]), upload[:min(20, len(upload))])
	}
	if strings.Count(upload, "\x1b_G") < 2 {
		t.Fatalf("expected multi-chunk kitty upload, got %d chunks", strings.Count(upload, "\x1b_G"))
	}
	if !strings.Contains(upload, "c=4") || !strings.Contains(upload, "r=2") {
		t.Fatalf("expected 4x2 kitty placement header, got %q", upload)
	}
	if !strings.Contains(upload, "U=1") {
		t.Error("expected U=1 (unicode placeholder) in upload escape")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type writeCapture struct {
	writes [][]byte
}

func (w *writeCapture) Write(p []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), p...))
	return len(p), nil
}
