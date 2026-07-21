# ADR: Render Slack Block Kit Tables

- Status: Superseded
- Date: 2026-07-20
- Repository: `dee-el/slk`
- Target branch: `main`
- Superseded by: `docs/adr/fix-table-cells-and-add-scrollable-viewports.md`

## Context

Messages containing Slack's Block Kit `table` block currently render:

```text
[unsupported block: table]
```

The existing dependency, `github.com/slack-go/slack v0.23.0`, already parses
table JSON into:

```go
type TableBlock struct {
    Type           MessageBlockType
    BlockID        string
    Rows           [][]*RichTextBlock
    ColumnSettings []ColumnSetting
}

type ColumnSetting struct {
    Align     ColumnAlignment // left, center, right
    IsWrapped bool
}
```

`internal/ui/messages/blockkit.parseOne` handles header, divider, section,
context, image, actions, and rich-text blocks. `*slack.TableBlock` falls through
to `UnknownBlock`, producing the placeholder shown above.

Every table cell is a Slack `rich_text` block. The blockkit package already has
`RichTextToMrkdwn`, and the renderer's `Context.RenderText` converts that
mrkdwn into the application's ANSI-styled text with mentions, links, emoji,
code, and theme colors. Table support should reuse those paths rather than
create another rich-text renderer.

Terminal tables have constraints absent from Slack desktop:

- message pane width varies with sidebar/thread state and terminal resize;
- Unicode and ANSI styling make byte length unusable for width calculations;
- tables may contain more columns than can fit;
- `is_wrapped=false` requires truncation rather than uncontrolled overflow;
- malformed or bot-generated payloads may have ragged rows, nil cells, missing
  column settings, or excessive dimensions.

The fork's `main` branch remains authoritative and intentionally drifts from
upstream.

## Decision

Parse Slack table blocks into a small internal representation and render them
as adaptive Unicode tables within the message width.

### Internal model

Add:

```go
type TableBlock struct {
    Rows    [][]TableCell
    Columns []TableColumn
}

type TableCell struct {
    Text string // Slack mrkdwn reconstructed from rich_text
}

type TableColumn struct {
    Align   TableAlignment
    Wrapped bool
}

type TableAlignment int

const (
    TableAlignLeft TableAlignment = iota
    TableAlignCenter
    TableAlignRight
)
```

`TableBlock.blockType()` returns `"table"`.

Do not retain slack-go cell pointers in the UI model. Convert each cell once at
parse time:

```go
text := RichTextToMrkdwn(cell.Elements)
```

Benefits:

- deterministic, immutable table model;
- same link/mention/emoji semantics as ordinary rich text;
- no slack-go-specific traversal during hot render/cache rebuild paths;
- straightforward unit tests.

### Parsing

Add `*slack.TableBlock` to `parseOne`.

Rules:

- Preserve row order and cell order.
- Nil cell becomes an empty `TableCell`.
- Ragged rows stay ragged in model; renderer pads missing cells.
- Column count is maximum row length and column-settings length.
- Missing column settings default to left-aligned and non-wrapped.
- Unknown alignment defaults to left.
- Ignore settings beyond rendered column cap.
- Safety cap: at most 100 rows and 20 columns, matching Slack table limits.
- If payload exceeds cap, truncate rows/columns and append a muted summary line
  after table: `[table truncated: showing 100 rows x 20 columns]`.
- Empty table renders no lines rather than an unsupported marker.

The internal model carries truncation metadata:

```go
type TableBlock struct {
    Rows          [][]TableCell
    Columns       []TableColumn
    RowsTruncated bool
    ColsTruncated bool
    SourceRows    int
    SourceCols    int
}
```

### Rich-text rendering

Each cell goes through existing context callbacks:

```go
rendered := cell.Text
if ctx.RenderText != nil {
    rendered = ctx.RenderText(cell.Text, ctx.UserNames)
}
```

This preserves:

- bold/italic/code reconstructed by `RichTextToMrkdwn`;
- user mentions;
- clickable OSC-8 links;
- custom/static/animated emoji placement;
- theme foreground/background.

