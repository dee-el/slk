# ADR: Add Authenticated Direct File Downloads

- Status: Proposed
- Date: 2026-07-20
- Repository: `dee-el/slk`
- Target branch: `main`

## Context

Non-image Slack attachments currently render as a filename and an underlined
workspace permalink, for example:

```text
[File] https://workspace.slack.com/files/U123/F123/production-debug.apk
```

The user can press `o` or Command-click the link to open Slack in a browser,
then download from the authenticated web session. `slk` cannot save the file
directly.

The message model currently keeps only one attachment URL:

```go
type Attachment struct {
    Kind string
    Name string
    URL  string // permalink for files; browser/thumbnail URL for images
}
```

`cmd/slk.extractAttachments` receives both `slack.File.Permalink` and
`slack.File.URLPrivate`, but deliberately keeps the permalink for non-image
display. The direct authenticated URL is discarded before the UI receives the
message.

The existing image fetcher already proves the required Slack authentication
scheme:

- exact `files.slack.com` host validation;
- `Authorization: Bearer <xoxc-token>`;
- `Cookie: d=<workspace-cookie>`;
- workspace ID extraction from the URL;
- fallback across authenticated workspaces for Slack Connect files;
- learned workspace-to-auth mapping;
- HTTP 429 backoff.

That fetcher is image-specific: it reads the entire response into memory,
requires an `image/*` content type, decodes pixels, and writes into the image
cache. General files such as APKs, ZIPs, PDFs, and videos must instead stream
to a user-owned download directory without entering the image cache or memory.

The fork's `main` branch is authoritative and intentionally allowed to drift
from upstream.

## Decision

Add direct, authenticated attachment downloads from Normal mode.

### User experience

- `d` on a selected channel message or thread reply starts file download.
- If the message has no downloadable attachments, show `No files to download`.
- If it has one attachment, download immediately.
- If it has multiple attachments, open a `Download file` picker:
  - `j` / `k` or arrows move selection;
  - `Enter` downloads the highlighted file;
  - `Esc` / `q` cancels;
  - rows show filename and formatted size when known.
- Downloads go to `~/Downloads`.
- Create `~/Downloads` with mode `0755` when missing.
- Never overwrite an existing file. Resolve conflicts as
  `production-debug (1).apk`, `production-debug (2).apk`, and so on.
- While active, status shows `Downloading production-debug.apk...`.
- Success shows `Downloaded: ~/Downloads/production-debug.apk`.
- Failure shows `Download failed: <short reason>` and removes partial data.
- One download may run at a time. Pressing `d` during an active download shows
  `Download already in progress`.
- Browser opening through `o` and clickable links remains unchanged.
- Images are downloadable too. `O` / `v` still opens image preview; `d`
  downloads original bytes when Slack provides `url_private`.

### Attachment metadata

Extend `messages.Attachment` so display/open behavior stays separate from
download behavior:

```go
type Attachment struct {
    Kind        string
    Name        string // display title
    URL         string // browser/open URL
    DownloadURL string // Slack url_private; never rendered directly
    Filename    string // original Slack filename used for local save
    Size        int64

    FileID string
    Mime   string
    Thumbs []ThumbSpec
}
```

`cmd/slk.extractAttachments` sets:

```go
att := messages.Attachment{
    Kind:        kind,
    Name:        displayName,
    URL:         pickAttachmentURL(f, kind),
    DownloadURL: f.URLPrivate,
    Filename:    f.Name,
    Size:        int64(f.Size),
}
```

Attachments without `URLPrivate` remain browser-openable but are excluded from
direct download selection.

### Shared Slack file authentication

Extract reusable auth resolution from `internal/image/fetcher.go` into a small
`internal/slackfile` package instead of duplicating token/cookie logic.

```go
type Auth struct {
    TeamID  string
    Token   string
    DCookie string
}

type AuthResolver struct {
    authsByTeam map[string]Auth
    fallbacks   []Auth
    learned     sync.Map
}

func NewAuthResolver(auths []Auth) *AuthResolver
func (r *AuthResolver) AuthsForURL(rawURL string) ([]Auth, error)
func (r *AuthResolver) Remember(rawURL string, auth Auth)
```

Security contract:

- Parse with `net/url`.
- Require HTTPS.
- Require hostname exactly `files.slack.com`; reject suffix, user-info, and
  lookalike hosts.
- Require recognized Slack file path prefixes (`/files/`, `/files-pri/`, or
  `/files-tmb/`).
