package image

import (
	"bytes"
	"errors"
	"image"
	imgcolor "image/color"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKitty_UploadEscapeFormat(t *testing.T) {
	t.Setenv("TMUX", "")
	src := makeSolid(64, 64, imgcolor.RGBA{1, 2, 3, 255})
	r := NewKittyRenderer(NewRegistry())
	out := r.Render(src, image.Pt(10, 5))

	if out.OnFlush == nil {
		t.Fatal("expected OnFlush set on first render")
	}
	if out.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	var buf bytes.Buffer
	if err := out.OnFlush(&buf); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.HasPrefix(s, "\x1b_G") {
		t.Errorf("expected \\e_G prefix, got %q", s[:minInt(20, len(s))])
	}
	if !strings.HasSuffix(s, "\x1b\\") {
		t.Errorf("expected \\e\\ suffix")
	}
	if !strings.Contains(s, "a=T") {
		t.Error("missing a=T (transmit-and-display, required for unicode-placeholder virtual placement)")
	}
	if !strings.Contains(s, "c=10") || !strings.Contains(s, "r=5") {
		t.Error("missing c=<cols>,r=<rows> for virtual placement footprint")
	}
	if !strings.Contains(s, "f=100") {
		t.Error("missing f=100 (PNG)")
	}
	if !strings.Contains(s, "U=1") {
		t.Error("missing U=1 (unicode placeholder)")
	}
}

func TestKitty_WrapForTmux(t *testing.T) {
	inner := "\x1b_Ga=T;payload\x1b\\"
	want := "\x1bPtmux;\x1b\x1b_Ga=T;payload\x1b\x1b\\\x1b\\"
	if got := wrapForTmux(inner); got != want {
		t.Fatalf("wrapForTmux() = %q, want %q", got, want)
	}
}

func TestKitty_UploadEscapeWrappedInTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux")

	payload := strings.Repeat("z", 4096+5)
	capture := &writeCapture{}
	if err := emitKittyUpload(SerializeOutput(capture), 42, payload, 10, 5); err != nil {
		t.Fatal(err)
	}
	if len(capture.writes) != 1 {
		t.Fatalf("write count = %d, want 1", len(capture.writes))
	}
	s := string(capture.writes[0])
	if strings.Count(s, "\x1bPtmux;") != 2 {
		t.Fatalf("tmux wrappers = %d, want 2", strings.Count(s, "\x1bPtmux;"))
	}
	if strings.Count(s, "\x1b\x1b_G") != 2 {
		t.Fatalf("wrapped kitty starts = %d, want 2", strings.Count(s, "\x1b\x1b_G"))
	}
	if !strings.HasPrefix(s, "\x1bPtmux;\x1b\x1b_G") {
		t.Fatalf("expected tmux-wrapped kitty upload, got %q", s[:minInt(20, len(s))])
	}
	if !strings.HasSuffix(s, "\x1b\x1b\\\x1b\\") {
		t.Fatalf("expected doubled inner ST plus tmux ST suffix, got %q", s)
	}
	if !strings.Contains(s, "a=T") || !strings.Contains(s, "U=1") {
		t.Fatalf("wrapped upload missing kitty parameters: %q", s)
	}
}

func TestEmitKittyUpload_EmptyPayloadNoOutput(t *testing.T) {
	t.Setenv("TMUX", "")
	capture := &writeCapture{}
	if err := emitKittyUpload(SerializeOutput(capture), 42, "", 4, 2); err != nil {
		t.Fatal(err)
	}
	if len(capture.writes) != 0 {
		t.Fatalf("write count = %d, want 0", len(capture.writes))
	}
}