Animated/custom emoji flush callbacks are not currently expressible through
`Context.RenderText`, whose return type is only `string`. Table cells therefore
use the same text-render callback as existing Block Kit section fields: emoji
render visually through the host path, but any new image side effects remain
governed by existing host renderer behavior. No second image/emoji pipeline is
added in this feature.

### Wide table layout

Use Unicode box drawing:

```text
┌──────────────┬──────────┬─────────┐
│ Service      │ Status   │ Owner   │
├──────────────┼──────────┼─────────┤
│ API          │ Healthy  │ @Alex   │
└──────────────┴──────────┴─────────┘
```

No row is assumed to be a header. If Slack marks first-row text bold in its
rich-text runs, existing styling preserves that visual distinction.

Width accounting:

- Use `lipgloss.Width` for all rendered ANSI-aware measurements.
- Outer borders consume two columns.
- Internal separators consume `columnCount - 1` columns.
- Available cell budget is `width - columnCount - 1`.
- Minimum cell width is 3 columns.
- Desired width per column is maximum unwrapped line width across visible rows,
  capped at 30 columns to prevent one long cell consuming whole table.
- Begin every column at minimum width.
- Distribute remaining budget round-robin toward desired widths.
- Result must consume at most caller-provided `width` display columns.

Cell behavior:

- `is_wrapped=true`: wrap through `Context.WrapText` when available; otherwise
  use an ANSI-aware local word wrapper.
- `is_wrapped=false`: preserve explicit newlines but truncate each line with
  `...` to column width.
- Empty cells render spaces.
- Multi-line cells increase row height; neighboring cells receive blank padded
  lines.
- Apply column alignment independently to every physical line.
- Left/right padding is spaces inside the cell width; borders add no extra
  padding beyond allocated cell width.
- Strip/control-normalize tabs and carriage returns before layout so malicious
  cell text cannot break row geometry.

### Narrow fallback

When the table cannot allocate the 3-column minimum to every column:

```text
Row 1
  C1: Service
  C2: Status
  C3: Owner
Row 2
  C1: API
  C2: Healthy
  C3: @Alex
```

Rules:

- Use stacked fallback when `width - columnCount - 1 < columnCount * 3`.
- `Row N` uses primary/bold style.
- `C<N>:` uses muted style.
- Cell content wraps to `max(1, width - labelWidth)`.
- Omit missing trailing cells from ragged rows.
- Preserve explicit blank cells between populated cells.
- Every line remains at most `width` columns.

This is preferable to horizontally scrolling inside a message, silently
dropping columns, or producing unreadable one-character columns.

### Rendering integration

Add `TableBlock` dispatch in `appendBlock`:

```go
case TableBlock:
    appendTable(out, v, ctx, width)
```

Implement table rendering in `internal/ui/messages/blockkit/table.go` to keep
`render.go` from growing further.

Table rendering contributes only `Lines` and `Height` through normal
`RenderResult` aggregation:

- no image hits;
- no sixel rows;
- no interactivity;
- no mode/key changes;
- no cache architecture changes.

Tables nested inside legacy attachments work automatically because
`RenderLegacy` already routes nested blocks through the same `Render` entry
point.

### Sanitization and width helpers

Add package-private helpers:

```go
func normalizeTableCellText(string) string
func measureTableColumns(TableBlock, Context, int) []int
func renderTableCell(TableCell, TableColumn, Context, int) []string
func alignTableLine(string, TableAlignment, int) string
func tableBorder(left, middle, right rune, widths []int) string
```

Do not use `lipgloss/table` for this implementation. Existing blockkit
renderers require exact width guarantees, custom wrapping/truncation, and
theme-background reapplication. A small local renderer provides deterministic
geometry and avoids adapting another stateful abstraction.

## File-by-File Changes

- `internal/ui/messages/blockkit/types.go`
  - Add table model, cell, column, alignment, and truncation metadata.
- `internal/ui/messages/blockkit/types_test.go`
  - Add interface assertion for `TableBlock`.
- `internal/ui/messages/blockkit/parse.go`
  - Parse `*slack.TableBlock`, rich-text cells, settings, ragged/nil rows, caps.
- `internal/ui/messages/blockkit/parse_test.go`
  - Table JSON parse, rich formatting, align/wrap settings, defaults, nil cell,
    ragged rows, empty table, 100x20 caps.
