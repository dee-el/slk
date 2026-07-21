# ADR: Add Multi-File Attachment Picker

- Status: Accepted
- Date: 2026-07-18
- Repository: `dee-el/slk`
- Target branch: `main`

## Context

`slk` can already upload files, but attaching a local file requires copying an
absolute path to the clipboard and pressing Ctrl+V. This is technically
functional but difficult to discover and cumbersome for repeated or multiple
attachments.

The existing compose model already supports multiple pending attachments:

```go
type PendingAttachment struct {
    Filename string
    Bytes    []byte
    Path     string
    Mime     string
    Size     int64
}
```

The upload pipeline sends every pending attachment sequentially and clears the
compose only after success. The missing piece is file discovery and selection.

The repository already depends on Bubble Tea and Bubbles. Bubbles includes a
single-select file picker, but its directory entries and cursor state are
private. Wrapping it cannot render persistent `[x]` markers for files selected
across directories. A dedicated small model is therefore clearer than forking
or patching dependency internals.

The fork intentionally diverges from `gammons/slk`. Work will happen directly
on `dee-el/slk`'s `main` branch. The existing runtime/CGO fixes currently live
on `fix/macos-clipboard-and-user-map-races` and must first be fast-forwarded to
fork `main`.

## Decision

Add a terminal-native, multi-select attachment picker opened from INSERT mode.
No new dependency and no OS-native dialog.

### User experience

- `Ctrl+O` in INSERT mode opens the attachment picker.
- Draft text and existing attachment chips remain unchanged while picker is
  open.
- Picker starts in the last directory used during this process; first use
  starts in the user's home directory.
- `j` / `k` and arrow keys move selection.
- `PageUp` / `PageDown`, `g`, and `G` navigate long directories.
- `Enter`, `l`, or Right opens the highlighted directory.
- `h`, Left, or Backspace goes to the parent directory.
- `Space` toggles the highlighted regular file.
- Selected files render with `[x]`; unselected files render with `[ ]`.
- `a` attaches all selected files and returns to INSERT mode.
- `Esc` cancels and returns to INSERT mode without changing pending
  attachments.
- Footer always shows controls and selection count, for example
  `space toggle  a attach  esc cancel  3/10 selected`.
- Works from channel compose and thread compose. The picker remembers which
  compose opened it.
- Maximum 10 selected files per picker session.
- Existing pending attachments count toward the maximum, preventing more than
  10 total pending files.
- Existing limit of 10 MiB applies to each file.
- Empty, oversized, unreadable, and non-regular files cannot be selected; a
  visible error explains why.
- Selecting the same canonical path twice is a no-op.
- Upload begins only when the user sends the message, preserving current
  attachment-chip behavior.

### Picker model

Add `internal/ui/attachmentpicker` with no Slack or App dependencies.

```go
type Item struct {
    Name  string
    Path  string
    IsDir bool
    Size  int64
}

type Model struct {
    visible          bool
    currentDirectory string
    lastDirectory    string
    items            []Item
    cursor           int
    offset           int
    selected         map[string]Item
    selectedOrder    []string
    maxSelected      int
    maxFileSize      int64
    reservedCount    int
    width            int
    height           int
    errText          string
    readGeneration   uint64
}
```

Directory reads run in a `tea.Cmd` so slow/network-mounted directories do not
block the UI loop. Every read message carries a generation; stale responses
are ignored after rapid navigation.

```go
type directoryLoadedMsg struct {
    generation uint64
    directory  string
    items      []Item
    err        error
}
```

The picker owns navigation and selection only. It returns selected paths to
the App; it does not construct compose attachments or upload files.

Public API:

```go
func New(maxSelected int, maxFileSize int64) *Model
func (m *Model) Open(directory string, reservedCount int) tea.Cmd
func (m *Model) Close()
func (m *Model) Update(tea.Msg) tea.Cmd
func (m *Model) HandleKey(tea.KeyMsg) (Action, tea.Cmd)
func (m *Model) SelectedPaths() []string
func (m *Model) ViewOverlay(width, height int, background string) string
func (m *Model) IsVisible() bool
func (m *Model) LastDirectory() string
```

