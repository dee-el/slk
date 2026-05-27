# Phase 11: Documentation and Smoke Checklist

> Index: `00-overview.md`. Previous: `10-yank-and-search.md`.

**Goal:** Document the new config knobs and behavior, and perform a one-time end-to-end manual smoke pass across the terminals slk targets.

**Files:**
- Modify: `wiki/Configuration.md`
- Modify: `wiki/Terminal-Compatibility.md`
- Modify: `wiki/Features.md`
- Modify: `README.md` (config example block, if it has one)

---

### Task 11.1: Document `emoji_images` and `emoji_cells` in `wiki/Configuration.md`

**Files:**
- Modify: `wiki/Configuration.md`

- [ ] **Step 1: Add the new entries**

Locate the `[appearance]` section in `wiki/Configuration.md` (search for `image_protocol`). Add the new keys to the example config block and the documentation table:

```toml
[appearance]
theme           = "nord"
image_protocol  = "auto"   # auto | kitty | sixel | halfblock | off
emoji_images    = "on"     # on | off  -- render emoji as PNG images via the kitty graphics protocol
emoji_cells     = 2        # 1 | 2     -- cell footprint per emoji image (defaults to 2; matches East-Asian-Wide)
max_image_rows  = 20
mouse_wheel_lines = 3
```

Add a prose paragraph below:

```markdown
### Emoji as Images

When `emoji_images = "on"` AND the active image protocol is `kitty`, every emoji
(standard Unicode + your workspace's custom emoji) renders as a PNG fetched from
Slack's CDN, using the kitty graphics protocol. This retires the per-terminal width
probe for emoji and fixes the rendering of ZWJ sequences, regional-indicator flags,
skin-tone modifiers, and your workspace's `:party_parrot:`-style custom emoji.

Set `emoji_images = "off"` if you're on a network that blocks `slack-edge.com`, or
if you prefer the glyph-rendering pipeline. On non-kitty terminals (sixel, halfblock,
off), `emoji_images = "on"` is silently treated as `"off"`.

`emoji_cells` controls the per-emoji terminal-cell footprint. Default `2` produces
a near-square pixel region in typical fonts. `1` is an escape hatch if `2` looks too
large in your font.
```

- [ ] **Step 2: Commit**

```bash
git add wiki/Configuration.md
git commit -m "docs(wiki): document emoji_images and emoji_cells config keys"
```

---

### Task 11.2: Update `wiki/Terminal-Compatibility.md`

**Files:**
- Modify: `wiki/Terminal-Compatibility.md`

- [ ] **Step 1: Add an emoji-as-images section**

Append a new section to `wiki/Terminal-Compatibility.md`:

```markdown
## Emoji as images (kitty only)

On kitty-class terminals (kitty, ghostty, recent WezTerm) slk renders every emoji as
a PNG image fetched from Slack's CDN. This solves three long-standing issues that
plagued the glyph-rendering pipeline:

- Multi-codepoint emoji (ZWJ sequences, regional-indicator flags, skin-tone
  modifiers, VS16-anchored emoji) render correctly instead of falling back to
  `:name:` text.
- Your workspace's custom emoji (`:party_parrot:`, team logos, in-jokes) appear as
  their actual artwork.
- Per-terminal and per-font width drift disappears: slk declares a fixed cell
  footprint and the terminal renders pixels into exactly that footprint.

This feature is on by default on kitty. If you are on a network that blocks
`a.slack-edge.com` or you prefer the legacy glyph rendering, disable it:

```toml
[appearance]
emoji_images = "off"
```

On sixel, halfblock, or `image_protocol = "off"` terminals the feature is
unsupported in v1; slk uses its existing glyph + width-probe pipeline. Future
versions may extend image rendering to other protocols (or remove the glyph path
entirely).

### Clipboard caveat

In v1, copying a message that contains image emoji yields spaces in the clipboard
where the emoji were (the visible width is preserved, the emoji content is lost).
If you frequently need the `:name:` form on yank, set `emoji_images = "off"`. A
richer mapping that preserves `:name:` text on copy is a planned follow-up.
```

- [ ] **Step 2: Commit**

```bash
git add wiki/Terminal-Compatibility.md
git commit -m "docs(wiki): explain emoji-as-images on kitty and yank caveat"
```

---

### Task 11.3: Update `wiki/Features.md`

**Files:**
- Modify: `wiki/Features.md`

- [ ] **Step 1: Add a bullet to the relevant feature list**

