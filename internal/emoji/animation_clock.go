package emoji

import (
	"sync"
	"sync/atomic"
	"time"
)

const animationClockGrace = 250 * time.Millisecond

// EmojiAnimationStartMsg kicks off the UI's shared 50ms animation tick chain.
type EmojiAnimationStartMsg struct{}

type AnimationClock struct {
	mu          sync.Mutex
	tickerOn    bool
	lastVisible time.Time
}

var animationStartDispatch = func(send func(any), msg any) {
	go send(msg)
}

func (c *AnimationClock) MarkVisible(send func(any)) {
	now := time.Now()
	start := false
	c.mu.Lock()
	c.lastVisible = now
	if send != nil && !c.tickerOn {
		c.tickerOn = true
		start = true
	}
	c.mu.Unlock()
	if start {
		animationStartDispatch(send, EmojiAnimationStartMsg{})
	}
}

func (c *AnimationClock) Continue(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.tickerOn {
		return false
	}
	if now.Sub(c.lastVisible) <= animationClockGrace {
		return true
	}
	c.tickerOn = false
	return false
}

func (c *AnimationClock) Stop() {
	c.mu.Lock()
	c.tickerOn = false
	c.mu.Unlock()
}

func (c *AnimationClock) resetForTest() {
	c.mu.Lock()
	c.tickerOn = false
	c.lastVisible = time.Time{}
	c.mu.Unlock()
}

var (
	emojiAnimationClock AnimationClock
	animationBlocked    atomic.Bool
)

func ContinueAnimationClock(now time.Time) bool {
	return emojiAnimationClock.Continue(now)
}

func StopAnimationClock() {
	emojiAnimationClock.Stop()
}

func SetAnimationBlocked(blocked bool) {
	animationBlocked.Store(blocked)
}

func animationIsBlocked() bool {
	return animationBlocked.Load()
}

func ResetAnimationClockForTest() {
	emojiAnimationClock.resetForTest()
	animationBlocked.Store(false)
	animationStartDispatch = func(send func(any), msg any) {
		go send(msg)
	}
}
