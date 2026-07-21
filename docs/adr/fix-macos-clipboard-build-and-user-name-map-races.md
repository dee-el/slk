# ADR: Fix macOS Clipboard Builds and User-Name Map Races

- Status: Accepted
- Date: 2026-07-17
- Repository: `dee-el/slk`
- Base: `v0.10.0` / `f60601f`

## Context

Two defects were reproduced against the v0.10.0 release.

### macOS clipboard disabled in release binaries

`slk` uses `golang.design/x/clipboard` for clipboard images and text. On macOS,
that package requires CGO. The current GoReleaser build forces
`CGO_ENABLED=0` for every target:

```yaml
builds:
  - id: slk
    env:
      - CGO_ENABLED=0
```

The released v0.9.0 and v0.10.0 macOS binaries therefore log:

```text
Warning: clipboard init failed (clipboard: cannot use when CGO_ENABLED=0); Ctrl+V image paste disabled
```

`smartPaste` returns immediately when `clipboardAvailable` is false, so both
clipboard images and Ctrl+V text fallback appear completely dead. A local
v0.10.0 build with `CGO_ENABLED=1` links Cocoa/AppKit and restores clipboard
behavior.

Changing only `.goreleaser.yaml` to `CGO_ENABLED=1` is not sufficient because
the current release job runs on Ubuntu and cannot compile macOS CGO binaries
without an Apple SDK and target compiler.

### Concurrent user-name map panic

The locally built app later crashed with:

```text
internal/runtime/maps.(*Iter).Next
github.com/gammons/slk/internal/ui/threadsview.stringMapsEqual
github.com/gammons/slk/internal/ui/threadsview.(*Model).SetUserNames
github.com/gammons/slk/internal/ui.(*App).renderThreadsViewPanel
```

One mutable `map[string]string` is currently shared between workspace startup
and network goroutines, `App.userNames`, every messages window, the threads
view, and the thread panel. Writers mutate the shared map while rendering and
background commands iterate or read it.

The alias also causes stale UI state even when no panic occurs:

- `threadsview.SetUserNames` may compare a map with itself after in-place
  mutation, incorrectly skip its version bump, and reuse stale panel output.
- `UserResolvedMsg` patches each window sequentially. The first model mutates
  the shared map; later models see the new value and return before repairing
  authored rows or invalidating caches.
- A thread export command can retain a map that is mutated after dispatch.

## Decision

Implement both fixes in one fork branch because the usable macOS binary is
needed to validate the runtime race fix.

### 1. Build macOS releases natively with CGO

Keep Linux and Windows builds static. Build Darwin targets with CGO on an
Intel macOS GitHub runner, where Apple SDKs are available. Run the complete
GoReleaser job on `macos-15-intel` so packaging, checksums, GitHub release,
and Homebrew formula generation remain one atomic GoReleaser operation.

Split GoReleaser builds by platform:

```yaml
builds:
  - id: slk-static
    main: ./cmd/slk
    binary: slk
    env:
      - CGO_ENABLED=0
    goos: [linux, windows]
    goarch: [amd64, arm64]
    ignore:
      - goos: windows
        goarch: arm64

  - id: slk-darwin
    main: ./cmd/slk
    binary: slk
    env:
      - CGO_ENABLED=1
    goos: [darwin]
    goarch: [amd64, arm64]
    overrides:
      - goos: darwin
        goarch: amd64
        env:
          - CGO_ENABLED=1
          - CC=./scripts/clang-darwin-amd64
      - goos: darwin
        goarch: arm64
        env:
          - CGO_ENABLED=1
          - CC=./scripts/clang-darwin-arm64
```

Compiler wrappers:

```sh
#!/bin/sh
exec clang -arch x86_64 "$@"
```

```sh
#!/bin/sh
exec clang -arch arm64 "$@"
```

Both build IDs use the existing `-trimpath` and version `ldflags`. The archive
configuration explicitly includes both IDs so filenames remain unchanged.

The workflow runner changes from `ubuntu-latest` to `macos-15-intel`. Before
release, it runs a snapshot build and validates both Darwin binaries:

```sh
file dist/slk-darwin_darwin_amd64_v1/slk
file dist/slk-darwin_darwin_arm64_v8.0/slk
otool -L dist/slk-darwin_darwin_amd64_v1/slk | grep AppKit
otool -L dist/slk-darwin_darwin_arm64_v8.0/slk | grep AppKit
```

Exact dist paths must be taken from `dist/artifacts.json`, not hardcoded, in
the final verification script. `go version -m` and `slk --version` validate
metadata. GoReleaser Pro split/merge will not be used.

### 2. Replace shared mutable user-name maps with owned snapshots

Use two ownership layers.

#### Workspace layer

Add a synchronized `userNameStore` in `cmd/slk`:

```go
type userNameStore struct {
    mu    sync.RWMutex
    names map[string]string
}

func (s *userNameStore) Get(id string) (string, bool)
func (s *userNameStore) Set(id, name string)
func (s *userNameStore) Merge(names map[string]string)
func (s *userNameStore) Snapshot() map[string]string
```

