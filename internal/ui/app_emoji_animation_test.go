package ui

import (
	"context"
	"image"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	emojiutil "github.com/gammons/slk/internal/emoji"
	imgpkg "github.com/gammons/slk/internal/image"
	"github.com/gammons/slk/internal/ui/messages"
)

type animatedEmojiFetcher struct {
	key    string
	target image.Point
	render imgpkg.Render
}

func (f animatedEmojiFetcher) Fetch(context.Context, imgpkg.FetchRequest) (imgpkg.FetchResult, error) {
	return imgpkg.FetchResult{}, nil
}

func (f animatedEmojiFetcher) Prerendered(key string, target image.Point, proto imgpkg.Protocol) (imgpkg.Render, bool) {
	if proto == imgpkg.ProtoKitty && key == f.key && target == f.target {
		return f.render, true
	}
	return imgpkg.Render{}, false
}

func waitForAsyncCount(t *testing.T, ch <-chan struct{}, count *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.After(time.Second)
	for count.Load() < want {
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("count = %d, want %d", count.Load(), want)
		}
	}
}

func newAnimatedEmojiApp(t *testing.T) (*App, *atomic.Int32, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	emojiutil.ResetAnimationClockForTest()
	emojiutil.SetAnimationBlocked(false)
	emojiutil.SetImageMode(true, 2)
	t.Cleanup(emojiutil.ResetAnimationClockForTest)
	t.Cleanup(func() {
		emojiutil.SetAnimationBlocked(false)
		emojiutil.SetImageMode(false, 2)
	})

	a := NewApp()
	a.width = 120
	a.height = 30
	a.imgProtocol = imgpkg.ProtoKitty
	url := emojiutil.CDNBaseURL + "1f44d.png"
	flushCount := &atomic.Int32{}
	startCount := &atomic.Int32{}
	startCh := make(chan struct{}, 8)
	a.SetEmojiContext(messages.EmojiContext{
		PlaceCtx: emojiutil.PlaceContext{
			Fetcher: animatedEmojiFetcher{
				key:    emojiutil.EmojiCacheKey(url),
				target: image.Pt(2, 1),
				render: imgpkg.Render{
					Cells:    image.Pt(2, 1),
					Lines:    []string{"\U0010EEEE\U0010EEEE"},
					Animated: true,
					OnFlush: func(io.Writer) error {
						flushCount.Add(1)
						return nil
					},
				},
			},
			AnimationEnabled: true,
			SendMsg: func(msg any) {
				if _, ok := msg.(emojiutil.EmojiAnimationStartMsg); ok {
					startCount.Add(1)
					select {
					case startCh <- struct{}{}:
					default:
					}
				}
			},
		},
		Cells: 2,
	})
	a.messagepane.SetMessages([]messages.MessageItem{{
		TS:        "1.0",
		UserName:  "alice",
		UserID:    "U1",
		Text:      "hi :thumbsup:",
		Timestamp: "1:00 PM",
	}})
	return a, flushCount, startCount, startCh
}

func newAnimationEnabledPlainApp(t *testing.T) *App {
	t.Helper()
	emojiutil.ResetAnimationClockForTest()
	emojiutil.SetAnimationBlocked(false)
	emojiutil.SetImageMode(true, 2)
	t.Cleanup(emojiutil.ResetAnimationClockForTest)
	t.Cleanup(func() {
		emojiutil.SetAnimationBlocked(false)
		emojiutil.SetImageMode(false, 2)
	})

	a := NewApp()
	a.width = 120
	a.height = 30
	a.imgProtocol = imgpkg.ProtoKitty
	a.SetEmojiContext(messages.EmojiContext{
		PlaceCtx: emojiutil.PlaceContext{AnimationEnabled: true},
		Cells:    2,
	})
	a.messagepane.SetMessages([]messages.MessageItem{{
		TS:        "1.0",
		UserName:  "alice",
		UserID:    "U1",
		Text:      "hello world",
		Timestamp: "1:00 PM",
	}})
	return a
}

func cacheSentinel(out, old, sentinel string) string {
	if replaced := strings.Replace(out, old, sentinel, 1); replaced != out {
		return replaced
	}
	return sentinel
}