- `internal/ui/messages/blockkit/render.go`
  - Dispatch `TableBlock`.
- `internal/ui/messages/blockkit/table.go` (new)
  - Wide Unicode table and narrow stacked renderer.
- `internal/ui/messages/blockkit/table_test.go` (new)
  - Exact wide output, ANSI-aware width, desired/min allocation, wrapping,
    truncation, alignment, multiline rows, narrow fallback, control chars,
    truncation summary, width invariants.
- `internal/ui/messages/blockkit/integration_test.go`
  - Parse real table JSON and render through host-style context.
- `internal/ui/messages/blockkit_integration_test.go`
  - Ensure message-pane cache displays table instead of unsupported marker.
- `internal/ui/thread/model_test.go`
  - Ensure table renders inside thread reply/parent with constrained width.
- `cmd/slk/cache_render_test.go`
  - Confirm cached Slack messages preserve parsed table rows/settings.
- `wiki/Features.md`
  - Add Block Kit table support.
- `wiki/Terminal-Compatibility.md`
  - Document adaptive stacked fallback on narrow panes.

No dependency or schema migration is required.

## Verification

Focused tests:

```sh
go test ./internal/ui/messages/blockkit ./internal/ui/messages ./internal/ui/thread ./cmd/slk
```

Width/property checks:

- Every rendered line has `lipgloss.Width(line) <= width` for widths 1-200.
- Random ragged tables never panic.
- Row/column caps hold for oversized payloads.
- ANSI/OSC-8 content does not corrupt borders.
- Empty and nil cells remain aligned.

Full verification:

```sh
go test -race -count=1 ./...
go vet ./...
make build-macos
otool -L bin/slk | grep AppKit.framework
```

Manual smoke:

1. Open message from screenshot; confirm table replaces unsupported marker.
2. Resize Ghostty from wide to narrow; confirm wide grid switches to stacked
   rows without clipping or horizontal overflow.
3. Open thread containing table.
4. Test table with bold first row, mentions, links, emoji, wrapped column,
   right-aligned numeric column, and empty cells.
5. Toggle sidebar/thread panel; confirm cache rebuild keeps geometry correct.

## Rollout

1. Implement directly on fork `main`; no feature branch.
2. Keep ADR status synchronized with implementation decisions.
3. Run focused table tests and full race/vet/AppKit build.
4. Build fork version with commit/date metadata.
5. Replace `~/bin/slk`.
6. Commit and push feature files directly to `origin/main`.

## Risks and Mitigations

- Table exceeds pane width: exact ANSI-aware budget and narrow stacked fallback.
- ANSI links/styles break borders: use `lipgloss.Width`, existing text renderer,
  and line-width property tests.
- Rich cell content is expensive: parse rich text once; normal message render
  cache stores final table rows.
- Malformed ragged rows panic: normalize maximum columns and pad at render.
- Huge bot table stalls UI: cap at 100 rows x 20 columns before render.
- Unclear header semantics: do not infer; preserve Slack rich-text bold styling.
- Wrapped text destroys links/styles: use existing ANSI-aware host wrapper.
- Narrow fallback is verbose: only activate when a readable grid is impossible.
- Table cells contain animation/image side effects: reuse current RenderText
  behavior and avoid introducing a second flush pipeline in this scope.
- Unicode box glyphs unavailable: terminals already targeted by slk support the
  same Unicode used throughout existing borders and controls.

## Alternatives Rejected

- Keep unsupported marker: loses message content.
- Plain TSV/space-separated rows: alignment breaks with ANSI and wide Unicode.
- Markdown pipe table: ambiguous pipes in cell content and weak narrow layout.
- Horizontal scrolling inside each message: conflicts with message navigation
  and adds per-message viewport state.
- Always stack cells: readable but wastes vertical space for normal tables.
- Infer first row as header: Slack table schema has no header flag.
- Use `lipgloss/table`: less control over exact width, wrapped rich text, and
  narrow fallback; local renderer is smaller and deterministic.
- Upgrade slack-go: current v0.23.0 already parses table blocks; dependency
  upgrade adds unrelated API/regression surface.

## Open Questions

None blocking. Defaults are adaptive Unicode grid, 3-column minimum, 30-column
desired cap, stacked narrow fallback, and 100x20 safety limits.