Rules:

- `Get`, `Set`, and `Merge` are mutex-protected.
- `Snapshot` returns a clone. Callers may retain it but must treat it as
  immutable.
- `WorkspaceContext` owns `*userNameStore`, not a public mutable map.
- Background `users.list`, cached lookup, RTM, unresolved-DM, search,
  history-fetch, notification, and send/reply paths use store methods.
- Reducer messages receive snapshots, never the canonical map.
- Remove memoization writes to command-local snapshots.

This fixes both the UI crash and remaining backend read/write races around the
same canonical map.

#### UI layer

`App.SetUserNames` clones its input once and distributes that immutable
snapshot to all UI models:

```go
func cloneStringMap(src map[string]string) map[string]string

func (a *App) SetUserNames(names map[string]string) {
    owned := cloneStringMap(names)
    a.userNames = owned
    a.threadsView.SetUserNames(owned)
    for _, m := range a.allWinModels() {
        m.SetUserNames(owned)
    }
    a.threadPanel.SetUserNames(owned)
    // rebuild pickers from owned
}
```

`UserResolvedMsg` performs an explicit App-level copy-on-write update before
patching message rows:

```go
names := cloneStringMap(a.userNames)
names[m.UserID] = m.DisplayName
a.SetUserNames(names)
for _, model := range a.allWinModels() {
    model.PatchUserName(m.UserID, m.DisplayName)
}
a.threadPanel.PatchUserName(m.UserID, m.DisplayName)
```

`messages.Model.PatchUserName` and `thread.Model.PatchUserName` must not mutate
a shared input map. They clone before changing map contents. Row repair and
cache invalidation are tracked separately from map-value changes so a model
still repairs stale authored rows when the map already contains the resolved
name.

Remove render-time user-name synchronization from
`renderThreadsViewPanel`. `App.SetUserNames` becomes the only bulk fan-out
point; rendering becomes read-only:

```go
func (a *App) renderThreadsViewPanel(...) string {
    a.threadsView.SetSelfUserID(a.currentUserID)
    tvVersion := a.threadsView.Version()
    // render/cache only
}
```

`threadsview.SetUserNames` retains equality-based version suppression, but it
only compares immutable snapshots. No mutex belongs in render code.

### 3. Cover sibling maps separately

`UserNamesByHandle`, `BotUserIDs`, `externalUsers`, and channel-name maps have
similar alias potential but are not in the captured panic. During
implementation, run race tests and fix any reported sibling race using the
same ownership pattern. Do not expand into an unrelated state-management
rewrite without a reproduced race.

## File-by-File Changes

### Release and build

- `.goreleaser.yaml`
  - Split static and Darwin builds.
  - Add Darwin CGO overrides.
  - Include both build IDs in archives and relevant package definitions.
- `.github/workflows/release.yml`
  - Run on Intel macOS.
  - Add snapshot/CGO validation before publish or an equivalent validation
    step that does not publish twice.
- `scripts/clang-darwin-amd64`
  - Add executable clang wrapper using `-arch x86_64`.
- `scripts/clang-darwin-arm64`
  - Add executable clang wrapper using `-arch arm64`.
- `.github/workflows/ci.yml`
  - Add a macOS build job with `CGO_ENABLED=1` and a clipboard-linkage smoke
    check so release configuration cannot silently regress.
- `Makefile`
  - Add `build-macos` or `verify-macos-clipboard-build` target for local
    reproduction.

### Workspace user-name ownership

- `cmd/slk/user_names.go` (new)
  - Add mutex-protected `userNameStore` and clone helper.
- `cmd/slk/user_names_test.go` (new)
  - Test snapshot immutability, merge semantics, and concurrent Get/Set/
    Snapshot operations.
- `cmd/slk/main.go`
  - Replace `WorkspaceContext.UserNames map[string]string` with store.
  - Convert all canonical reads/writes to `Get`, `Set`, `Merge`, or
    `Snapshot`.
  - Send only snapshots through Bubble Tea messages and async commands.
- `cmd/slk/channelitem.go`
  - Read names through store API or a stable snapshot passed by caller.
- `cmd/slk/search_workspace_test.go` and related main-package tests
  - Adapt fixtures and assert no mutation of snapshots.

### UI snapshot ownership

- `internal/ui/app.go`
  - Clone input in `SetUserNames`.
  - Add App-level copy-on-write scalar patch helper.
- `internal/ui/reducer_workspace.go`
  - Update App canonical snapshot on `UserResolvedMsg`, then repair rows in
    every window and thread model.
- `internal/ui/view_messages.go`
  - Remove `threadsView.SetUserNames` from render path.
- `internal/ui/messages/model.go`
  - Make `PatchUserName` copy-on-write and independently repair rows.
- `internal/ui/thread/model.go`
  - Apply same copy-on-write and row-repair behavior.
- `internal/ui/threadsview/model.go`
  - Document immutable-snapshot contract; keep equality optimization.