- Never return authenticated credentials for another host.
- Redirects may continue only to HTTPS `files.slack.com`; cross-host redirects
  fail before credentials can be forwarded.

For compatibility, `internal/image.TeamAuth` becomes a type alias to
`slackfile.Auth`. Image fetcher uses the shared resolver but retains its image
decode/cache behavior and existing public API.

### Streaming downloader

Add `internal/slackfile/downloader.go`:

```go
type Downloader struct {
    http     *http.Client
    auth     *AuthResolver
    retries  int
}

type Request struct {
    URL      string
    Filename string
    Size     int64
    Dir      string
}

type Result struct {
    Path  string
    Bytes int64
}

func (d *Downloader) Download(ctx context.Context, req Request) (Result, error)
```

Download algorithm:

1. Validate direct URL before resolving auth.
2. Sanitize `Filename` with `filepath.Base`; reject empty, `.`, path
   separators, NUL, and control characters.
3. Create destination directory if missing.
4. Pick an unused final filename without overwriting existing files.
5. Create a random temporary file inside destination directory with mode
   `0600`.
6. GET with browser transport plus one candidate workspace auth.
7. On 401/403 or HTML login response, remove/truncate the temporary file and
   try next workspace auth.
8. On 429, honor `Retry-After` when valid; otherwise use bounded exponential
   backoff matching image fetch behavior.
9. Stream response with `io.CopyBuffer`; never call `io.ReadAll`.
10. If metadata size is positive, require downloaded byte count to match.
11. `Sync`, close, then atomically rename temporary file to final path.
12. Remove temporary file on every error or context cancellation.
13. Remember successful fallback auth for future Slack Connect files.

Ignore server `Content-Disposition` filenames. Slack message metadata is the
trusted source, and sanitizing one caller-supplied name is safer than resolving
two potentially conflicting names.

### UI callback and messages

Follow existing uploader wiring: network and filesystem work stay outside the
App reducer.

```go
type DownloadFile struct {
    URL      string
    Filename string
    Size     int64
}

type DownloadFunc func(file DownloadFile) tea.Cmd

type DownloadStartedMsg struct{ Filename string }
type DownloadResultMsg struct {
    Path string
    Err  error
}
```

`cmd/slk/main.go` constructs one shared `slackfile.AuthResolver`, gives it to
both image fetcher and generic downloader, and wires `App.SetDownloader`.

App fields:

```go
downloader         DownloadFunc
downloadInProgress bool
downloadPicker     *downloadpicker.Model
```

`DownloadResultMsg` clears the in-progress guard and updates status. The
command uses a bounded context timeout of 30 minutes; no whole-file memory
buffer exists.

### Multi-attachment picker

Add `internal/ui/downloadpicker`, following `linkpicker` conventions:

```go
type Item struct {
    Name string
    Size int64
    File DownloadFile
}
```

Add `ModeDownloadPicker`, mode handler, overlay rendering, mouse box geometry,
and wheel navigation. The picker is read-only and does not interact with the
compose attachment picker.

### Selected-message lookup

Add one App helper that returns the selected message from either supported
panel:

```go
func (a *App) selectedMessage() (messages.MessageItem, bool)
```

Use it for direct download selection. Do not change existing thread-open,
reaction, edit, or link-open behavior in this ADR.

## File-by-File Changes

- `internal/slackfile/auth.go` (new)
  - Shared exact-host validation, auth selection, Slack Connect fallback, and
    learned mapping.
- `internal/slackfile/auth_test.go` (new)
  - Exact-host, path-prefix, lookalike URL, foreign-team fallback, and learned
    auth tests.
- `internal/slackfile/downloader.go` (new)
  - Streaming authenticated download, retry, collision-safe paths, temporary
    file lifecycle, atomic rename.
- `internal/slackfile/downloader_test.go` (new)
  - Auth headers, streaming, 401/403 fallback, HTML login rejection, 429,
    redirects, cancellation, size mismatch, conflict naming, and cleanup.
- `internal/image/fetcher.go`
  - Replace private auth maps/resolution with shared `slackfile.AuthResolver`;
    keep image-only response checks and cache pipeline.
- `internal/image/fetcher_test.go`
  - Preserve all existing image authentication and Slack Connect behavior.
- `internal/ui/messages/model.go`
  - Add `DownloadURL`, `Filename`, and `Size` to `Attachment`.
- `cmd/slk/main.go`
  - Preserve `URLPrivate`, original filename, and size in UI attachments;
    construct and wire downloader.
- `cmd/slk/attachments_test.go`
  - Verify browser URL remains permalink while direct URL is retained.