func TestEmitKittyUpload_MultiChunkBufferedSingleWrite(t *testing.T) {
	t.Setenv("TMUX", "")
	payload := strings.Repeat("A", 4096*2+17)
	capture := &writeCapture{}
	if err := emitKittyUpload(SerializeOutput(capture), 42, payload, 4, 2); err != nil {
		t.Fatal(err)
	}
	if len(capture.writes) != 1 {
		t.Fatalf("write count = %d, want 1", len(capture.writes))
	}

	chunks := parseKittyTransfer(t, string(capture.writes[0]))
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[0].header != "a=T,f=100,t=d,i=42,U=1,c=4,r=2,q=2,m=1" {
		t.Fatalf("first header = %q", chunks[0].header)
	}
	if chunks[1].header != "m=1" {
		t.Fatalf("second header = %q, want %q", chunks[1].header, "m=1")
	}
	if chunks[2].header != "m=0" {
		t.Fatalf("third header = %q, want %q", chunks[2].header, "m=0")
	}
	if chunks[0].payload != payload[:4096] {
		t.Fatal("first chunk payload mismatch")
	}
	if chunks[1].payload != payload[4096:8192] {
		t.Fatal("second chunk payload mismatch")
	}
	if chunks[2].payload != payload[8192:] {
		t.Fatal("final chunk payload mismatch")
	}
}

func TestEmitKittyUpload_ConcurrentTransfersStayAtomic(t *testing.T) {
	t.Setenv("TMUX", "")
	capture := &writeCapture{}
	w := SerializeOutput(capture)
	transfers := []struct {
		id      uint32
		payload string
	}{
		{id: 101, payload: strings.Repeat("A", 4096*2+13)},
		{id: 202, payload: strings.Repeat("B", 4096+29)},
	}

	start := make(chan struct{})
	errCh := make(chan error, len(transfers))
	var wg sync.WaitGroup
	for _, transfer := range transfers {
		transfer := transfer
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errCh <- emitKittyUpload(w, transfer.id, transfer.payload, 4, 2)
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	writes := capture.snapshot()
	if len(writes) != 2 {
		t.Fatalf("transfer writes = %d, want 2", len(writes))
	}
	expected := map[uint32]string{}
	for _, transfer := range transfers {
		expected[transfer.id] = transfer.payload
	}
	seen := map[uint32]bool{}
	for _, raw := range writes {
		chunks := parseKittyTransfer(t, string(raw))
		id := kittyTransferID(t, chunks[0].header)
		if seen[id] {
			t.Fatalf("duplicate transfer for image id %d", id)
		}
		seen[id] = true
		var got strings.Builder
		for i, chunk := range chunks {
			got.WriteString(chunk.payload)
			if i == 0 {
				want := "a=T,f=100,t=d,i=" + strconv.Itoa(int(id)) + ",U=1,c=4,r=2,q=2,m="
				if !strings.HasPrefix(chunk.header, want) {
					t.Fatalf("first header = %q, want prefix %q", chunk.header, want)
				}
				continue
			}
			wantMore := "m=1"
			if i == len(chunks)-1 {
				wantMore = "m=0"
			}
			if chunk.header != wantMore {
				t.Fatalf("chunk %d header = %q, want %q", i, chunk.header, wantMore)
			}
		}
		if got.String() != expected[id] {
			t.Fatalf("payload mismatch for image id %d", id)
		}
	}
	if len(seen) != len(transfers) {
		t.Fatalf("seen transfers = %d, want %d", len(seen), len(transfers))
	}
}

func TestKitty_SecondRenderSameImageNoFlush(t *testing.T) {
	t.Setenv("TMUX", "")
	reg := NewRegistry()
	r := NewKittyRenderer(reg)
	src := makeSolid(8, 8, imgcolor.RGBA{1, 2, 3, 255})

	r.SetSource("test-key", src)
	out1 := r.RenderKey("test-key", image.Pt(4, 2))
	if out1.OnFlush == nil {
		t.Fatal("first render should flush")
	}
	// Confirm the upload was actually delivered: only AFTER the
	// closure has run does the renderer know the terminal received
	// the bytes. Without this, the second RenderKey should still
	// hand back an OnFlush — see TestKitty_RerenderBeforeUploadStillFlushes.
	if err := out1.OnFlush(&bytes.Buffer{}); err != nil {
		t.Fatalf("first OnFlush failed: %v", err)
	}

	out2 := r.RenderKey("test-key", image.Pt(4, 2))
	if out2.OnFlush != nil {
		t.Error("second render of same (key, size) after a successful upload should not flush again")
	}
	if out2.ID != out1.ID {
		t.Error("ID should be stable across renders of same (key, size)")
	}
}

func TestKitty_StaticUploadFailureRetriesSameCallback(t *testing.T) {
	t.Setenv("TMUX", "")
	reg := NewRegistry()
	r := NewKittyRenderer(reg)
	target := image.Pt(4, 2)
	r.SetSource("static", makeSolid(32, 32, imgcolor.RGBA{1, 2, 3, 255}))

	out := r.RenderKey("static", target)
	if out.OnFlush == nil {
		t.Fatal("expected static OnFlush")
	}
	err := out.OnFlush(partialErrorWriter{n: 1})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v, want %v", err, io.ErrShortWrite)
	}
	if _, fresh := reg.Lookup("static", target); !fresh {
		t.Fatal("registry marked upload complete after failed emit")
	}
	var retry bytes.Buffer
	if err := out.OnFlush(&retry); err != nil {
		t.Fatal(err)
	}
	if retry.Len() == 0 {
		t.Fatal("same callback retry wrote no bytes")
	}
	if _, fresh := reg.Lookup("static", target); fresh {
		t.Fatal("registry should be warm after successful retry")
	}
	var third bytes.Buffer
	if err := out.OnFlush(&third); err != nil {
		t.Fatal(err)
	}
	if third.Len() != 0 {
		t.Fatalf("third call should be idempotent after success, wrote %d bytes", third.Len())
	}
	warm := r.RenderKey("static", target)
	if warm.OnFlush != nil {
		t.Fatal("warm RenderKey should not return OnFlush after successful retry")
	}
}

