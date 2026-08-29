# Embedding atelier into your tmux statusline

This is the load-bearing integration doc. Atelier exposes shell-runnable
data emitters as its **public statusline API**. You plug them into your
tmux `status-left` / `window-status-format` (or anywhere else that accepts
`#(...)` shell-out) to surface atelier's state in whatever visual style
you want.

If you're using the bundled launcher (`atelier` command), these are
already wired for you. Everyone else: this is how.

> [!IMPORTANT]
> **Upgrading to the intent-workspace release? Re-source your tmux.conf.**
> The v1 redesign **changed the keybindings** (`M-c` is now List Changes,
> the k9s context switcher moved to `M-k`, `M-r` renames a workspace, and
> `M-u` clone-from-URL is gone) and **moved the bar to the top** showing the
> workspace *description* instead of the repo name. `atelier init --bare`
> emits the new binding block, but your running tmux server keeps the *old*
> bindings until you reload — so an embedded (`--bare`) setup will still fire
> the pre-redesign bindings after you upgrade the binary. Reload with:
>
> ```bash
> tmux source-file ~/.config/tmux/tmux.conf   # or your config path
> ```
>
> (Bundled-launcher users get this for free — a fresh `atelier` sources
> the current init.) This is a breaking change for embedders.

---

## The emitters

Both are subcommands of the `atelier` binary. They print to stdout
and exit 0 in all states (so tmux `#(...)` never produces noise).

> [!NOTE]
> **The git-freshness (ahead/behind) and per-window forge (PR) badges are
> retired from the statusline.** Both were per-*branch* signals, and a
> workspace is no longer a branch checkout — the driver agent runs at the
> workspace root, which spans repos. "Is there unpushed/unmerged work" now
> lives as **PR state** in the `M-c` Changes view, and PRs are an aggregate
> on the workspace record (rendered in the `M-s` rollup and `M-c`), not a
> per-window tmux option. `atelier status freshness` and `atelier status
> forge` are gone; don't wire them into new configs.

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

### `atelier status description '<session>'`

Prints the current workspace's **description** — its intent-derived title
(`@workspace_title`) — for the left of the bar. The single argument is the
session whose description to show; the bundled theme passes
`#{client_session}` (the focused client's session). Falls back to the
session name when the option is unset (e.g. the launcher's `default`
session), so the bar is never blank.

| Argument | Source | Meaning |
|---|---|---|
| `session` | `#{client_session}` | the session whose `@workspace_title` to render |

It reads `@workspace_title` via `show-options` (a direct target read) rather
than a `#{@workspace_title}` status FORMAT, because a session user-option
does not resolve inside a status format on every tmux version (3.4). Output
is the raw title text (no color codes) — wrap it in your own `#[...]`
directives to style it, as the bundled theme does with `#[bold]`.

---

## Worked examples

### Vanilla tmux — atelier-style top bar

Add to your `~/.config/tmux/tmux.conf` after sourcing atelier:

```tmux
run-shell 'atelier init --bare | tmux source-file -'

# Bar at the top: the workspace description + attention rollup on the left,
# the clock on the right. No per-window chrome.
set -g status-position top
set -g status-left " #[bold]#(atelier status description '#{client_session}')#[nobold]  #(atelier status attention count) "
set -g status-right " %H:%M "
set -g window-status-format ""
set -g window-status-current-format ""
```

### Keep your own bar, just add the attention rollup (stamping)

If you'd rather keep your existing per-window bar and only add atelier's
attention rollup, the stamp command injects it after `#W` in your existing
`window-status-current-format`:

```tmux
run-shell 'atelier init --bare | tmux source-file -'

# Set whatever format you want. Atelier's stamp-statusline (run via init)
# injects the attention segment AFTER `#W` and before any other content.
# Safe to re-source any number of times — the stamp strips a prior
# injection before re-adding the canonical segment.
set -g window-status-current-format "#W"
```

The stamp re-injects the segment AFTER `#W` and any trailing color/glyph
blocks (so the icon lands in the right segment of a powerline-style
format), stripping any prior atelier injection first. It targets
`window-status-current-format` ONLY, so the bar reflects the focused
workspace; keep `window-status-format ""` so inactive windows render
nothing. The global `attention count` rollup still surfaces how many
background workspaces are waiting.

### Powerline-style

```tmux
# Your existing powerline-style format, with `#W` somewhere in it:
set -g window-status-current-format "#[fg=brightblack,bg=blue]#[fg=white,bg=blue] #W #[fg=blue,bg=brightblack]"

# Atelier's stamp finds `#W ` + the trailing color/arrow block and
# injects after the arrow, so the icon renders in the NEXT segment
# rather than inside the colored name box.
run-shell 'atelier init --bare | tmux source-file -'
```

Final format (after stamp):
```
#[fg=brightblack,bg=blue]#[fg=white,bg=blue] #W #[fg=blue,bg=brightblack]#(atelier status attention count)
```

Only the attention segment is injected now; the freshness and forge icons
that used to follow it are gone.

### Dracula

The bundled launcher's theme uses a dracula-leaning palette; if
you're embedding into [dracula/tmux](https://draculatheme.com/tmux),
the same stamp logic works against dracula's status format. See
[examples/tmux/](../examples/tmux/) for a full reference.

---

## Where the data comes from

Atelier writes the per-workspace tmux options consumed by these emitters:

- `@needs_attention` — set by atelier's observer loop when a workspace's
  agent is blocked waiting on you; cleared by atelier's
  `after-select-window` and `client-session-changed` hooks.
- `@workspace_title` — the workspace's renameable description, set at
  creation (from the intent) and edited by `M-r`. Session-scoped.

All four atelier hooks (`window-unlinked`, `session-closed`,
`after-select-window`, `client-session-changed`) are emitted by
`atelier init` so the rollup stays accurate.

---

## Performance

Both emitters are fast and side-effect-free:

- `description` does one `tmux show-options` (~2ms typical).
- `attention count` does one `tmux list-windows -a` (~5ms typical)
  and counts matching lines.

tmux invokes `#(...)` shell-outs once per `status-interval` (default
15s; atelier sets it to 3s in its `StatuslineBlock`). A couple of
invocations every 3s is trivial.

---

## What you can't customize through these emitters

If you want to change the icons or colors themselves (e.g. swap ⏺ for
something else), the emitters are the wrong layer — they hardcode the
output strings. Fork the emitter logic, or wrap them in your own shell
function and post-process the output:

```bash
# in some script atelier_attn on PATH:
out=$(atelier status attention count)
echo "${out/⏺/●}"  # your preferred glyph
```

Then in tmux: `#(atelier_attn)`.

Atelier's API stability promise is on the **arg shape and output
shape contract**, not on the specific glyphs/colors. Wrap if you
need different visuals.

---

## What `--bare` emits (and what it doesn't)

`atelier init --bare` emits **engine wiring only** — the tool bindings,
core bindings, the four hooks above, the `stamp-statusline` injection,
the background refresh loop, and workspace restore. It does **not** emit:

- The bundled **theme** (top bar, `status-left` description, `window-status-*`
  formats, palette, clipboard, behavioral defaults) — you own the visual
  layer. In `--bare` mode the attention rollup reaches you via the
  `stamp-statusline` injection into your own `window-status-current-format`,
  not `status-left`.
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
