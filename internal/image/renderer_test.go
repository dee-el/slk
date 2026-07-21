package image

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// TestSerializeOutput_NoInterleave asserts that concurrent Write calls
// to a SerializeOutput-wrapped writer land as contiguous runs — never
// interleaved at the byte level. This is the invariant the kitty
// graphics path depends on: a single image upload is hundreds of KB
// of chunked APC escape data and ANY byte-level interleave from a
// competing goroutine corrupts the protocol stream.
func TestSerializeOutput_NoInterleave(t *testing.T) {
	var buf bytes.Buffer
	w := SerializeOutput(&buf)

	const size = 10000
	a := bytes.Repeat([]byte("A"), size)
	b := bytes.Repeat([]byte("B"), size)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); w.Write(a) }()
	go func() { defer wg.Done(); w.Write(b) }()
	wg.Wait()

	got := buf.String()
	wantAB := strings.Repeat("A", size) + strings.Repeat("B", size)
	wantBA := strings.Repeat("B", size) + strings.Repeat("A", size)
	if got != wantAB && got != wantBA {
		// Find the first non-A non-B run boundary to give a useful
		// diagnostic. The full strings are too large to print.
		for i := 1; i < len(got); i++ {
			if got[i] != got[i-1] {
				if i != size {
					t.Fatalf("write interleave detected: first boundary at byte %d, want %d", i, size)
				}
				break
			}
		}
		t.Fatalf("unexpected output: len=%d", len(got))
	}
}

// TestSerializeOutput_ManyWritersForwardsAllBytes asserts that all
// bytes from many concurrent writers reach the underlying writer.
func TestSerializeOutput_ManyWritersForwardsAllBytes(t *testing.T) {
	var buf bytes.Buffer
	w := SerializeOutput(&buf)

	const writers = 32
	const perWriter = 1024
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			w.Write(bytes.Repeat([]byte("x"), perWriter))
		}()
	}
	wg.Wait()

	if got, want := buf.Len(), writers*perWriter; got != want {
		t.Fatalf("byte count: got %d, want %d", got, want)
	}
}

func TestSerializeOutput_CompletesShortWritesUnderOneLock(t *testing.T) {
	under := newBlockingPartialWriter()
	w := SerializeOutput(under)

	type result struct {
		name string
		n    int
		err  error
	}
	results := make(chan result, 2)
	go func() {
		n, err := w.Write([]byte("AAAA"))
		results <- result{name: "A", n: n, err: err}
	}()

	<-under.firstCallDone
	go func() {
		n, err := w.Write([]byte("BBBB"))
		results <- result{name: "B", n: n, err: err}
	}()

	close(under.releaseSecondCall)

	gotResults := map[string]result{}
	for range 2 {
		res := <-results
		gotResults[res.name] = res
	}
	if res := gotResults["A"]; res.n != 4 || res.err != nil {
		t.Fatalf("write A = (%d, %v), want (4, nil)", res.n, res.err)
	}
	if res := gotResults["B"]; res.n != 4 || res.err != nil {
		t.Fatalf("write B = (%d, %v), want (4, nil)", res.n, res.err)
	}
	if got := under.callsSnapshot(); len(got) != 3 || got[0] != "AA" || got[1] != "AA" || got[2] != "BBBB" {
		t.Fatalf("underlying call order = %q, want [\"AA\" \"AA\" \"BBBB\"]", got)
	}
}

func TestSerializeOutput_ReturnsPartialCountAndError(t *testing.T) {
	boom := errors.New("boom")
	w := SerializeOutput(partialErrorWriter{n: 2, err: boom})
	n, err := w.Write([]byte("ABCD"))
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
}

func TestSerializeOutput_ZeroProgressReturnsErrShortWrite(t *testing.T) {
	w := SerializeOutput(zeroProgressWriter{})
	n, err := w.Write([]byte("ABCD"))
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v, want %v", err, io.ErrShortWrite)
	}
}

func TestSerializeOutput_InvalidWriteCount(t *testing.T) {
	w := SerializeOutput(invalidCountWriter{})
	n, err := w.Write([]byte("ABCD"))
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid write count") {
		t.Fatalf("error = %v, want invalid write count", err)
	}
}

type blockingPartialWriter struct {
	mu                sync.Mutex
	calls             []string
	firstCallDone     chan struct{}
	releaseSecondCall chan struct{}
}

func newBlockingPartialWriter() *blockingPartialWriter {
	return &blockingPartialWriter{
		firstCallDone:     make(chan struct{}),
		releaseSecondCall: make(chan struct{}),
	}
}

func (w *blockingPartialWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	call := len(w.calls)
	switch call {
	case 0:
		chunk := len(p) / 2
		if chunk == 0 {
			chunk = 1
		}
		w.calls = append(w.calls, string(p[:chunk]))
		close(w.firstCallDone)
		w.mu.Unlock()
		return chunk, nil
	case 1:
		w.calls = append(w.calls, string(p))
		w.mu.Unlock()
		<-w.releaseSecondCall
		return len(p), nil
	default:
		w.calls = append(w.calls, string(p))
		w.mu.Unlock()
		return len(p), nil
	}
}

func (w *blockingPartialWriter) callsSnapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

type partialErrorWriter struct {
	n   int
	err error
}

func (w partialErrorWriter) Write(p []byte) (int, error) {
	if w.n > len(p) {
		return len(p), w.err
	}
	return w.n, w.err
}

type zeroProgressWriter struct{}

func (zeroProgressWriter) Write([]byte) (int, error) {
	return 0, nil
}

type invalidCountWriter struct{}

func (invalidCountWriter) Write(p []byte) (int, error) {
	return len(p) + 1, nil
}
