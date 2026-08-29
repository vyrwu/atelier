# Embedding atelier into your tmux statusline

This is the load-bearing integration doc. Atelier exposes shell-runnable
data emitters as its **public statusline API**. You plug them into your
tmux `window-status-format` (or anywhere else that accepts `#(...)`
shell-out) to surface atelier's per-workspace state in whatever visual
style you want.

If you're using the bundled launcher (`atelier` command), these are
already wired for you. Everyone else: this is how.

> [!IMPORTANT]
> **Upgrading to the intent-workspace release? Re-source your tmux.conf.**
> The v1 redesign **changed the keybindings** (`M-c` is now List Changes,
> the k9s context switcher moved to `M-k`, `M-r` renames a workspace, and
> `M-u` clone-from-URL is gone). `atelier init --bare` emits the new
> binding block, but your running tmux server keeps the *old* bindings
> until you reload — so an embedded (`--bare`) setup will still fire the
> pre-redesign bindings after you upgrade the binary. Reload with:
>
> ```bash
> tmux source-file ~/.config/tmux/tmux.conf   # or your config path
> ```
>
> (Bundled-launcher users get this for free — a fresh `atelier` sources
> the current init.) This is a breaking change for embedders.

---

## The two emitters

Both are subcommands of the `atelier` binary. They print to stdout
and exit 0 in all states (so tmux `#(...)` never produces noise).

> [!NOTE]
> **The git-freshness (ahead/behind) segment is retired.** It was a
> per-*branch* signal, and a workspace is no longer a branch checkout —
> the driver agent runs at the workspace root, which spans repos. The
> "is there unpushed/unmerged work" signal now lives in the **PR state**
> in the `M-c` Changes view. `atelier status freshness` is no longer part
> of the stamped statusline (its input options are no longer populated,
> so it renders nothing); don't wire it into new configs.

### `atelier status attention count`

Scans every tmux window across every session and sums
`@needs_attention=1`. Renders the rollup as a yellow `⏺ <n>` icon.
No arguments needed.

**Output shapes:**

| State | Output |
|---|---|
| No window flagged | `` (empty) |
| N windows flagged | ` #[fg=yellow]⏺ N#[default]` |

The flag is set by atelier's background observer loop when a workspace's
agent is *blocked* waiting on you (derived by re-reading the agent's
transcript each tick — not a hook the agent installs). It clears
automatically when the user opens that window (via `after-select-window`
hook) or attaches to its popup (via `client-session-changed` hook).

### `atelier status forge '<forge_state>'`

Renders the workspace's code-forge (PR/MR) status as a colored Nerd
Font glyph. The single argument is the window's cached `@forge_state`,
classified by the active forge adapter (GitHub, …) and refreshed in the
background (`SpawnForgeRefresh`) on every workspace-land event and on
picker open. When no forge integration is configured the option is
unset and the emitter renders nothing.

| Argument | Source | Type |
|---|---|---|
| `forge_state` | `#{@forge_state}` | one of `open` / `draft` / `merged` / `closed` (empty = no PR) |

**Output shapes** — each state renders a distinct Nerd Font v3 Codicon
(shared with the picker badge), reinforced by a 256-palette color that
resolves against the user's theme:

| State | Glyph (Codicon) | Output |
|---|---|---|
| No PR / no forge integration (`forge_state` empty) | — | `` (empty) |
| Open PR | git-pull-request `U+EA64` | ` #[fg=colour35]<glyph>#[default]` (green) |
| Draft PR | git-pull-request-draft `U+EBDB` | ` #[fg=colour244]<glyph>#[default]` (grey) |
| Merged | git-merge `U+EAFE` | ` #[fg=colour141]<glyph>#[default]` (purple) |
| Closed | git-pull-request-closed `U+EBDA` | ` #[fg=colour203]<glyph>#[default]` (red) |

The glyph + color come from the kernel-owned spec (`integration.ForgeGlyph`),
so this status-line badge and the workspace picker's badge always match.

---

## Worked examples

### Vanilla tmux

Add to your `~/.config/tmux/tmux.conf` after sourcing atelier:

```tmux
run-shell 'atelier init --bare | tmux source-file -'

# Show only the active window in the status bar, with atelier's
# attention rollup next to the window name, then the forge PR badge.
set -g window-status-current-format "#W #(atelier status attention count)#(atelier status forge '#{@forge_state}')"
```

### Idempotent stamping (recommended)

Hand-writing the emitters into your format gets tedious. Atelier
ships a stamp command that injects the canonical segments after
`#W` in your existing format:

