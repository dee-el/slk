# ADR: Animate Custom GIF Emoji

- Status: Accepted
- Date: 2026-07-20
- Repository: `dee-el/slk`
- Target branch: `main`

## Context

Slack custom emoji may be GIF files, such as `:party_parrot:`. `slk` already
fetches GIF bytes and imports Go's GIF decoder, but its shared image fetcher
uses:

```go
img, _, err := image.Decode(file)
```

For GIF input, `image.Decode` returns only the first frame. The fetcher then
stores that frame in the decoded memo, prerenders one PNG payload, and the
kitty renderer uploads it as a static image. Every emoji surface therefore
shows animated custom emoji as a still image.

Current emoji image flow:

1. `emoji.ResolveEmojiToTokens` resolves a shortcode to a CDN URL.
2. `emoji.Place` requests the image with an `E-<hash>` cache key.
3. `image.Fetcher` downloads raw bytes into the shared image cache.
4. `image.Decode` keeps one frame.
5. `KittyRenderer.RenderKey` creates one stable kitty image ID and one
   unicode-placeholder placement.
6. Visible message/thread entries invoke cached `OnFlush` callbacks each
   Bubble Tea frame, but static callbacks emit only once.

The architecture already has two useful properties:

- placeholder lines reference a stable kitty image ID, so pixel data can be
  replaced without rebuilding message text or changing layout;
- visible message/thread entries invoke their flush callbacks on every View,
  so an animated callback can update pixels only while its emoji is visible.

`slk` enables emoji images only when the final protocol is kitty. Ghostty,
kitty, and recent WezTerm use this path. Half-block, sixel, tmux fallback, and
`emoji_images = "off"` currently use text/static fallback and should not gain a
high-frequency animation loop.

Standard Unicode emoji are normally backed by static Slack PNG assets. This
ADR animates custom GIF assets; it does not invent animation for static emoji.

## Decision

Decode and animate custom GIF emoji through demand-driven frame replacement on
the existing kitty image ID.

### Scope

Animation applies to every surface that already calls `emoji.Place`:

- channel message bodies;
- thread parent and replies;
- reaction pills;
- reaction picker where image placement is active;
- compose emoji autocomplete where image placement is active.

Animation does not apply to:

- GIF file attachments or full-screen image preview in this change;
- avatars;
- Block Kit images;
- non-kitty protocols;
- tmux fallback mode;
- static PNG/JPEG/WebP emoji.

When animation cannot be decoded or exceeds safety limits, render the first
frame statically rather than failing the emoji.

### Configuration

Use existing animation switches instead of adding another setting:

- `[animations].enabled = false` disables GIF animation globally;
- `[appearance].emoji_images = "off"` disables all emoji images, including
  GIF animation;
- otherwise animated custom emoji are on by default.

Pass the effective flag into `emoji.PlaceContext`:

```go
type PlaceContext struct {
    Fetcher          PlaceFetcher
    SendMsg          func(msg any)
    AnimationEnabled bool
}
```

### Animated image representation

Add an image-package type that contains fully composited frames:

```go
type Animation struct {
    Frames    []*image.RGBA
    Delays    []time.Duration
    LoopCount int
    Duration  time.Duration
}
```

Invariants:

- `len(Frames) == len(Delays)`;
- every frame has identical full-canvas bounds;
- delays are positive;
- `Duration` is sum of delays;
- frame zero is also the static fallback;
- `LoopCount == 0` means infinite, matching GIF semantics;
- positive loop count freezes on the last frame after final loop.

### GIF detection and decode

Extend `image.FetchRequest`:

```go
type FetchRequest struct {
    // existing fields...
    Animate bool
}
```

`emoji.spawnEmojiFetch` sets `Animate: ctx.AnimationEnabled`. Other image
callers leave it false and retain static behavior.

When `Animate` is true and cached source MIME/extension is GIF:

1. Reject source files larger than 8 MiB for animation; use static decode.
2. Run `gif.DecodeAll` off the Bubble Tea goroutine.
3. Reject animation when canvas exceeds 512x512.
4. Reject animation when frame count exceeds 256.
5. Composite partial GIF frames onto a full RGBA canvas while honoring
   disposal methods:
   - `DisposalNone` / unspecified: retain prior canvas;
   - `DisposalBackground`: clear prior frame rectangle to transparent;
   - `DisposalPrevious`: restore snapshot from before prior frame.
