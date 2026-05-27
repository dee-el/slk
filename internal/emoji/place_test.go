package emoji

import (
	"context"
	"errors"
	goimage "image"
	"sync"
	"testing"

	imgpkg "github.com/gammons/slk/internal/image"
)

// fakeFetcher implements PlaceFetcher for unit tests. Behavior is
// controlled by the prerender map (warm hits) and a fetchFn closure
// (cold-path fetch behavior).
type fakeFetcher struct {
	mu         sync.Mutex
	prerender  map[string]imgpkg.Render // keyed by "<key>|<cx>x<cy>"
	fetchFn    func(ctx context.Context, req imgpkg.FetchRequest) (imgpkg.FetchResult, error)
	fetchCalls []imgpkg.FetchRequest
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{prerender: map[string]imgpkg.Render{}}
}

func (f *fakeFetcher) prerenderKey(key string, t goimage.Point) string {
	return key + "|" + itoa(t.X) + "x" + itoa(t.Y)
}

func itoa(n int) string {
	// avoid strconv import here for brevity in tests
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (f *fakeFetcher) setPrerendered(key string, target goimage.Point, r imgpkg.Render) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prerender[f.prerenderKey(key, target)] = r
}

func (f *fakeFetcher) Prerendered(key string, t goimage.Point, proto imgpkg.Protocol) (imgpkg.Render, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.prerender[f.prerenderKey(key, t)]
	return r, ok
}

func (f *fakeFetcher) Fetch(ctx context.Context, req imgpkg.FetchRequest) (imgpkg.FetchResult, error) {
	f.mu.Lock()
	f.fetchCalls = append(f.fetchCalls, req)
	fn := f.fetchFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return imgpkg.FetchResult{}, errors.New("fakeFetcher: no fetchFn set")
}

func TestPlace_InvalidInputs(t *testing.T) {
	ff := newFakeFetcher()
	ctx := PlaceContext{Fetcher: ff}

	cases := []struct {
		name string
		url  string
		cell int
		fctx PlaceContext
	}{
		{"empty url", "", 2, ctx},
		{"zero cells", "https://x", 0, ctx},
		{"negative cells", "https://x", -1, ctx},
		{"nil fetcher", "https://x", 2, PlaceContext{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, flush, ok := Place(c.fctx, c.url, c.cell)
			if ok {
				t.Errorf("Place(%q, %d) = (%q, flush=%v, true), want ok=false", c.url, c.cell, s, flush != nil)
			}
			if s != "" {
				t.Errorf("Place(%q, %d) placement = %q, want \"\"", c.url, c.cell, s)
			}
		})
	}

	// The fetcher should not have been called for any of these inputs.
	if len(ff.fetchCalls) != 0 {
		t.Errorf("fetcher was called %d times for invalid inputs, want 0", len(ff.fetchCalls))
	}
}
