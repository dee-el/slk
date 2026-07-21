# ADR: Fix Table Cells and Add Scrollable Viewports

- Status: Accepted
- Date: 2026-07-21
- Repository: `dee-el/slk`
- Target branch: `main`
- Supersedes: narrow-table and non-interactive decisions in
  `docs/adr/render-slack-block-kit-tables.md`

## Context

Block Kit table headers render, but data rows from pasted spreadsheet tables are
blank. Slack emits heterogeneous table cell types:

```json
[
  {"type":"rich_text","elements":[...]},
  {"type":"raw_text","text":"Alice"},
  {"type":"raw_number","value":42},
  null
]
```

The project currently uses `github.com/slack-go/slack v0.23.0`. Its table model
declares every cell as `*RichTextBlock`:

```go
type TableBlock struct {
    Rows [][]*RichTextBlock `json:"rows"`
}
```

`raw_text` and `raw_number` objects therefore decode without rich-text
elements. `parseTableCell` receives a non-nil but empty `RichTextBlock` and
stores an empty string. This matches the screenshot: rich-text header labels
render, while raw data cells are blank.

`slack-go v0.27.0` fixes this upstream with a `TableCell` interface and concrete
`TableRichTextCell`, `TableRawTextCell`, and `TableRawNumberCell` types. It also
preserves `null` as an empty cell.

Current table layout compresses every column toward a 3-column minimum and
switches to a verbose stacked layout when all columns cannot fit. Long tables
also contribute their full height to the outer message viewport. Requested
behavior is a grid with horizontal and vertical scrolling once natural table
dimensions exceed a bounded inline viewport.

Interactive scrolling cannot live only in `blockkit`: renderer is pure and
message output is cached. Main-message and thread models must own table focus
and offsets, while renderer receives immutable viewport input and returns table
regions for focus/navigation.

## Decision

Fix heterogeneous cell decoding by upgrading `slack-go` to `v0.27.0`, then add
a dedicated inline `TABLE` mode and per-pane table viewport state.

### Cell decoding

Update dependency:

```text
github.com/slack-go/slack v0.23.0 -> v0.27.0
```

Convert each upstream cell at parse time:

```go
func parseTableCell(cell slack.TableCell) TableCell {
    switch cell := cell.(type) {
    case nil:
        return TableCell{}
    case *slack.TableRichTextCell:
        return TableCell{Text: RichTextToMrkdwn(
            RichTextBlock{Elements: cell.Elements},
        )}
    case *slack.TableRawTextCell:
        return TableCell{Text: cell.Text}
    case *slack.TableRawNumberCell:
        if cell.Text != "" {
            return TableCell{Text: cell.Text}
        }
        return TableCell{Text: strconv.FormatFloat(
            cell.Value, 'f', -1, 64,
        )}
    default:
        return TableCell{}
    }
}
```

Rules:

- Rich text keeps current mrkdwn reconstruction and host `RenderText` path.
- Raw text is literal text, then passes through normal table sanitization and
  host rendering.
- Raw number uses Slack's display `text` override when present; otherwise use a
  non-exponent, precision-preserving representation where possible through
  `strconv.FormatFloat(value, 'f', -1, 64)`.
- `null` and unknown cells render empty, never panic.
- Existing 100-row and 20-column safety caps remain.
- Add a production-shaped fixture mixing rich text, raw text, raw numbers, and
  null cells. Existing rich-text-only fixture remains.

### Table identity

Retain source `block_id` and assign structural paths during rendering:

```go
type TableBlock struct {
    BlockID       string
    Rows          [][]TableCell
    Columns       []TableColumn
    RowsTruncated bool
    ColsTruncated bool
    SourceRows    int
    SourceCols    int
}

type TableKey struct {
    MessageTS string
    Path      string // blocks/2 or legacy/1/blocks/3
    BlockID   string // advisory; path remains uniqueness source
}
```

`block_id` can be empty or duplicated, so stable state keys use message TS plus
structural path. Block ID is retained only to remap state after harmless block
reordering when unambiguous.

### State ownership

`App` owns mode/key routing only. Each pane model owns its table state:

```go
type TableViewport struct {
    XOffset int // terminal display columns
    YOffset int // rendered physical lines
}

type tableFocus struct {
    Key TableKey
}

tableFocus *tableFocus
tableViews map[TableKey]TableViewport
```

