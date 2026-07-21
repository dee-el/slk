# Terminal Compatibility

slk works in any modern terminal, but the experience scales with what your
terminal supports. This table summarizes the important capabilities; pick
something from the top of the list for the richest experience.

| Terminal              | Inline images        | True-pixel avatars | OSC 8 links | OSC 52 clipboard | Notes                                                       |
|-----------------------|----------------------|--------------------|-------------|------------------|-------------------------------------------------------------|
| **kitty**             | kitty graphics       | yes                | yes         | yes              | Best overall experience. Older versions may need `clipboard_control write-clipboard`. |
| **ghostty**           | kitty graphics       | yes                | yes         | yes              | Recommended.                                                |
| **WezTerm** (recent)  | kitty graphics       | yes                | yes         | yes              |                                                             |
| **foot** (Wayland)    | sixel                | half-block         | yes         | yes              | Best Wayland-native option.                                 |
| **iTerm2 ≥ 3.5**      | half-block           | half-block         | yes         | yes              | Implements kitty graphics but not unicode placeholders, so slk falls back to half-block. |
| **Alacritty**         | half-block           | half-block         | yes (≥0.11) | yes              | Fast and reliable, but no inline images.                    |
| **gnome-terminal** (recent) | half-block     | half-block         | yes         | yes              |                                                             |
| **mlterm**            | sixel                | half-block         | partial     | partial          |                                                             |
| **screen**            | half-block           | half-block         | no          | no               | No working OSC 52 path; consider switching to tmux.         |

Animated custom GIF emoji use the kitty graphics path too: kitty, Ghostty, and recent WezTerm animate them when `[animations].enabled = true` and emoji images are on. Half-block, sixel, tmux fallback, and image-off mode stay static/text-only.

Block Kit tables use Unicode box-drawing characters plus a bounded viewport frame when they overflow the current pane. Use `t` to enter TABLE mode, then `h/j/k/l`, `PgUp`/`PgDn`, `Ctrl+U`/`Ctrl+D`, and `Tab`/`Shift+Tab` to pan or switch tables.

## Inside tmux

slk forces half-block for inline images regardless of the
outer terminal — pixel-protocol pass-through inside tmux is unreliable. OSC 52
clipboard requires `set -g set-clipboard on` in your tmux config.

## Overriding the image protocol

You can override slk's image-protocol pick via the `[appearance] image_protocol`
config key (`auto` / `kitty` / `sixel` / `halfblock` / `off`). See
[[Configuration]] for details.

## Related

- [[Clipboard and OSC 52|Clipboard-and-OSC-52]] — getting copy/paste to land
- [[Tradeoffs and Non-Goals|Tradeoffs-and-Non-Goals]] — image rendering caveats (GIF attachments, unfurls, threads pane sixel)