func TestEmojiAnimationEnabledIdle_ReusesMessageTopCache(t *testing.T) {
	a := newAnimationEnabledPlainApp(t)
	first := a.View().Content
	if !a.renderCache.msgTop.valid {
		t.Fatal("precondition: msgTop cache not primed")
	}
	a.lastScreenValid = false
	a.renderCache.msgTop.output = cacheSentinel(a.renderCache.msgTop.output, "general", "CACHED!")
	second := a.View().Content
	if !strings.Contains(second, "CACHED!") {
		t.Fatalf("expected cached msgTop output reused when animation enabled but idle\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestEmojiAnimationEnabledIdle_ReusesThreadTopCache(t *testing.T) {
	a := newAnimationEnabledPlainApp(t)
	a.width = 200
	a.height = 40
	a.threadVisible = true
	a.threadPanel.SetThread(
		messages.MessageItem{TS: "1.0", UserName: "alice", Text: "parent"},
		[]messages.MessageItem{{TS: "1.1", UserName: "bob", Text: "reply"}},
		"C1", "1.0",
	)
	_ = a.View()
	if !a.renderCache.thread.valid {
		t.Fatal("precondition: thread top cache not primed")
	}
	a.lastScreenValid = false
	a.renderCache.thread.output = cacheSentinel(a.renderCache.thread.output, "reply", "CACHE")
	second := a.View().Content
	if !strings.Contains(second, "CACHE") {
		t.Fatalf("expected cached thread top output reused when animation enabled but idle\nsecond:\n%s", second)
	}
}

func TestEmojiAnimationTickChain_StartStopRestart(t *testing.T) {
	a, flushCount, startCount, startCh := newAnimatedEmojiApp(t)
	version := a.messagepane.Version()

	_ = a.View()
	if flushCount.Load() != 1 {
		t.Fatalf("initial View flush count = %d, want 1", flushCount.Load())
	}
	waitForAsyncCount(t, startCh, startCount, 1)

	_, cmd := a.Update(EmojiAnimationStartMsg{})
	if cmd == nil {
		t.Fatal("start msg should schedule animation tick")
	}
	if !a.emojiAnimationTicking {
		t.Fatal("emojiAnimationTicking should be true after start")
	}

	a.lastScreenValid = false
	a.renderCache.msgTop.output = cacheSentinel(a.renderCache.msgTop.output, "general", "CACHED!")
	second := a.View().Content
	if !strings.Contains(second, "CACHED!") {
		t.Fatalf("expected active animation frame to reuse cached msgTop output\n%s", second)
	}
	if flushCount.Load() != 2 {
		t.Fatalf("second View flush count = %d, want 2", flushCount.Load())
	}
	if startCount.Load() != 1 {
		t.Fatalf("start count while chain active = %d, want 1", startCount.Load())
	}

	_, cmd = a.Update(emojiAnimationTickMsg{now: time.Now().Add(50 * time.Millisecond)})
	if cmd == nil {
		t.Fatal("visible animation tick should continue chain")
	}
	if a.messagepane.Version() != version {
		t.Fatal("animation tick must not invalidate message cache version")
	}

	_, cmd = a.Update(emojiAnimationTickMsg{now: time.Now().Add(300 * time.Millisecond)})
	if cmd != nil {
		t.Fatal("inactive animation tick should stop chain")
	}
	if a.emojiAnimationTicking {
		t.Fatal("emojiAnimationTicking should be false after inactivity stop")
	}

	a.lastScreenValid = false
	a.renderCache.msgTop.output = cacheSentinel(a.renderCache.msgTop.output, "CACHED!", "RESTART")
	restart := a.View().Content
	if !strings.Contains(restart, "RESTART") {
		t.Fatalf("expected restart frame to reuse cached msgTop output\n%s", restart)
	}
	waitForAsyncCount(t, startCh, startCount, 2)
	if flushCount.Load() != 3 {
		t.Fatalf("restart View flush count = %d, want 3", flushCount.Load())
	}
}

func TestEmojiAnimationStopsUnderModal(t *testing.T) {
	a, flushCount, startCount, startCh := newAnimatedEmojiApp(t)

	_ = a.View()
	waitForAsyncCount(t, startCh, startCount, 1)
	_, _ = a.Update(EmojiAnimationStartMsg{})
	a.confirmPrompt.Open("Quit?", "Body", nil)
	a.SetMode(ModeConfirm)

	_ = a.View()
	if flushCount.Load() != 1 {
		t.Fatalf("modal View should block background animated flushes, got %d", flushCount.Load())
	}

	_, cmd := a.Update(emojiAnimationTickMsg{now: time.Now().Add(50 * time.Millisecond)})
	if cmd != nil {
		t.Fatal("modal tick should stop animation chain")
	}
	if a.emojiAnimationTicking {
		t.Fatal("emojiAnimationTicking should be false under modal")
	}

	a.confirmPrompt.Close()
	a.SetMode(ModeNormal)
	a.lastScreenValid = false
	a.renderCache.msgTop.output = cacheSentinel(a.renderCache.msgTop.output, "general", "UNBLOCK!")
	resumed := a.View().Content
	if !strings.Contains(resumed, "UNBLOCK!") {
		t.Fatalf("expected modal-close frame to reuse cached msgTop output\n%s", resumed)
	}
	waitForAsyncCount(t, startCh, startCount, 2)
	if flushCount.Load() != 2 {
		t.Fatalf("closing modal should resume animated flushes, got %d", flushCount.Load())
	}
}