- `internal/ui/downloadpicker/model.go` (new)
  - Multi-attachment selection modal.
- `internal/ui/downloadpicker/model_test.go` (new)
  - Navigation, selection, empty state, size formatting, and bounds.
- `internal/ui/mode.go`
  - Add `ModeDownloadPicker` as modal.
- `internal/ui/keys.go`
  - Add `d` binding with `download attachment` help text.
- `internal/ui/mode_normal.go`
  - Route `d` from channel or thread selection.
- `internal/ui/mode_download_picker.go` (new)
  - Picker cancel/select behavior and download dispatch.
- `internal/ui/mode_handlers.go`
  - Register download picker mode.
- `internal/ui/callbacks.go`
  - Add `DownloadFile` and `DownloadFunc` contracts.
- `internal/ui/msgs.go`
  - Add download lifecycle messages.
- `internal/ui/app.go`
  - Downloader state, selected attachment helpers, and setter.
- `internal/ui/reducer_download.go` (new)
  - Download result and toast handling.
- `internal/ui/reducer_modal_click.go`
  - Register download picker geometry and row activation.
- `internal/ui/view_overlays.go`
  - Render download picker overlay.
- `internal/ui/app_download_test.go` (new)
  - Channel/thread routing, no files, single file, multiple picker, concurrent
    guard, success/failure, and original display-link preservation.
- `README.md`, `wiki/Features.md`, `wiki/Keybindings.md`
  - Document `d`, destination, collision behavior, and browser fallback.
- `wiki/Tradeoffs-and-Non-Goals.md`
  - Mark direct file uploads/downloads as delivered where appropriate.

## Build and Verification

Focused tests:

```sh
go test ./internal/slackfile ./internal/image ./internal/ui/downloadpicker ./internal/ui ./cmd/slk
```

Full verification:

```sh
go test -race -count=1 ./...
go vet ./...
make build-macos
otool -L bin/slk | grep AppKit.framework
```

Manual smoke:

1. Select a message with one APK/PDF attachment and press `d`.
2. Confirm bytes land in `~/Downloads` and file opens normally.
3. Download same file again; confirm numbered suffix and no overwrite.
4. Select a message with multiple attachments; choose each through picker.
5. Repeat from thread panel.
6. Download original image while retaining `O` preview behavior.
7. Download Slack Connect attachment requiring fallback auth.
8. Disconnect network mid-download; confirm no partial final file remains.
9. Press `o`; confirm browser opening remains unchanged.
10. Test hostile/lookalike URL metadata; confirm request is rejected and no
    token/cookie reaches foreign host.

## Rollout

1. Implement directly on fork `main`; no feature branch.
2. Keep this proposed ADR tracked until accepted, rejected, or superseded.
3. Run full race, vet, and macOS CGO build verification.
4. Build fork version with commit/date metadata.
5. Replace `~/bin/slk`.
6. Commit and push feature files directly to `origin/main`.

## Risks and Mitigations

- Credential leak through crafted URL: exact HTTPS host/path validation before
  auth selection; reject cross-host redirects.
- Large files exhausting memory: stream with fixed-size buffer.
- Partial/corrupt final files: temporary file, byte-count check, sync, atomic
  rename, cleanup on all failures.
- Existing file overwritten: reserve collision-free final path; never truncate
  user files.
- Slack Connect auth uncertainty: preserve ordered fallback and learned auth
  behavior already proven by image fetcher.
- Image regression from auth extraction: keep image fetcher API stable and run
  complete existing image race tests.
- Token expiry or Slack HTML login page: reject HTML as auth failure and surface
  actionable failure toast.
- Filename traversal: ignore Content-Disposition and sanitize Slack metadata
  filename to one basename.
- Disk full/permission failure: return error, delete partial temp file, keep UI
  responsive.

## Alternatives Rejected

- Keep browser-only flow: works but forces context switching and extra clicks.
- Shell out to `curl`: leaks process arguments/environment risk, complicates
  cross-platform behavior, and bypasses shared retry/auth safeguards.
- Download through image fetcher unchanged: reads whole file into memory and
  rejects non-image content.
- Reuse browser permalink as download URL: returns Slack UI/HTML, not original
  bytes.
- Overwrite same-name files: unsafe and surprising.
- Prompt for destination on every download: too much friction for primary
  workflow; stable `~/Downloads` plus collision suffix is predictable.

## Open Questions

None blocking. Defaults are `d`, `~/Downloads`, one active download, automatic
collision suffixes, and picker only when selected message has multiple files.
