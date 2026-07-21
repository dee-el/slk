# ADR: Make Kitty Image Uploads Atomic

- Status: Accepted
- Date: 2026-07-21
- Repository: `dee-el/slk`
- Target branch: `main`

## Context

Some message avatars intermittently render as a tiny glyph while other avatars
retain the expected 4-column by 2-row footprint. The source images and message
layout are not changing size. The visible glyph is Kitty's Unicode placeholder
remaining unresolved because the terminal did not receive a valid image upload.

Avatar rendering is concurrent. `internal/avatar.Cache` has multiple preload
workers, and animated custom emoji can emit Kitty frame replacements while
avatars load. Every avatar still requests:

```go
target := image.Pt(AvatarCols, AvatarRows) // 4 x 2 cells
```

Kitty payloads larger than 4096 base64 bytes must be split into APC chunks:

```go
func emitKittyUpload(w io.Writer, id uint32, payload string, cols, rows int) error {
    for each 4096-byte chunk {
        writeKittySequence(w, chunkSequence)
    }
}
```

Production output is wrapped by `SerializeOutput`, whose mutex currently covers
one `Write` call:

```go
func (s *serializedWriter) Write(p []byte) (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.w.Write(p)
}
```

One logical image transfer uses multiple `Write` calls, so the lock is released
between continuation chunks. Concurrent uploads can interleave:

```text
image A: first chunk, i=A, m=1
image B: first chunk, i=B, m=1
image A: continuation, m=0
```

Continuation chunks omit image ID and attach to the terminal's active transfer.
Interleaving corrupts one or both images. OS writes still succeed, so the Kitty
registry marks the image uploaded and does not retry. Its 4x2 placeholder then
appears as a small font glyph or empty mark.

High-detail photos compress poorly and exceed one chunk more often than simple
logos/default avatars. This explains user-specific behavior. Animated GIF emoji
adds more concurrent Kitty traffic, increasing collision probability, but does
not change avatar dimensions.

Existing `TestSerializeOutput_NoInterleave` writes one complete byte slice per
goroutine. It proves individual `Write` calls are atomic but does not model a
logical Kitty transfer composed of multiple writes.

## Decision

Assemble every logical Kitty upload into one byte buffer, then submit that full
transfer through the serialized writer with one `Write` call. Make the
serialized writer complete short writes while retaining its mutex.

### Atomic transfer construction

Split Kitty sequence formatting from output. Add a helper that appends a
sequence to an in-memory buffer, including tmux wrapping:

```go
func appendKittySequence(dst *bytes.Buffer, seq string) {
    if inTmux() {
        seq = wrapForTmux(seq)
    }
    dst.WriteString(seq)
}
```

`emitKittyUpload` keeps protocol chunking unchanged but buffers every APC chunk:

```go
func emitKittyUpload(w io.Writer, id uint32, payload string, cols, rows int) error {
    var transfer bytes.Buffer
    for each 4096-byte payload chunk {
        appendKittySequence(&transfer, sequence)
    }
    return writeAll(w, transfer.Bytes())
}
```

Protocol behavior remains unchanged:

- First chunk carries `a=T,f=100,t=d,i=<id>,U=1,c=<cols>,r=<rows>,q=2`.
- Every chunk carries `m=1` except final `m=0`.
- Continuation chunks omit image ID as required by Kitty.
- tmux DCS wrapping remains per APC sequence.
- Empty payload produces no output, matching current behavior.

The complete transfer is bounded by the already-built base64 PNG payload. This
adds one temporary copy per upload but avoids terminal protocol corruption.
Avatar payloads are small; larger inline-image payloads already incur PNG and
base64 buffers, so one transfer buffer is acceptable.

### Serialized writer short-write contract

One caller-level `Write` must remain atomic even if the wrapped writer returns a
short write. Change `serializedWriter.Write` to loop under one lock:

```go
func (s *serializedWriter) Write(p []byte) (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    total := 0
    for len(p) > 0 {
        n, err := s.w.Write(p)
        total += n
        p = p[n:]
        if err != nil {
            return total, err
        }
        if n == 0 {
            return total, io.ErrShortWrite
        }
    }
    return total, nil
}
```

This gives `emitKittyUpload` one atomic transaction at the `SerializeOutput`
boundary and follows `io.Writer` semantics for partial writes.

### Registry behavior

Keep existing registry semantics:

- Mark uploaded only after the complete buffered transfer returns success.
- Do not mark uploaded after short write or output error.
- Animated frame state commits only after complete transfer success.

No terminal acknowledgement is requested (`q=2` remains), so terminal-side
render failures remain unobservable. Atomic output removes the known corruption
source without adding synchronous protocol replies.

### Scope

Apply the fix to shared `emitKittyUpload`; this covers:

- message avatars;
- static inline images;
- static custom emoji;
- animated custom emoji frame replacements;
- image preview uploads using the same renderer.

Do not alter:

- fixed avatar footprint;
- source URL selection (`image_32`, bot icon preference);
- image resize quality;
- disk-cache keys;
- image registry IDs;
- sixel or half-block renderers;
- thread avatar behavior;
- image protocol configuration.

Stale avatar URL/cache invalidation and retry-after-profile-change are separate
issues. They can cause old or missing avatars but do not explain tiny unresolved
Kitty placeholders.

## File-by-File Changes

- `internal/image/kitty.go`
  - Buffer all chunks for one upload and emit as one writer transaction.
  - Preserve tmux wrapping and protocol headers.
- `internal/image/kitty_test.go`
  - Add multi-chunk atomic transfer tests.
  - Add concurrent upload tests with distinct image IDs.
  - Test tmux wrapping, empty payload, errors, and chunk headers.
- `internal/image/renderer.go`
  - Make `serializedWriter.Write` complete short writes under one lock.
- `internal/image/renderer_test.go`
  - Test short-write completion and error/zero-write handling.
  - Replace single-write-only concurrency assumptions with transaction coverage.
- `internal/avatar/kitty_test.go`
  - Add high-entropy 32x32 avatar integration test proving one atomic upload and
    fixed 4x2 placeholder footprint.

No dependency, config, or database changes.

## Verification

Focused tests:

```sh
go test ./internal/image ./internal/avatar
go test -race -count=1 ./internal/image ./internal/avatar
```

Required assertions:

- A payload larger than 4096 bytes produces several Kitty APC chunks but one
  underlying serialized-writer transaction.
- Two concurrent multi-chunk uploads remain complete contiguous transfers;
  chunks from different image IDs never interleave.
- First and continuation headers remain protocol-correct.
- tmux wraps each APC sequence while retaining one outer transaction.
- A short underlying write is completed before another caller can write.
- Zero-byte/no-error writer behavior returns `io.ErrShortWrite` without looping.
- Underlying writer error returns partial count and prevents registry upload mark.
- High-entropy avatar still produces exactly 4x2 placeholder cells and one
  complete multi-chunk upload.
- Existing static, animated, warm-cache, and parity tests remain unchanged.

Full release gate:

```sh
go test -race -count=1 ./...
go vet ./...
make build-macos
otool -L bin/slk | grep AppKit.framework
```

Manual smoke:

1. Run Kitty-capable terminal with `image_protocol = "kitty"` or auto detection.
2. Clear/restart session so avatar registry starts fresh.
3. Open channel containing many distinct high-detail avatars.
4. Trigger animated custom emoji while avatars lazy-load.
5. Confirm every avatar resolves at 4x2 with no tiny placeholder glyphs.
6. Switch channels repeatedly and confirm warm renders remain stable.
7. Repeat inside tmux.

Temporary workaround before release:

```toml
[appearance]
image_protocol = "halfblock"
```

This avoids Kitty side-channel uploads but reduces image fidelity and disables
Kitty-specific animation behavior.

## Rollout

1. Accept this ADR.
2. Implement shared Kitty transfer atomicity.
3. Run focused race tests and full release gate.
4. Complete independent review with no high/medium findings.
5. Commit ADR and implementation as separate commits.
6. Build/install next fork binary with exact commit/date metadata.
7. Push both commits to `origin/main`.
8. Restart `slk` so old registry state is discarded and failed placeholders
   upload again.

## Risks and Mitigations

- Extra transfer-sized allocation: one buffer per upload; acceptable relative to
  existing PNG/base64 payloads and bounded by current image dimensions.
- Large writes block other image uploads longer: intentional serialization;
  protocol correctness requires complete transfer ordering.
- Animated GIF frame traffic waits behind a large static image: frames may skip
  briefly, preferable to corrupting both transfers.
- tmux escaping changes accidentally: preserve per-sequence `wrapForTmux` and
  add exact tests.
- Short-write loop stalls: zero progress returns `io.ErrShortWrite`.
- Existing callers expect one underlying write per APC chunk: no caller relies
  on this internal detail; tests will move to logical-transfer semantics.
- Registry remains stale from failures before upgrade: process restart resets
  in-memory registry and triggers upload again.

## Alternatives Rejected

- Increase avatar source size: improves sharpness but does not fix corrupted
  Kitty transfers.
- Reduce worker count to one: hides avatar/avatar races but animated/static image
  uploads can still interleave and sacrifices useful fetch concurrency.
- Lock each APC chunk: current behavior; continuation chunks remain vulnerable.
- Add global lock inside `emitKittyUpload`: would work but duplicates output
  serialization and misses other callers unless all share same global state.
- Put image ID on continuation chunks: violates Kitty transfer protocol.
- Request terminal acknowledgements: adds asynchronous response handling and
  does not prevent malformed interleaved streams.
- Switch everyone to half-block: avoids bug but loses Kitty quality/animation.
- Retry unresolved placeholders on timer: terminal failure is not observable;
  repeated corrupted uploads can worsen output.

## Open Questions

None blocking. Decision is one buffered writer transaction per logical Kitty
upload, with short writes completed under the existing serialized-output lock.