// TestKitty_RerenderBeforeUploadStillFlushes captures the messages-pane
// cache-rebuild race that previously dropped images on the floor:
//
//  1. buildCache calls RenderKey → fresh=true, OnFlush closure captured
//     in a viewEntry. View() hasn't run yet, so the closure has NOT
//     fired (no bytes on the wire).
//  2. SetMessages is called again (e.g. network-verify after a cache hit)
//     → m.cache = nil, discarding the viewEntry and its closure.
//  3. buildCache runs again → RenderKey for the same (key, target).
//
// Under the buggy semantic, step 3 would return OnFlush=nil because the
// registry had already minted an ID — even though no bytes were ever
// sent to the terminal. The placement cells reference an image_id the
// terminal has never seen, so the image renders as blank cells.
//
// The correct semantic: RenderKey returns a fireable OnFlush until a
// previous closure has confirmed delivery. The test holds OnFlush from
// the first render WITHOUT firing it, then asserts the second render
// also hands back a fireable OnFlush.
func TestKitty_RerenderBeforeUploadStillFlushes(t *testing.T) {
	t.Setenv("TMUX", "")
	reg := NewRegistry()
	r := NewKittyRenderer(reg)
	src := makeSolid(8, 8, imgcolor.RGBA{1, 2, 3, 255})

	r.SetSource("test-key", src)
	out1 := r.RenderKey("test-key", image.Pt(4, 2))
	if out1.OnFlush == nil {
		t.Fatal("first render should flush (precondition)")
	}
	// Intentionally do NOT call out1.OnFlush — simulate the cache
	// rebuild that throws the closure away before it can fire.

	out2 := r.RenderKey("test-key", image.Pt(4, 2))
	if out2.OnFlush == nil {
		t.Fatal("second render before any successful upload must still flush — otherwise the image_id is referenced by placement cells the terminal never received")
	}
	if out2.ID != out1.ID {
		t.Errorf("ID should remain stable; got %d vs %d", out2.ID, out1.ID)
	}

	// After firing once, a third render should not flush again — one
	// successful upload per (key, target) is enough.
	if err := out2.OnFlush(&bytes.Buffer{}); err != nil {
		t.Fatalf("second OnFlush failed: %v", err)
	}
	out3 := r.RenderKey("test-key", image.Pt(4, 2))
	if out3.OnFlush != nil {
		t.Error("third render after a confirmed upload should not flush again")
	}
}