6. Clone each resulting full canvas into `Animation.Frames`.
7. Convert GIF delay units (10 ms) to `time.Duration`.
8. Clamp delays below 20 ms to 100 ms, matching common browser treatment of
   pathological zero-delay GIFs.
9. Store animation in a dedicated in-memory memo keyed by cache key.
10. Continue storing original GIF bytes in existing disk LRU.

If animated decode fails or violates limits, seek/reopen and use current
`image.Decode` static path. Do not negative-cache a valid GIF merely because
animation was rejected.

### Fetcher and prerender integration

Add to `image.Fetcher`:

```go
animations sync.Map // cache key -> *Animation
```

`fetchInner` returns frame zero as `FetchResult.Img` for compatibility and
passes any decoded animation into prerendering:

```go
func (f *Fetcher) maybePrerender(
    key string,
    img image.Image,
    animation *Animation,
    cellTarget image.Point,
)
```

For kitty prerender:

```go
kr.SetSource(key, img)
if animation != nil {
    kr.SetAnimation(key, animation)
}
```

The existing static decoded and prerender memos remain valid. Static callers
still receive frame zero.

### Kitty frame encoding

Extend `KittyRenderer`:

```go
type KittyRenderer struct {
    // existing fields...
    animations       map[string]*Animation
    animationPayload map[animationPayloadKey][]string
    emittedFrame     map[uint32]int
}
```

`animationPayloadKey` scopes pre-encoded frame payloads by stable image ID and
target cell size. Frame payloads are prepared off the UI thread during
prerender:

1. scale each composited frame to target pixel dimensions;
2. PNG-encode each scaled frame;
3. base64-encode once;
4. retain payload slice for the animation lifetime.

This prevents resize, PNG encoding, or base64 work on the Bubble Tea View
path.

`Render` gains metadata only:

```go
type Render struct {
    // existing fields...
    Animated bool
}
```

Animated `RenderKey` behavior:

- placeholder lines and image ID remain stable for all frames;
- `OnFlush` is reusable rather than guarded by one `atomic.Bool`;
- select current frame from monotonic elapsed time and GIF delays;
- emit only when selected frame differs from `emittedFrame[imageID]`;
- use existing chunked kitty upload with the same image ID, replacing pixel
  data under existing unicode placeholders;
- deduplicate repeated occurrences of the same emoji in one View through the
  renderer-level `emittedFrame` map;
- mark animation as visible whenever callback is invoked.

No message-cache invalidation is needed per animation frame. Cached rows keep
the same placeholder bytes.

### Demand-driven animation clock

A permanent 10-20 FPS Bubble Tea tick would waste CPU while `slk` is idle or
animated emoji are off-screen. Add a process-local animation clock in
`internal/emoji`:

```go
type AnimationClock struct {
    mu          sync.Mutex
    tickerOn    bool
    lastVisible time.Time
}

func (c *AnimationClock) MarkVisible(send func(any))
func (c *AnimationClock) Continue(now time.Time) bool
```

Flow:

1. Animated `Place` wraps the renderer flush callback.
2. When a visible entry invokes the callback, `MarkVisible` records activity.
3. If no clock chain exists, it atomically starts one by dispatching
   `EmojiAnimationStartMsg` through `PlaceContext.SendMsg`.
4. UI reducer schedules `emojiAnimationTickMsg` every 50 ms.
5. Each tick causes a normal View; visible animated callbacks choose and emit
   their due frame.
6. Continue ticks while an animated callback was visible within the last
   250 ms.
7. When no animation has been visible for 250 ms, atomically stop chain.
8. If an already-cached animation becomes visible later, its callback starts a
   fresh chain.

The clock uses a mutex for the stop/start race: visibility concurrent with a
stopping tick cannot strand an animation without a future tick.

Tick messages:

```go
type EmojiAnimationStartMsg struct{}
type emojiAnimationTickMsg struct{}
```

