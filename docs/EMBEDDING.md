# Embedding atelier into your tmux statusline

This is the load-bearing integration doc. Atelier exposes two
shell-runnable data emitters as its **public statusline API**. You
plug them into your tmux `window-status-format` (or anywhere else
that accepts `#(...)` shell-out) to surface atelier's per-workspace
state in whatever visual style you want.

If you're using the bundled launcher (`atelier` command), these are
already wired for you. Everyone else: this is how.

---

## The two emitters

Both are subcommands of the `atelier` binary. They print to stdout
and exit 0 in all states (so tmux `#(...)` never produces noise).

### `atelier status freshness <behind> <ahead> <pull_error> <freshness_ts> <repo_path>`

Renders the git sync status for a workspace as a colored icon. The
arguments come from tmux options that atelier's background pull
(`_bg-pull`) stamps on the workspace's window.

| Arguments (in order) | Source | Type |
|---|---|---|
| `behind` | `#{@workspace_behind}` | integer (commits behind `origin/<default>`) |
| `ahead` | `#{@workspace_ahead}` | integer (commits ahead of `origin/<default>`) |
| `pull_error` | `#{@workspace_pull_error}` | short error string from the last failed pull |
| `freshness_ts` | `#{@workspace_freshness_ts}` | unix epoch of last successful fetch |
| `repo_path` | `#{@repo_path}` | absolute path of the workspace's repo (set on the tmux session) |

**Output shapes:**

| Workspace state | Output |
|---|---|
| Not a git repo (`repo_path` empty) | `` (empty) |
| Pull pending / never ran (`freshness_ts` empty + no error) | `` (empty) |
| In sync (`behind=0 ahead=0`) | ` #[fg=green]✔#[default]` |
| Behind only | ` #[fg=red]↓N#[default]` |
| Ahead only | ` #[fg=yellow]↑N#[default]` |
| Diverged (behind AND ahead) | ` #[fg=red]↓N↑M#[default]` |
| Pull error | ` #[fg=red]⚠ <truncated msg>#[default]` |

Note the leading space — the emitter pads itself so adjacent content
doesn't kiss the icon.

### `atelier status attention count`

Scans every tmux window across every session and sums
`@needs_attention=1`. Renders the rollup as a yellow `⏺ <n>` icon.
No arguments needed.

**Output shapes:**

| State | Output |
|---|---|
| No window flagged | `` (empty) |
| N windows flagged | ` #[fg=yellow]⏺ N#[default]` |

The flag is set by the Claude integration's Stop hook when a Claude session
completes work in a window the user wasn't viewing at the time. It
clears automatically when the user opens that window (via
`after-select-window` hook) or attaches to its popup (via
`client-session-changed` hook).

---

## Worked examples

### Vanilla tmux

Add to your `~/.config/tmux/tmux.conf` after sourcing atelier:

```tmux
run-shell 'atelier init --bare | tmux source-file -'

# Show only the active window in the status bar, with atelier's
# freshness icon next to the window name and the attention rollup
# at the end.
set -g window-status-current-format "#W #(atelier status freshness '#{@workspace_behind}' '#{@workspace_ahead}' '#{@workspace_pull_error}' '#{@workspace_freshness_ts}' '#{@repo_path}')#(atelier status attention count)"
```

### Idempotent stamping (recommended)

Hand-writing the freshness emitter into your format gets tedious.
Atelier ships a stamp command that injects the canonical pair after
`#W` in your existing format:

```tmux
run-shell 'atelier init --bare | tmux source-file -'

# Set whatever format you want. Atelier's stamp-statusline (run via
# init) will inject the freshness + attention segments AFTER `#W`
# and before any other content. Safe to re-source the config any
# number of times — the stamp strips prior injections before
# adding the canonical pair.
set -g window-status-current-format "#W"
```

The stamp regex matches and strips any prior `#(atelier status
(freshness|attention)...)` injection, then re-injects the canonical
pair AFTER `#W` and any trailing color/glyph blocks (so the icon
lands in the right segment of a powerline-style format).

### Powerline-style

```tmux
# Your existing powerline-style format, with `#W` somewhere in it:
set -g window-status-current-format "#[fg=brightblack,bg=blue]#[fg=white,bg=blue] #W #[fg=blue,bg=brightblack]"

# Atelier's stamp finds `#W ` + the trailing color/arrow block and
# injects after the arrow, so the icon renders in the NEXT segment
# rather than inside the colored name box.
run-shell 'atelier init --bare | tmux source-file -'
```

Final format (after stamp):
```
#[fg=brightblack,bg=blue]#[fg=white,bg=blue] #W #[fg=blue,bg=brightblack]#(atelier status freshness …)#(atelier status attention count)
```

### Dracula

The bundled launcher's theme uses a dracula-leaning palette; if
you're embedding into [dracula/tmux](https://draculatheme.com/tmux),
the same stamp logic works against dracula's status format. See
[examples/tmux/](../examples/tmux/) for a full reference.

---

## Where the data comes from

Atelier writes the per-workspace tmux options consumed by these
emitters:

- `@repo_path` — set by atelier on session creation
  (`workspaces` plugin).
- `@workspace_behind`, `@workspace_ahead`,
  `@workspace_freshness_ts`, `@workspace_pull_error` — set by
  atelier's background-pull worker, which fires on every workspace
  switch and at startup for stale workspaces.
- `@needs_attention` — set by the Claude integration's Stop hook on
  Claude session completion; cleared by atelier's `after-select-window`
  and `client-session-changed` hooks.

All four atelier hooks (`window-unlinked`, `session-closed`,
`after-select-window`, `client-session-changed`) are emitted by
`atelier init` so the rollup stays accurate.

---

## Performance

Both emitters are fast and side-effect-free:

- `freshness` is a pure function — no subprocess, no tmux call,
  just arg-to-output string mapping.
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
# in some script atelier_freshness on PATH:
out=$(atelier status freshness "$@")
echo "${out/✔/✓}"  # your preferred glyph
```

Then in tmux: `#(atelier_freshness '#{@workspace_behind}' …)`.

Atelier's API stability promise is on the **arg shape and output
shape contract**, not on the specific glyphs/colors. Wrap if you
need different visuals.

---

## See also

- [README.md](../README.md) — the engine overview.
- [DESIGN.md](../DESIGN.md) — internal architecture.
- [examples/tmux/](../examples/tmux/) — runnable reference configs.
- [RELEASING.md](../RELEASING.md) — how atelier is shipped.