Main messages and thread use separate state. Split windows remain independent;
mutable offsets are not stored in shared immutable `MessageItem.Blocks`.

State rules:

- Clamp offsets on every render and resize.
- Prune entries when source messages or tables disappear.
- Exit table mode if asynchronous selection changes away from source message.
- Preserve offsets when user exits and later re-enters same table.
- Migrate or clear state when an optimistic local message TS is replaced.

### Natural table canvas

Build grid at natural dimensions before clipping:

- Minimum cell width remains 3 display columns.
- Desired column width remains capped at 30 display columns.
- Natural width is sum of desired widths plus outer/internal borders.
- Do not shrink all columns to fit pane width.
- `is_wrapped=true` wraps at 30-column cap.
- `is_wrapped=false` keeps natural content up to 30 columns and truncates beyond
  cap with `...`.
- Multi-line cells still set row physical height.
- Existing ANSI, Unicode, and OSC-8 balancing stays required.

This replaces stacked fallback for normal usable panes. Widths below 4 columns
retain defensive plain/stacked output because a framed viewport cannot render.

### Inline viewport geometry

Use bounded viewport only when natural table exceeds available width or height.
Tables that fit preserve current full grid output and require no focus state.

Geometry:

```go
viewportWidth := blockWidth
maxTableHeight := min(12, max(5, paneContentHeight/2))
viewportHeight := min(naturalHeight, maxTableHeight)
```

- Horizontal overflow starts when natural width exceeds `blockWidth`.
- Vertical overflow starts when natural height exceeds `maxTableHeight`.
- Default vertical cap is 12 physical lines, reduced to half available pane
  height so one table cannot consume most of a short message/thread pane.
- Viewport output height stays constant while offsets change. Outer message
  cache geometry therefore remains stable during inner scrolling.
- `XOffset` and `YOffset` address display columns and physical rendered lines,
  not bytes, runes, logical rows, or source columns.
- Top/bottom status lines show overflow direction and position only when needed:

```text
┌ table  x 1/84  y 1/27  ─────────────────────────────── ▶
│ agent_id │ product │ q3-2025 │ q4-2025 │ q1-2026 │
│ 123      │ Widget  │ 10      │ 12      │ 14      │
└────────────────────────────────────────────────────── ▼
```

- Use ANSI-aware display-column cutting. Never slice bytes or cut inside escape
  sequences.
- Rebalance OSC-8 hyperlinks and styles at each clipped physical line.
- When horizontally offset, viewport frame remains visible; only grid canvas
  inside frame moves.
- Truncation summary stays below viewport and does not scroll with data.

### Renderer contract

Extend blockkit context/result:

```go
type TableViewportInput struct {
    Key       TableKey
    XOffset   int
    YOffset   int
    MaxHeight int
    Focused   bool
}

type TableRegion struct {
    Key        TableKey
    LineStart  int
    LineEnd    int
    FullWidth  int
    FullHeight int
    MaxX       int
    MaxY       int
}

type RenderResult struct {
    // existing fields
    TableRegions []TableRegion
}
```

Renderer remains deterministic: same blocks, dimensions, and viewport input
produce same lines and regions. It does not mutate offsets.

Nested tables inside legacy attachments receive paths such as
`legacy/1/blocks/3`; attachment rendering translates returned line regions by
its local output offset.

### Focus and controls

Add `ModeTable`, displayed as `TABLE` in status bar.

Normal mode:

- `t`: focus first table region in selected message/reply.
- If selected item has no table, focus nearest visible table in focused pane.
- No visible table: no-op with brief status text.
- `Enter` remains thread-open and is not reused.

Table mode:

| Key | Action |
|---|---|
| `h` / Left | Scroll left one display column |
| `l` / Right | Scroll right one display column |
| `j` / Down | Scroll down one physical line |
| `k` / Up | Scroll up one physical line |
| `PgUp` / `PgDn` | Scroll one inner viewport page |
| `Ctrl+U` / `Ctrl+D` | Scroll half inner viewport page |
| `Tab` / `Shift+Tab` | Focus next/previous table in same message |
| `Esc` / `q` | Exit table mode |