App tracks one chain guard as defense in depth:

```go
emojiAnimationTicking bool
```

The reducer starts only when:

- final image protocol is kitty;
- animation config is enabled;
- at least one visible animated callback requested animation.

While a modal overlay is active, tick chain stops. Closing the modal produces
a normal View, and visible emoji restart it. This avoids animating obscured
background images and avoids kitty uploads whose placeholders the overlay has
temporarily blanked.

### Terminal behavior

Use frame replacement rather than kitty's optional native animation commands.
Reason:

- current renderer already supports stable ID replacement;
- Ghostty/kitty/WezTerm support for basic kitty image upload is broader than
  support for every native animation control command;
- app-driven replacement preserves one implementation across supported kitty
  terminals;
- placeholder layout never changes.

If a terminal does not visually replace an existing kitty image ID correctly,
fall back to frame zero for that terminal after manual compatibility testing;
do not rotate IDs because placeholder foreground colors encode the image ID.

## File-by-File Changes

- `internal/image/animation.go` (new)
  - GIF decode limits, disposal-aware frame compositing, delay normalization,
    loop/frame selection.
- `internal/image/animation_test.go` (new)
  - Disposal modes, partial frame rectangles, transparency, delay clamping,
    finite/infinite loops, size/frame limits, static fallback.
- `internal/image/fetcher.go`
  - Add `FetchRequest.Animate`, animation memo, animated decode branch, and
    animated prerender handoff.
- `internal/image/fetcher_test.go`
  - Verify custom GIF loads all frames only when requested; cache-hit behavior;
    malformed/oversized GIF static fallback; no change for GIF attachments.
- `internal/image/renderer.go`
  - Add `Render.Animated`.
- `internal/image/kitty.go`
  - Animation source/payload memos, reusable flush callback, stable-ID frame
    replacement, per-ID frame dedupe.
- `internal/image/kitty_test.go`
  - Stable placeholders, same-ID frame uploads, payload precompute, duplicate
    occurrence dedupe, finite loop freeze, concurrent flush safety.
- `internal/emoji/place.go`
  - Pass animated request flag, wrap animated flushes, add demand clock and
    start message.
- `internal/emoji/place_test.go`
  - Static vs animated requests, clock start dedupe, inactivity stop/restart,
    animation-disabled behavior.
- `internal/ui/msgs.go`
  - Re-export animation start message and add internal tick message.
- `internal/ui/reducer_io.go`
  - Handle start/tick chain without invalidating message caches.
- `internal/ui/app.go`
  - Add animation-chain guard.
- `internal/ui/app_emoji_animation_test.go` (new)
  - Single tick chain, 50 ms cadence, stop off-screen/under modal, restart on
    visibility, no full pane invalidation per frame.
- `cmd/slk/main.go`
  - Pass effective `[animations].enabled` into all emoji place contexts.
- `internal/config/config_test.go`
  - Confirm existing global animation switch controls GIF animation.
- `wiki/Features.md`
  - Document animated custom emoji on kitty-capable terminals.
- `wiki/Terminal-Compatibility.md`
  - Mark animation support for Ghostty/kitty/compatible WezTerm; static
    fallback elsewhere.
- `wiki/Tradeoffs-and-Non-Goals.md`
  - Remove/replace animated-GIF caveat for custom emoji while retaining static
    GIF attachment limitation.

## Verification

Focused tests:

```sh
go test ./internal/image ./internal/emoji ./internal/ui/messages ./internal/ui/thread ./internal/ui
```

Race and full suite:

```sh
go test -race -count=1 ./internal/image ./internal/emoji ./internal/ui/...
go test -race -count=1 ./...
go vet ./...
```

Build:

```sh
make build-macos
otool -L bin/slk | grep AppKit.framework
```

Performance verification:

1. Open channel with 20 repeated instances of one animated custom emoji.
2. Confirm one image payload per changed frame, not one per occurrence.
3. Confirm no message cache rebuild occurs per animation tick.
4. Scroll animated emoji off-screen; confirm tick chain stops within 250 ms.
5. Leave app idle for one minute; confirm no animation ticks or kitty writes.
6. Scroll back; confirm animation restarts without refetch/decode.
7. Open modal; confirm background animation pauses and resumes afterward.
8. Run with race detector while switching channels/workspaces quickly.