```tmux
run-shell 'atelier init --bare | tmux source-file -'

# Set whatever format you want. Atelier's stamp-statusline (run via
# init) will inject the attention + forge segments AFTER `#W` and
# before any other content. Safe to re-source the config any number
# of times — the stamp strips prior injections before adding the
# canonical segments.
set -g window-status-current-format "#W"
```

The stamp re-injects the canonical segments AFTER `#W` and any trailing
color/glyph blocks (so the icons land in the right segment of a
powerline-style format), stripping any prior atelier injection first.
Segments are injected into `window-status-current-format` ONLY, so the
bar reflects the focused workspace; inactive windows render nothing
(keep `window-status-format ""`). The global `attention count` rollup
still surfaces how many background workspaces are waiting.

### Powerline-style

```tmux
# Your existing powerline-style format, with `#W` somewhere in it:
set -g window-status-current-format "#[fg=brightblack,bg=blue]#[fg=white,bg=blue] #W #[fg=blue,bg=brightblack]"

# Atelier's stamp finds `#W ` + the trailing color/arrow block and
# injects after the arrow, so the icons render in the NEXT segment
# rather than inside the colored name box.
run-shell 'atelier init --bare | tmux source-file -'
```

Final format (after stamp):
```
#[fg=brightblack,bg=blue]#[fg=white,bg=blue] #W #[fg=blue,bg=brightblack]#(atelier status attention count)#(atelier status forge '#{@forge_state}')
```

Only two segments are injected now; the freshness icon that used to sit
between `#W` and the attention rollup is gone.

### Dracula

The bundled launcher's theme uses a dracula-leaning palette; if
you're embedding into [dracula/tmux](https://draculatheme.com/tmux),
the same stamp logic works against dracula's status format. See
[examples/tmux/](../examples/tmux/) for a full reference.

---

## Where the data comes from

Atelier writes the per-workspace tmux options consumed by these
emitters:

- `@needs_attention` — set by atelier's observer loop when a workspace's
  agent is blocked waiting on you; cleared by atelier's
  `after-select-window` and `client-session-changed` hooks.
- `@forge_state` — set by the forge-refresh worker
  (`SpawnForgeRefresh`), which classifies each workspace's PR via
  the active forge adapter. Fires on every workspace-land event and on
  picker open; per-repo batched queries are TTL-throttled.

All four atelier hooks (`window-unlinked`, `session-closed`,
`after-select-window`, `client-session-changed`) are emitted by
`atelier init` so the rollup stays accurate.

---

## Performance

Both emitters are fast and side-effect-free:

- `forge` is pure — it maps the pre-cached `@forge_state`
  argument to a glyph. The forge query happens out-of-band in the
  refresh worker, never in the emitter.
- `attention count` does one `tmux list-windows -a` (~5ms typical)
  and counts matching lines.

tmux invokes `#(...)` shell-outs once per `status-interval` (default
15s; atelier sets it to 3s in its `StatuslineBlock`). 8 windows ×
0.3 Hz × 2 emitters = ~5 invocations/second. Trivial.

---

## What you can't customize through these emitters

If you want to change the icons or colors themselves (e.g. swap ✔ for
something else), the emitters are the wrong layer — they hardcode the
output strings. Fork the emitter logic, or wrap them in your own shell
function and post-process the output:

```bash
# in some script atelier_forge on PATH:
out=$(atelier status forge "$@")
echo "${out/ /  }"  # your preferred spacing/glyph
```

Then in tmux: `#(atelier_forge '#{@forge_state}')`.

Atelier's API stability promise is on the **arg shape and output
shape contract**, not on the specific glyphs/colors. Wrap if you
need different visuals.

---

## What `--bare` emits (and what it doesn't)

`atelier init --bare` emits **engine wiring only** — the tool bindings,
core bindings, the four hooks above, the `stamp-statusline` injection,
the background refresh loop, and workspace restore. It does **not** emit:

- The bundled **theme** (`window-status-*` formats, palette, clipboard,
  behavioral defaults) — you own the visual layer.
- The **launcher default screen**. In the bundled launcher, a
  `client-attached` hook runs `atelier internal welcome`, which opens the
  `M-n` intent creator on attach when you have no workspaces yet (and
  no-ops once one exists). This is bundled-only: an embedded (`--bare`)
  user drives their own tmux, so it isn't wired — create your first
  workspace with `M-n` yourself.

Because the binding block *is* emitted in `--bare` mode, the
keybinding-change callout at the top of this doc applies to you: after
upgrading the binary, re-source your config so tmux picks up the new
bindings.

---

## See also

- [README.md](../README.md) — the engine overview.
- [DESIGN.md](../DESIGN.md) — internal architecture.
- [examples/tmux/](../examples/tmux/) — runnable reference configs.
- [RELEASING.md](../RELEASING.md) — how atelier is shipped.