- `internal/ui/app_test.go`
  - Assert `SetUserNames` clones caller input and scalar resolution updates
    App state plus pickers.
- `internal/ui/fanout_global_test.go`
  - Assert scalar name resolution repairs authored rows and invalidates every
    window, not only first window.
- `internal/ui/messages/model_test.go`
  - Assert patch does not mutate caller snapshot and repairs rows even when
    map value already matches.
- `internal/ui/thread/model_test.go`
  - Assert same behavior and old exported snapshot remains unchanged.
- `internal/ui/threadsview/model_test.go`
  - Assert changed immutable snapshot bumps version and repeated identical
    snapshot is a no-op.

## Verification

Focused tests:

```sh
go test ./cmd/slk ./internal/ui ./internal/ui/messages ./internal/ui/thread ./internal/ui/threadsview
```

Race tests:

```sh
go test -race -count=1 ./internal/ui/... ./cmd/slk
go test -race -count=50 -run 'Test(SetUserNames|UserResolved|PatchUserName|UserNameStore)' \
  ./internal/ui ./internal/ui/messages ./internal/ui/thread ./internal/ui/threadsview ./cmd/slk
go test -race -count=1 ./...
```

Full verification:

```sh
go test ./...
go vet ./...
goreleaser check
goreleaser release --snapshot --clean
```

macOS binary checks:

```sh
CGO_ENABLED=1 go build -o /tmp/slk-cgo ./cmd/slk
otool -L /tmp/slk-cgo | grep -E 'Cocoa|AppKit'
/tmp/slk-cgo --version
```

Manual smoke:

1. Copy a PNG to macOS clipboard.
2. Run built binary in Ghostty.
3. Enter INSERT mode and press Ctrl+V; attachment chip appears.
4. Send image, leave INSERT mode, select image message, press `v`; preview
   opens.
5. Open Threads view while users resolve in background; scroll and switch
   channels/workspaces for at least several minutes under a `-race` build.
6. Confirm no concurrent-map panic or race warning.

## Rollout

1. Implement on a fork branch from `main`.
2. Build and install a local CGO binary at `~/bin/slk` for smoke testing.
3. Keep existing installed binary available until smoke tests pass.
4. Commit only source, tests, scripts, and workflows. Keep this ADR local.
5. Push branch to `dee-el/slk`.
6. Do not create a release tag until workflow snapshot artifacts prove both
   Darwin architectures link AppKit and Ctrl+V works.
7. Open an upstream PR only after fork validation.

## Risks and Mitigations

- **Intel macOS runner availability:** GitHub runner labels can change.
  Confirm `macos-15-intel` availability before implementation; pin a supported
  Intel label rather than silently falling back to ARM.
- **Cross-architecture clang behavior:** Verify both Mach-O architectures with
  `file` and framework linkage with `otool`; do not trust successful `go build`
  alone.
- **GoReleaser packaging from macOS:** Snapshot-test nFPM and archives before
  changing release workflow.
- **Snapshot allocation cost:** Clone only at ownership boundaries and user
  updates, never per render. Benchmark Threads view idle rendering.
- **Large workspace write cost:** Workspace store uses mutex-protected in-place
  canonical writes; immutable clones are made only for messages/UI snapshots.
- **Incomplete race cleanup:** Run broad `-race` suite and manual race build;
  fix reproduced sibling races before push.
- **Behavioral stale-cache regression:** Tests must assert model versions and
  authored rows across multiple windows and thread panel.

## Alternatives Rejected

- Set `CGO_ENABLED=1` in current Ubuntu GoReleaser job: cannot compile macOS
  CGO without Apple toolchain/SDK.
- GoReleaser split/merge: requires GoReleaser Pro.
- Keep release binaries static and require users to build locally: leaves a
  documented feature dead in official macOS downloads.
- Add mutex inside `threadsview.stringMapsEqual`: treats one crash site while
  preserving shared mutable aliases and stale-cache bugs.
- Clone only in `threadsview.SetUserNames`: source map can mutate during clone,
  and cloning during every render is too expensive.
- Clone only in `App.SetUserNames`: contains the captured UI panic but leaves
  workspace background races and model-to-model alias mutation.
- Replace all maps with `sync.Map`: weakens typed snapshot semantics and does
  not solve cache invalidation or immutable async payloads.

## Open Questions for Implementation Day

1. Confirm GitHub currently provides `macos-15-intel` to this public fork; if
   not, select the newest supported Intel label.
2. Confirm GoReleaser Community can generate nFPM packages on macOS. If not,
   keep GoReleaser publishing on Ubuntu and replace the release pipeline with
   explicit native-build artifacts plus a final custom publisher job.
3. Decide whether sibling `UserNamesByHandle` and `BotUserIDs` races reproduce
   under `go test -race`; include them only when evidence exists.
4. Confirm upstream expects one PR or separate runtime/build PRs. Fork branch
   may contain both, but upstream submission can split commits cleanly.