`Action` is `None`, `Cancel`, or `Attach`. App mode handling owns the mode
transition.

### App integration

Add `ModeAttachmentPicker` as a modal overlay and register
`handleAttachmentPickerMode`.

App fields:

```go
attachmentPicker *attachmentpicker.Model
attachmentTarget Panel
```

Opening:

```go
if code == 'o' && mod == tea.ModCtrl {
    return a.openAttachmentPicker()
}
```

`openAttachmentPicker` resolves the active compose, computes existing pending
attachment count, records `PanelMessages` or `PanelThread`, sets
`ModeAttachmentPicker`, and opens the picker.

Attaching:

```go
for _, path := range a.attachmentPicker.SelectedPaths() {
    attachment, err := pendingAttachmentFromPath(path)
    if err != nil {
        // Keep picker open and show error.
        return nil
    }
    target.AddAttachment(attachment)
}
```

On success, close picker, restore `ModeInsert`, focus the originating compose,
and show `Attached N files` toast. Existing draft and attachments are not
reset.

### Shared path validation

Extract file-path validation currently embedded in
`tryAttachFromClipboard`:

```go
func pendingAttachmentFromPath(path string) (compose.PendingAttachment, error)
```

It performs:

- `filepath.Clean` and absolute-path normalization.
- `os.Stat` to follow symlinks and reject missing/non-regular files.
- empty-file rejection.
- per-file 10 MiB size validation.
- basename, MIME-by-extension, path, and size construction.

Both Ctrl+V path attachment and picker attachment use this helper, keeping
behavior identical.

### Rendering

Add picker rendering to `view_overlays.go` and `overlayActive()`.

Layout:

```text
┌ Attach files ─────────────────────────────────┐
│ /Users/name/Documents                         │
│                                               │
│   [dir]  ..                                   │
│ > [ ]    report.pdf                    1.2 MB │
│   [x]    screenshot.png               420 KB │
│   [ ]    notes.txt                       8 KB │
│                                               │
│ space toggle  a attach  esc cancel  1/10     │
└───────────────────────────────────────────────┘
```

The modal uses existing dimmed-overlay and theme styles. It clamps width and
height for small terminals, scrolls the item list, and never renders beyond
the terminal bounds.

Mouse support, fuzzy filename search, hidden-file toggle, bulk select-all,
directory attachment, drag-and-drop, and native Finder/Explorer dialogs are
out of scope for MVP.

## File-by-File Changes

- `internal/ui/attachmentpicker/model.go` (new)
  - Directory loading, navigation, selection, validation gates, viewport,
    modal rendering.
- `internal/ui/attachmentpicker/model_test.go` (new)
  - Navigation, stale read suppression, multi-select order, duplicate toggle,
    limits, invalid files, scrolling, and rendering tests.
- `internal/ui/mode.go`
  - Add `ModeAttachmentPicker`, modal classification, and display label.
- `internal/ui/mode_handlers.go`
  - Register attachment-picker handler.
- `internal/ui/mode_attachment_picker.go` (new)
  - Handle cancel, attach, navigation, and return-to-compose behavior.
- `internal/ui/mode_insert.go`
  - Bind `Ctrl+O` to open picker before forwarding key to textarea.
- `internal/ui/app.go`
  - Add picker state, origin target, open/close helpers, and shared
    `pendingAttachmentFromPath` validation.
- `internal/ui/reducer_io.go`
  - Forward asynchronous directory-loaded messages to picker even while other
    reducers process background app events.
- `internal/ui/view_overlays.go`
  - Render picker and include it in overlay memoization guard.
- `internal/ui/app_test.go`
  - Channel/thread targeting, draft preservation, existing-attachment limit,
    cancel, successful multi-attach, path validation parity.