Manual compatibility matrix:

1. Ghostty: custom GIF animates in channel body, reaction pill, and thread.
2. kitty: same behavior.
3. recent WezTerm with kitty protocol: same or documented static fallback.
4. tmux: existing half-block/static fallback, no corruption.
5. Alacritty/iTerm fallback: static/text emoji, no tick chain.
6. `[animations].enabled = false`: frame zero only.
7. `[appearance].emoji_images = "off"`: existing text/glyph behavior.

## Rollout

1. Implement directly on fork `main`; no feature branch.
2. Keep this ADR tracked with implementation history.
3. Verify Ghostty first because it is the user's active terminal.
4. Run full race/vet/macOS CGO build.
5. Build fork version with commit/date metadata.
6. Replace `~/bin/slk`.
7. Commit and push feature files directly to `origin/main`.

## Risks and Mitigations

- High idle CPU: demand-driven tick chain stops after 250 ms off-screen.
- Full message rerender at 20 FPS: stable placeholder and reusable flush avoid
  cache invalidation per frame.
- Duplicate emoji multiply terminal writes: renderer dedupes by image ID and
  selected frame.
- GIF disposal rendered incorrectly: explicit full-canvas compositor tests for
  background/previous semantics.
- GIF memory bomb: 8 MiB source, 512x512 canvas, and 256-frame animation caps;
  static fallback on rejection.
- PNG/base64 work blocks UI: precompute scaled payloads in fetch/prerender
  goroutine.
- Tick start/stop race: one mutex-owned clock state plus App guard.
- Modal kitty corruption: pause animation while overlays blank placeholders.
- Terminal does not replace same ID: compatibility test before rollout; static
  fallback rather than ID rotation.
- Shared cache key previously decoded static: animated request checks raw cached
  GIF and fills animation memo even when static decoded memo exists.
- Finite-loop GIF behavior: honor loop count and freeze final frame.

## Post-Implementation Deadlock Correction

The first implementation dispatched `EmojiAnimationStartMsg` synchronously
from the animated emoji `OnFlush` callback. `OnFlush` runs inside Bubble Tea's
`View` call on the main event-loop goroutine. Bubble Tea v2 `Program.Send`
writes to the program's message channel and blocks until the event loop receives
it; the event loop cannot receive while it is still executing `View`. A visible
animated emoji therefore deadlocked startup/rendering.

Corrected decision:

- `AnimationClock.MarkVisible` records visibility and atomically claims the
  single ticker-start transition synchronously.
- Actual external `SendMsg(EmojiAnimationStartMsg{})` runs in one goroutine
  after the claim.
- At most one goroutine is created per stopped-to-running clock transition;
  per-frame flushes while ticker is active create none.
- Tests use channels/atomics and wait for asynchronous delivery; plain mutable
  counters are forbidden because race-enabled tests must cover this path.
- Add a regression test with an unbuffered send callback: animated flush must
  return promptly even when no receiver is ready. This models Bubble Tea's
  `Program.Send` behavior and prevents reintroducing a View-loop deadlock.
- Program shutdown is safe because Bubble Tea `Program.Send` returns when the
  program context is done.

## Alternatives Rejected

- Keep first-frame behavior: current bug/limitation.
- Native kitty animation commands: less portable across Ghostty/WezTerm kitty
  protocol subsets and larger protocol change.
- Rebuild all message caches every frame: high CPU and input latency.
- Allocate a new kitty image ID per frame: placeholder lines encode ID and would
  require cache rebuild/layout churn.
- Permanent global 20 FPS ticker: drains CPU even when animations are hidden.
- Animate all GIF attachments in same change: much larger rendering, viewport,
  memory, and protocol scope.
- Decode/encode frame inside View: blocks Bubble Tea event loop.

## Open Questions

None blocking. Scope is animated custom GIF emoji on kitty-protocol surfaces,
with static fallback elsewhere and existing `[animations].enabled` as the
master switch.