Locate the emoji-related bullets in `wiki/Features.md` (search for `emoji`). Add:

```markdown
- On kitty-class terminals, every emoji — standard and workspace-custom — renders
  as a PNG image from Slack's CDN, with reliable alignment regardless of font.
  Configurable via `[appearance] emoji_images`.
```

- [ ] **Step 2: Commit**

```bash
git add wiki/Features.md
git commit -m "docs(wiki): list emoji-as-images among features"
```

---

### Task 11.4: Manual smoke checklist

**Files:** none (manual procedure)

Perform the following on a real kitty (or ghostty / WezTerm) terminal with a workspace that has at least one custom emoji. Each check is binary pass/fail.

- [ ] **kitty + no tmux:**
  - Open a channel with messages containing `:thumbsup:`, `:heart:`, `:fire:`, a multi-codepoint emoji (e.g., `:flag-us:`), and a workspace custom emoji.
  - Confirm each renders as an image, not as `:name:` text and not as a single-glyph fallback.
  - Confirm reaction pills on those messages show image emoji followed by the count.
  - Open a thread (`t` or whatever the keybind is). Confirm replies show image emoji.
  - Open the reaction picker (`+`). Filter to a query. Confirm dropdown shows image previews.
  - In compose, type `:thu`. Confirm autocomplete dropdown shows image previews.

- [ ] **kitty + tmux:**
  - Repeat the above inside a tmux session. Confirm images still render (kitty escapes wrap in DCS passthrough; the existing inline-image pipeline already proves this works).
  - If images do NOT render under tmux, the DCS-wrap path is the suspect — check that the existing inline image attachments still render too. If they don't, this is a tmux/passthrough config issue, not specific to emoji.

- [ ] **ghostty:**
  - Repeat the kitty-no-tmux check on ghostty.

- [ ] **WezTerm:**
  - Repeat the kitty-no-tmux check on WezTerm.

- [ ] **CDN-blocked network:**
  - `[appearance] emoji_images = "off"` in config, restart slk. Confirm emoji render via the legacy glyph/`:name:` path with no regression.

- [ ] **`emoji_cells = 1` override:**
  - Set `emoji_cells = 1`, restart on kitty. Confirm emoji render at 1-cell width. Visually check layout — reaction pills still line up, message-body alignment preserved.

- [ ] **Yank/clipboard:**
  - Select a message containing an emoji. Copy. Paste into another terminal.
  - Confirm the pasted text is readable (spaces where emoji were per Phase 10 v1).
  - Confirm no garbled `􏻮`-like bytes appear.

- [ ] **Cold-cache UX:**
  - On a fresh machine (or after manually clearing the image cache dir), open a busy channel.
  - Confirm emoji slots show blank reservations briefly, then fill in as fetches complete (matches existing avatar/attachment cold-cache UX).
  - Confirm no layout shifts during the cold→warm transition (cell widths are stable).

- [ ] **Density check:**
  - Find a channel with a message that has many reactions (10+) or that contains many emoji in one body. Confirm rendering throughput is fine — no visible stutter, no flicker.

- [ ] **Cache survives restart:**
  - Quit slk. Restart on the same channel. Confirm previously-warm emoji render fast (no cold-cache reservations for emoji that survived in the disk LRU).

---

### Task 11.5: Final phase check

- [ ] **Step 1: Verify full plan is implemented**

```bash
git log --oneline -50 | grep -E '(emoji|image)' | wc -l
```

Expect ~30-50 commits across phases 1-11.

- [ ] **Step 2: Run the full test suite one last time**

Run: `go test ./... -count=1`
Expected: no failures.

- [ ] **Step 3: Tag or PR**

Either tag the work for release or open a PR per the repo's convention. The PR description should reference the design spec (`docs/superpowers/specs/2026-05-27-emoji-as-images-design.md`) and summarize the manual smoke results from Task 11.4.

---

**Plan complete.** Image emoji ship across all in-scope surfaces on kitty terminals. Documented follow-ups (in `00-overview.md`):

- Persisted decoded-pixel cache to skip PNG decode on session start
- Persisted kitty payload + stable cross-session image IDs to skip re-transmit
- Per-workspace emoji style detection (Apple / Google / Twitter / Slack-classic)
- Surfaces G (channel list) and H (user status / display name)
- Picker / autocomplete prefetch tuning if lazy proves slow
- Richer plainText mapping on yank (preserve `:name:` form in clipboard) — Phase 10 addendum
- Eventual deletion of non-kitty rendering paths