func TestKitty_PlaceholderRows(t *testing.T) {
	src := makeSolid(20, 20, imgcolor.RGBA{255, 255, 255, 255})
	r := NewKittyRenderer(NewRegistry())
	out := r.Render(src, image.Pt(10, 5))

	if len(out.Lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(out.Lines))
	}
	for i, line := range out.Lines {
		if !strings.Contains(line, "\U0010EEEE") {
			t.Errorf("line %d missing placeholder rune: %q", i, line[:minInt(30, len(line))])
		}
		if !strings.Contains(line, "\x1b[38;2;") {
			t.Errorf("line %d missing 24-bit SGR: %q", i, line[:minInt(30, len(line))])
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// BenchmarkKitty_RenderKeyWarm measures the steady-state cost of
// RenderKey for an already-uploaded (key, target) -- the path that
// dominates buildCache in image-heavy channels. Pre-fix this was
// ~16ms/op on typical kitty terminals because buildPlaceholderLines
// re-ran cells.Y * cells.X strings.Builder writes per call. Post-fix
// (placeholder-line memoization) it should be a single map lookup.
//
// We pre-flush so the registry marks the image uploaded; subsequent
// RenderKey calls hit the "fresh=false" branch -- which still ran
// buildPlaceholderLines before the fix.
func BenchmarkKitty_RenderKeyWarm(b *testing.B) {
	t := image.Pt(60, 20)
	src := makeSolid(60*8, 20*16, imgcolor.RGBA{1, 2, 3, 255})
	r := NewKittyRenderer(NewRegistry())
	r.SetSource("bench", src)

	// First call returns OnFlush; emit it so the registry marks the
	// image uploaded. Subsequent calls hit the warm path.
	first := r.RenderKey("bench", t)
	if first.OnFlush != nil {
		_ = first.OnFlush(&bytes.Buffer{})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.RenderKey("bench", t)
	}
}

// BenchmarkKitty_RenderKeyFreshRepeat exercises the path that
// dominates real-world channel-switch latency: an image whose
// OnFlush has not yet fired (off-screen viewEntries) so
// Registry.Lookup keeps returning fresh=true. Pre-fix every call
// re-ran bilinear-resize + PNG-encode + base64. Post-fix the
// payload cache should make these calls effectively free.
func BenchmarkKitty_RenderKeyFreshRepeat(b *testing.B) {
	t := image.Pt(60, 16)
	// 8x16 pixels per cell -> 480x256 source roughly matches a
	// typical Slack attachment thumbnail target.
	src := makeSolid(60*8, 16*16, imgcolor.RGBA{1, 2, 3, 255})
	r := NewKittyRenderer(NewRegistry())
	r.SetSource("bench-fresh", src)
	// Do NOT call OnFlush; this simulates an off-screen viewEntry
	// whose flush was never invoked. Subsequent RenderKey calls
	// will see fresh=true on every iteration.
	_ = r.RenderKey("bench-fresh", t)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.RenderKey("bench-fresh", t)
	}
}

// TestKitty_RenderKeyWarmMatchesCold guards the placeholder-line
// memoization: the cached output must be byte-identical to the
// freshly-computed output for the same (id, target).
func TestKitty_RenderKeyWarmMatchesCold(t *testing.T) {
	target := image.Pt(8, 4)
	src := makeSolid(64, 64, imgcolor.RGBA{1, 2, 3, 255})
	r := NewKittyRenderer(NewRegistry())
	r.SetSource("k", src)

	cold := r.RenderKey("k", target)
	if cold.OnFlush != nil {
		_ = cold.OnFlush(&bytes.Buffer{}) // mark uploaded
	}
	warm := r.RenderKey("k", target)

	if len(cold.Lines) != len(warm.Lines) {
		t.Fatalf("line count differs: cold=%d warm=%d", len(cold.Lines), len(warm.Lines))
	}
	for i := range cold.Lines {
		if cold.Lines[i] != warm.Lines[i] {
			t.Errorf("line %d differs:\n cold=%q\n warm=%q", i, cold.Lines[i], warm.Lines[i])
		}
	}
	if cold.ID != warm.ID {
		t.Errorf("ID differs: cold=%d warm=%d", cold.ID, warm.ID)
	}
}

// TestKitty_PayloadCacheUploadIdentity guards the payload memoization:
// when fresh=true is returned for the same (id, target) multiple times
// (the off-screen viewEntry case), each OnFlush invocation must emit
// byte-identical APC upload bytes. The first call's OnFlush runs the
// expensive resize+encode; subsequent calls reuse the cached payload.
// Without this guarantee, repeated cache rebuilds for the same image
// could produce divergent uploads and leave the terminal pointing at
// stale image data.
//
// We deliberately do NOT call MarkUploaded between the two RenderKey
// calls (i.e. we DO NOT invoke first.OnFlush). This is the exact path
// the bug fixes: an off-screen image whose OnFlush was never consumed
// by the visible-entry loop, then re-rendered on the next buildCache.
func TestKitty_PayloadCacheUploadIdentity(t *testing.T) {
	t.Setenv("TMUX", "")
	target := image.Pt(10, 6)
	src := makeSolid(80, 96, imgcolor.RGBA{200, 100, 50, 255})
	r := NewKittyRenderer(NewRegistry())
	r.SetSource("payload-test", src)

	first := r.RenderKey("payload-test", target)
	if first.OnFlush == nil {
		t.Fatal("first call: expected OnFlush set when fresh=true")
	}
	var firstBuf bytes.Buffer
	if err := first.OnFlush(&firstBuf); err != nil {
		t.Fatal(err)
	}

	// Note: we do NOT call MarkUploaded explicitly. The first OnFlush
	// did call it via the registry, so Lookup will now return
	// fresh=false. To force fresh=true again (the off-screen case),
	// use a different (key, target) bound to a different id but
	// reuse the source image -- this exercises the payload cache
	// path because the second call still goes through the
	// resize+encode branch with a fresh id.
	//
	// Simpler and more direct: hand-craft the off-screen scenario by
	// reaching past MarkUploaded via a fresh renderer where we never
	// drain OnFlush. Each fresh=true call must reuse the cached
	// payload for the SAME (id, target).
	r2 := NewKittyRenderer(NewRegistry())
	r2.SetSource("payload-test", src)
	a := r2.RenderKey("payload-test", target)
	b := r2.RenderKey("payload-test", target)
	if a.OnFlush == nil || b.OnFlush == nil {
		t.Fatal("both off-screen calls: expected OnFlush set when fresh=true")
	}
	var aBuf, bBuf bytes.Buffer
	if err := a.OnFlush(&aBuf); err != nil {
		t.Fatal(err)
	}
	if err := b.OnFlush(&bBuf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aBuf.Bytes(), bBuf.Bytes()) {
		t.Errorf("upload bytes differ between consecutive fresh=true calls\nlen a=%d b=%d", aBuf.Len(), bBuf.Len())
	}
}

func TestKitty_AnimatedStableIDFrameReplacementAndDedup(t *testing.T) {
	target := image.Pt(2, 1)
	frame0 := makeSolid(16, 16, imgcolor.RGBA{255, 0, 0, 255}).(*image.RGBA)
	frame1 := makeSolid(16, 16, imgcolor.RGBA{0, 0, 255, 255}).(*image.RGBA)
	anim := &Animation{
		Frames:    []*image.RGBA{frame0, frame1},
		Delays:    []time.Duration{100 * time.Millisecond, 100 * time.Millisecond},
		LoopCount: 0,
		Duration:  200 * time.Millisecond,
	}
	r := NewKittyRenderer(NewRegistry())
	r.SetSource("anim", frame0)
	r.SetAnimation("anim", anim)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	out := r.RenderKey("anim", target)
	if !out.Animated {
		t.Fatal("expected animated render")
	}
	if out.OnFlush == nil {
		t.Fatal("expected reusable animated OnFlush")
	}
	if out.ID == 0 {
		t.Fatal("expected stable kitty image ID")
	}

	var first bytes.Buffer
	if err := out.OnFlush(&first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "i="+itoaInt(int(out.ID))) {
		t.Fatalf("first animated upload missing stable image id %d", out.ID)
	}

	var sameFrame bytes.Buffer
	if err := out.OnFlush(&sameFrame); err != nil {
		t.Fatal(err)
	}
	if sameFrame.Len() != 0 {
		t.Fatalf("same-frame animated flush should dedupe, wrote %d bytes", sameFrame.Len())
	}

	now = now.Add(150 * time.Millisecond)
	var nextFrame bytes.Buffer
	if err := out.OnFlush(&nextFrame); err != nil {
		t.Fatal(err)
	}
	if nextFrame.Len() == 0 {
		t.Fatal("next animated frame should replace pixels on same image ID")
	}
	if !strings.Contains(nextFrame.String(), "i="+itoaInt(int(out.ID))) {
		t.Fatalf("next animated upload missing stable image id %d", out.ID)
	}

	second := r.RenderKey("anim", target)
	if second.ID != out.ID {
		t.Fatalf("stable ID changed across renders: %d vs %d", second.ID, out.ID)
	}
	if !second.Animated || second.OnFlush == nil {
		t.Fatal("subsequent animated RenderKey should stay animated and reusable")
	}
}

func TestKitty_AnimatedUploadFailureDoesNotCommitFrame(t *testing.T) {
	target := image.Pt(2, 1)
	frame0 := makeSolid(16, 16, imgcolor.RGBA{255, 0, 0, 255}).(*image.RGBA)
	frame1 := makeSolid(16, 16, imgcolor.RGBA{0, 0, 255, 255}).(*image.RGBA)
	anim := &Animation{
		Frames:    []*image.RGBA{frame0, frame1},
		Delays:    []time.Duration{100 * time.Millisecond, 100 * time.Millisecond},
		LoopCount: 0,
		Duration:  200 * time.Millisecond,
	}
	reg := NewRegistry()
	r := NewKittyRenderer(reg)
	r.SetSource("anim", frame0)
	r.SetAnimation("anim", anim)
	r.now = func() time.Time { return time.Unix(0, 0) }

	out := r.RenderKey("anim", target)
	boom := errors.New("boom")
	if err := out.OnFlush(partialErrorWriter{n: 7, err: boom}); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
	state := r.animationState[out.ID]
	if state.hasFrame {
		t.Fatal("animation frame committed after failed emit")
	}
	if _, fresh := reg.Lookup("anim", target); !fresh {
		t.Fatal("registry marked animated upload complete after failed emit")
	}
	var retry bytes.Buffer
	if err := out.OnFlush(&retry); err != nil {
		t.Fatal(err)
	}
	if retry.Len() == 0 {
		t.Fatal("retry animated upload wrote no bytes")
	}
	state = r.animationState[out.ID]
	if !state.hasFrame || state.emittedFrame != 0 {
		t.Fatalf("animation state after retry = %+v, want committed frame 0", state)
	}
}

func TestKitty_AnimatedDuplicateOccurrencesDeduped(t *testing.T) {
	target := image.Pt(2, 1)
	frame0 := makeSolid(16, 16, imgcolor.RGBA{255, 0, 0, 255}).(*image.RGBA)
	anim := &Animation{
		Frames:    []*image.RGBA{frame0},
		Delays:    []time.Duration{10 * time.Second},
		LoopCount: 0,
		Duration:  10 * time.Second,
	}
	r := NewKittyRenderer(NewRegistry())
	r.SetSource("anim", frame0)
	r.SetAnimation("anim", anim)
	r.now = func() time.Time { return time.Unix(0, 0) }

	a := r.RenderKey("anim", target)
	b := r.RenderKey("anim", target)
	if a.OnFlush == nil || b.OnFlush == nil {
		t.Fatal("expected animated flush callbacks on duplicate occurrences")
	}
	var first, second bytes.Buffer
	if err := a.OnFlush(&first); err != nil {
		t.Fatal(err)
	}
	if err := b.OnFlush(&second); err != nil {
		t.Fatal(err)
	}
	if first.Len() == 0 {
		t.Fatal("first occurrence should emit upload bytes")
	}
	if second.Len() != 0 {
		t.Fatalf("duplicate occurrence should be deduped, wrote %d bytes", second.Len())
	}
}

func TestKitty_AnimatedFiniteLoopFreezesLastFrame(t *testing.T) {
	target := image.Pt(2, 1)
	frame0 := makeSolid(16, 16, imgcolor.RGBA{255, 0, 0, 255}).(*image.RGBA)
	frame1 := makeSolid(16, 16, imgcolor.RGBA{0, 255, 0, 255}).(*image.RGBA)
	anim := &Animation{
		Frames:    []*image.RGBA{frame0, frame1},
		Delays:    []time.Duration{100 * time.Millisecond, 100 * time.Millisecond},
		LoopCount: -1,
		Duration:  200 * time.Millisecond,
	}
	r := NewKittyRenderer(NewRegistry())
	r.SetSource("anim", frame0)
	r.SetAnimation("anim", anim)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	out := r.RenderKey("anim", target)
	if err := out.OnFlush(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(250 * time.Millisecond)
	var last bytes.Buffer
	if err := out.OnFlush(&last); err != nil {
		t.Fatal(err)
	}
	if last.Len() == 0 {
		t.Fatal("finite animation should emit final frame when loop completes")
	}
	now = now.Add(250 * time.Millisecond)
	var frozen bytes.Buffer
	if err := out.OnFlush(&frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.Len() != 0 {
		t.Fatalf("final frame should stay frozen, wrote %d bytes", frozen.Len())
	}
}

func TestKitty_AnimatedRefreshInvalidatesPayloadAndPlayback(t *testing.T) {
	target := image.Pt(2, 1)
	old0 := makeSolid(16, 16, imgcolor.RGBA{255, 0, 0, 255}).(*image.RGBA)
	old1 := makeSolid(16, 16, imgcolor.RGBA{0, 0, 255, 255}).(*image.RGBA)
	new0 := makeSolid(16, 16, imgcolor.RGBA{0, 255, 0, 255}).(*image.RGBA)
	new1 := makeSolid(16, 16, imgcolor.RGBA{255, 255, 0, 255}).(*image.RGBA)

	r := NewKittyRenderer(NewRegistry())
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	r.SetSource("anim", old0)
	r.SetAnimation("anim", &Animation{
		Frames:    []*image.RGBA{old0, old1},
		Delays:    []time.Duration{100 * time.Millisecond, 100 * time.Millisecond},
		LoopCount: 0,
		Duration:  200 * time.Millisecond,
	})
	first := r.RenderKey("anim", target)
	var firstBuf bytes.Buffer
	if err := first.OnFlush(&firstBuf); err != nil {
		t.Fatal(err)
	}
	if firstBuf.Len() == 0 {
		t.Fatal("first animation upload should emit bytes")
	}

	r.SetSource("anim", new0)
	r.SetAnimation("anim", &Animation{
		Frames:    []*image.RGBA{new0, new1},
		Delays:    []time.Duration{100 * time.Millisecond, 100 * time.Millisecond},
		LoopCount: 0,
		Duration:  200 * time.Millisecond,
	})
	refreshed := r.RenderKey("anim", target)
	if refreshed.ID != first.ID {
		t.Fatalf("refresh should keep stable kitty image id: got %d want %d", refreshed.ID, first.ID)
	}
	var refreshedBuf bytes.Buffer
	if err := refreshed.OnFlush(&refreshedBuf); err != nil {
		t.Fatal(err)
	}
	if refreshedBuf.Len() == 0 {
		t.Fatal("refreshed animation should emit first upload again")
	}
	if bytes.Equal(firstBuf.Bytes(), refreshedBuf.Bytes()) {
		t.Fatal("refreshed animation upload bytes should differ after payload invalidation")
	}
}

func TestKitty_SetSourceChangedSourceReuploadsSameID(t *testing.T) {
	target := image.Pt(2, 1)
	oldSrc := makeSolid(16, 16, imgcolor.RGBA{255, 0, 0, 255})
	newSrc := makeSolid(16, 16, imgcolor.RGBA{0, 255, 0, 255})
	r := NewKittyRenderer(NewRegistry())
	r.SetSource("static", oldSrc)
	first := r.RenderKey("static", target)
	var firstBuf bytes.Buffer
	if err := first.OnFlush(&firstBuf); err != nil {
		t.Fatal(err)
	}
	if firstBuf.Len() == 0 {
		t.Fatal("first static upload should emit bytes")
	}

	r.SetSource("static", newSrc)
	refreshed := r.RenderKey("static", target)
	if refreshed.ID != first.ID {
		t.Fatalf("stable id changed across source refresh: got %d want %d", refreshed.ID, first.ID)
	}
	if refreshed.OnFlush == nil {
		t.Fatal("changed source under same key should reupload on next RenderKey")
	}
	var refreshedBuf bytes.Buffer
	if err := refreshed.OnFlush(&refreshedBuf); err != nil {
		t.Fatal(err)
	}
	if refreshedBuf.Len() == 0 {
		t.Fatal("refreshed static upload should emit replacement bytes")
	}
	if bytes.Equal(firstBuf.Bytes(), refreshedBuf.Bytes()) {
		t.Fatal("replacement upload bytes should differ for changed source")
	}
}

func TestKitty_SetSourceSameSourceRebindStaysWarm(t *testing.T) {
	target := image.Pt(2, 1)
	src := makeSolid(16, 16, imgcolor.RGBA{255, 0, 0, 255})
	r := NewKittyRenderer(NewRegistry())
	r.SetSource("static", src)
	first := r.RenderKey("static", target)
	if first.OnFlush == nil {
		t.Fatal("first render should flush")
	}
	if err := first.OnFlush(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	r.SetSource("static", src)
	warm := r.RenderKey("static", target)
	if warm.ID != first.ID {
		t.Fatalf("stable id changed after same-source rebind: got %d want %d", warm.ID, first.ID)
	}
	if warm.OnFlush != nil {
		t.Fatal("same in-memory source rebind should keep warm/static payload state")
	}
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

type writeCapture struct {
	mu     sync.Mutex
	writes [][]byte
}

func (w *writeCapture) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (w *writeCapture) snapshot() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([][]byte, len(w.writes))
	for i := range w.writes {
		out[i] = append([]byte(nil), w.writes[i]...)
	}
	return out
}

type kittyTransferChunk struct {
	header  string
	payload string
}

func parseKittyTransfer(t *testing.T, transfer string) []kittyTransferChunk {
	t.Helper()
	chunks := []kittyTransferChunk{}
	for len(transfer) > 0 {
		if !strings.HasPrefix(transfer, "\x1b_G") {
			t.Fatalf("transfer missing kitty prefix: %q", transfer[:minInt(20, len(transfer))])
		}
		transfer = transfer[len("\x1b_G"):]
		end := strings.Index(transfer, "\x1b\\")
		if end < 0 {
			t.Fatalf("transfer missing terminator: %q", transfer)
		}
		body := transfer[:end]
		header, payload, ok := strings.Cut(body, ";")
		if !ok {
			t.Fatalf("transfer missing header separator: %q", body)
		}
		chunks = append(chunks, kittyTransferChunk{header: header, payload: payload})
		transfer = transfer[end+len("\x1b\\"):]
	}
	return chunks
}

func kittyTransferID(t *testing.T, header string) uint32 {
	t.Helper()
	for _, field := range strings.Split(header, ",") {
		if !strings.HasPrefix(field, "i=") {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimPrefix(field, "i="), 10, 32)
		if err != nil {
			t.Fatalf("parse image id from %q: %v", header, err)
		}
		return uint32(id)
	}
	t.Fatalf("header missing image id: %q", header)
	return 0
}