Movement keys are consumed at table boundaries. They do not unexpectedly move
message selection or panel focus. Mouse wheel continues scrolling outer pane in
this first implementation; pointer-aware nested scrolling and horizontal wheel
events are deferred.

On focus entry, outer pane scrolls only enough to reveal focused table viewport.
While `TABLE` mode is active, outer pane offset remains unchanged.

### Cache integration

Main message cache:

- Store translated `TableRegion` values on each rendered `viewEntry`.
- Include table height budget in cache identity alongside width.
- Offset changes mark only focused message TS stale and call `dirty()`.
- Focus style changes stale old/new source entries.

Thread cache:

- Store regions for parent and replies.
- Add height budget to reply cache predicate.
- Rebuild only focused parent/reply when offsets change; avoid whole-thread
  rebuild on every keypress.
- Keep parent tables focusable through nearest-visible region lookup despite
  parent not being a selectable reply.

Every state mutation increments pane version so app panel caches invalidate.

### Safety limits

Keep:

```go
tableMaxRows         = 100
tableMaxCols         = 20
tableMinCellWidth    = 3
tableDesiredWidthCap = 30
```

Add:

```go
tableMaxCellRunes = 10_000
tableMaxCellLines = 200
```

Reason: vertical clipping after constructing an unbounded cell still permits a
hostile payload to allocate/render huge intermediate output. Truncated cell
content receives `...` and table summary notes content truncation.

## File-by-File Changes

- `go.mod`, `go.sum`
  - Upgrade `github.com/slack-go/slack` to `v0.27.0`; tidy module graph.
- `internal/ui/messages/blockkit/types.go`
  - Retain table BlockID; add viewport input/key/region types and content-cap
    metadata.
- `internal/ui/messages/blockkit/parse.go`
  - Convert rich-text/raw-text/raw-number/null cells.
- `internal/ui/messages/blockkit/parse_test.go`
  - Test heterogeneous production payload, number text override, null, unknown,
    limits, and BlockID.
- `internal/ui/messages/blockkit/testdata/table_mixed_cells.json`
  - Add pasted-spreadsheet fixture matching reported blank-row shape.
- `internal/ui/messages/blockkit/render.go`
  - Assign structural paths and aggregate table regions.
- `internal/ui/messages/blockkit/table.go`
  - Separate natural canvas construction from ANSI-aware viewport clipping;
    add position indicators and safety caps; retain tiny-width fallback.
- `internal/ui/messages/blockkit/table_test.go`
  - Test horizontal/vertical offsets, boundaries, stable viewport height,
    direction indicators, Unicode, ANSI, OSC-8, resize clamp, and cell caps.
- `internal/ui/messages/blockkit/attachments.go`
  - Propagate nested paths and translate nested table regions.
- `internal/ui/messages/model.go`
  - Own main-pane focus/offset map, region metadata, targeted invalidation,
    height-aware cache key, and visibility adjustment.
- `internal/ui/messages/blockkit_integration_test.go`
  - Test selected-message table focus, scrolling, multiple tables, and stable
    entry geometry.
- `internal/ui/thread/model.go`
  - Own thread focus/offset map, parent/reply regions, targeted invalidation,
    and visibility adjustment.
- `internal/ui/thread/model_test.go`
  - Test reply and parent scrolling parity.
- `internal/ui/mode.go`
  - Add inline `ModeTable` and `TABLE` label.
- `internal/ui/mode_handlers.go`
  - Register table mode handler.
- `internal/ui/mode_table.go`
  - Add focused table key routing.
- `internal/ui/mode_normal.go`
  - Route `t` to table focus in focused message/thread pane.
- `internal/ui/keys.go`
  - Add `FocusTable` binding and keyless table-control help entries.
- `internal/ui/app.go`
  - Route table lifecycle and page distances to focused pane.
- `cmd/slk/cache_render_test.go`
  - Verify cache round-trip preserves mixed table cell values.
- `wiki/Features.md`
  - Document scrollable Block Kit tables and `t` focus.
- `wiki/Terminal-Compatibility.md`
  - Replace stacked-fallback statement with viewport behavior and controls.

No database schema migration is required.

## Verification

Focused tests:

```sh
go test ./internal/ui/messages/blockkit ./internal/ui/messages ./internal/ui/thread ./internal/ui ./cmd/slk
go test -race -count=1 ./internal/ui/messages/blockkit ./internal/ui/messages ./internal/ui/thread ./internal/ui ./cmd/slk
```