- `internal/ui/help/*` or help model source
  - Add INSERT-mode `Ctrl+O` documentation if help content is mode-aware.
- `wiki/Keybindings.md`
  - Document picker controls.
- `wiki/Features.md`
  - Replace path-only smart-paste wording with discoverable multi-file picker.
- `README.md`
  - Add concise attach-files workflow.

## Branch and Commit Workflow

No feature branch.

1. Keep ADR status aligned with implementation state.
2. Switch to local `main`.
3. Fast-forward `main` to `fix/macos-clipboard-and-user-map-races`:

   ```sh
   git switch main
   git merge --ff-only fix/macos-clipboard-and-user-map-races
   git push origin main
   ```

4. Implement and verify directly on `main`.
5. Commit implementation and ADR updates.
6. Push `main` to `dee-el/slk` through `github-personal` SSH.

Expected feature commit:

```text
feat: add multi-file attachment picker
```

## Verification

Focused tests:

```sh
go test ./internal/ui/attachmentpicker ./internal/ui ./internal/ui/compose
```

Race and full suite:

```sh
go test -race -count=1 ./internal/ui/... ./cmd/slk
go test -race -count=1 ./...
go vet ./...
```

Build:

```sh
make build-macos
otool -L bin/slk | grep AppKit.framework
```

Manual smoke:

1. Start `slk`, select channel, press `i`, type draft text.
2. Press Ctrl+O and select files in multiple directories with Space.
3. Attach with `a`; confirm chips appear and draft text remains.
4. Reopen picker; existing pending count reduces available selection slots.
5. Send; confirm every selected file uploads.
6. Repeat from thread compose; confirm files land in thread.
7. Cancel picker; confirm no draft or pending attachment changes.
8. Verify oversized, empty, unreadable, duplicate, and more-than-10 files are
   rejected without closing picker.
9. Resize Ghostty while picker is open; confirm modal remains usable.

## Rollout

1. Fast-forward fork `main` with existing runtime/CGO fixes.
2. Implement feature directly on fork `main`.
3. Build and install new binary to `~/bin/slk` with fork version metadata.
4. Manually smoke-test in Ghostty.
5. Commit and push fork `main`.
6. Keep feature branch unnecessary; fork `main` is authoritative and may drift
   from upstream.

## Risks and Mitigations

- Directory reads can block: execute in `tea.Cmd`; ignore stale generations.
- Files can change between selection and send: uploader already opens at send;
  surface existing open/upload error and preserve pending attachments.
- Large selection can overwhelm compose: cap total pending files at 10.
- Duplicate paths through symlinks: canonicalize with `filepath.EvalSymlinks`
  when possible before selection-key comparison.
- Picker opened from thread then thread closes asynchronously: store target;
  if thread no longer exists on attach, keep picker open and report error.
- Modal may leak keys into compose: dedicated mode captures all key events.
- Background Slack events must continue while picker is open: mode captures
  only keys; normal reducers still process non-key messages.
- Existing Ctrl+O normal-mode behavior: binding applies only to INSERT mode;
  lowercase `o` and uppercase `O` normal-mode actions remain unchanged.
- Hidden/unreadable directories: display read error and allow parent/cancel.

## Alternatives Rejected

- Continue clipboard-path workflow: undiscoverable and cumbersome.
- macOS Finder via AppleScript: best native Mac UX but not cross-platform and
  blocks/complicates terminal process integration.
- Directly wrap Bubbles single-select filepicker: cannot render persistent
  multi-selection markers because its entry/cursor state is private.
- Fork Bubbles filepicker source wholesale: imports unnecessary behavior and
  creates a large maintenance copy for a small feature.
- Upload immediately from picker: breaks caption workflow, retries, and current
  attachment-chip semantics.

## Open Questions

None blocking. Defaults are terminal-native picker, maximum 10 total pending
files, 10 MiB per file, Space toggle, and `a` attach.