Required assertions:

- Reported mixed-cell payload renders every raw text and raw number value.
- Number `text` override wins over numeric value.
- Every visible viewport line stays within exact pane width.
- Offsets clamp after terminal resize and payload update.
- ANSI styles and OSC-8 links close/reopen around horizontal cuts.
- Table output height does not change while scrolling.
- Multiple tables keep independent offsets.
- Main pane, thread reply, and thread parent behave consistently.
- Outer message/reply selection does not move in table mode.
- Existing non-table Slack parsing compiles and passes after dependency upgrade.

Full release gate:

```sh
go test -race -count=1 ./...
go vet ./...
make build-macos
otool -L bin/slk | grep AppKit.framework
```

Manual smoke:

1. Open reported message and confirm all data-row values render.
2. Press `t`; confirm status mode changes to `TABLE`.
3. Resize pane below natural table width; use `h/l` and arrow keys to reveal all
   columns without stacked conversion.
4. Open table longer than viewport cap; use `j/k`, page, and half-page keys.
5. Exit with `Esc`; confirm normal message navigation resumes.
6. Repeat in thread reply and visible thread-parent table.
7. Toggle sidebar/thread and resize terminal; confirm offsets clamp and table
   frame remains valid.

## Rollout

1. Implement directly on fork `main`; keep ADR status synchronized.
2. Upgrade dependency and resolve only concrete compile/API changes.
3. Land decoding fix first in worktree tests, then viewport state/rendering.
4. Run focused race tests during implementation and full release gate at end.
5. Final independent code review must return no high/medium findings.
6. Commit implementation as one feature commit unless dependency upgrade exposes
   unrelated fixes requiring separate commits.
7. Build next fork version with commit/date metadata, install to `~/bin/slk`,
   verify version, and push `main`.

## Risks and Mitigations

- Dependency upgrade changes unrelated Slack types: compile whole repository,
  inspect module diff, and avoid compatibility shims unless a real caller needs
  one.
- Horizontal cuts corrupt ANSI/OSC-8 state: use display-aware ANSI helpers and
  explicit style/link balance tests at every cut offset.
- Nested scrolling confuses navigation: dedicated `TABLE` mode captures keys;
  `Esc` visibly returns to normal mode.
- Tall table changes message height while scrolling: fixed viewport geometry.
- Cached output ignores new offsets: offsets live in pane state, focused entry is
  explicitly invalidated, and pane version increments.
- Multiple tables share offsets: key by message TS plus structural block path.
- Table edit/reorder leaves stale state: retain BlockID for optional remap, prune
  absent paths, clamp every render.
- Huge cells consume memory before clipping: cap cell runes and physical lines.
- Thread parent lacks selection: nearest-visible region lookup makes parent
  tables reachable without changing reply-selection semantics.
- Natural grid can be hundreds of columns wide: existing 20x30 cap bounds canvas
  around 621 display columns plus ANSI sequences.
- Users expect mouse-wheel inner scrolling: document keyboard-first behavior;
  defer pointer hit-testing until keyboard model proves stable.

## Alternatives Rejected

- Patch only current parser with reflection/custom JSON interception: typed raw
  cell support already exists upstream in `slack-go v0.27.0`; local duplication
  would be brittle.
- Keep `v0.23.0` and render empty raw cells: loses user data.
- Continue compressing every column: values become unreadable and columns remain
  inaccessible at narrow widths.
- Keep stacked fallback: preserves data but loses table scanning/alignment and
  does not satisfy horizontal-scroll request.
- Let outer message viewport handle long tables: one table can monopolize many
  screens and has no independent vertical position.
- Reuse `Enter` for table focus: conflicts with established open-thread action.
- Bubble inner movement into outer pane at boundaries: repeated keys cause
  surprising message or panel jumps.
- Store offsets inside parsed blocks: block models are shared across split
  windows and intentionally immutable.
- Add database columns for offsets: viewport position is ephemeral UI state.

## Open Questions

No blocking questions. Proposed defaults are keyboard-first `TABLE` mode,
12-line/half-pane vertical cap, display-column horizontal movement, current
100x20 dimensions, and no inner mouse-wheel routing in first release.
