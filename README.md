# atelier

[![ci](https://github.com/vyrwu/atelier/actions/workflows/ci.yml/badge.svg)](https://github.com/vyrwu/atelier/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/vyrwu/atelier?display_name=tag&sort=semver)](https://github.com/vyrwu/atelier/releases)
[![license](https://img.shields.io/github/license/vyrwu/atelier)](LICENSE)

**TUI-native agentic workspaces.** A tmux + git-worktree workspace
manager for parallel Claude/Codex/Aider sessions and the terminal
tools that go with them. Small Go core, plugin architecture, opinion-
free statusline API.

> **Status:** alpha. Single-author project. Stable for the author's daily
> use; expect rough edges if you adopt early.

---

## What is this?

If you spend your day in tmux running Claude Code (or Codex, or Aider),
juggling git worktrees, switching between k8s contexts, hopping between
repos — you've probably ended up with a pile of bash scripts gluing it
together.

Atelier is what that pile of scripts wants to become.

- **Workspace = tmux window + git worktree + tool state.** Each
  workspace has its own Claude session, lazygit, k9s context,
  postgres CLI. `M-;` picks any tool for the current workspace;
  `M-s` switches workspaces; `M-n` creates a new one from a natural-
  language task description; `M-r` recovers a soft-closed workspace.
- **Plugin architecture.** Every tool is a separate binary on `PATH`
  named `atelier-<name>`. The engine has zero compile-time knowledge
  of any specific tool. Replace `atelier-k8s` (EKS-targeted) with
  your own `atelier-k8s-aks` — drop it on PATH, atelier picks it up.
- **Selective install.** One Homebrew tap, one formula per tool.
  Install only what you use.
- **Statusline data emitters.** Atelier exposes freshness (git
  ahead/behind/error) and attention (Claude-finished-while-you-were-
  elsewhere) as commands you embed into your tmux statusline with
  `#(atelier status ...)`. Works with vanilla tmux, Dracula, Powerline —
  anything. The engine doesn't dictate visuals; you do.
- **Persistent state.** Workspaces, recap text, attention flags, git
  freshness — written through to disk and rehydrated on tmux restart.
  Detach with `M-q`; the server keeps running so background Claude
  sessions survive.
- **Always-on diagnostics.** Every tmux call from every atelier
  process logs to `~/.cache/atelier/debug.log`. When something
  breaks, you have the trace.

---

## Two ways in

### 1. Bundled launcher (the distribution path)

The fastest way to use atelier — no tmux.conf to write, no plugin
manager, no font setup. `atelier` spawns its own tmux server on a
dedicated socket (`tmux -L atelier`) and ships sane defaults out of
the box: system clipboard wired into copy-mode yank, 50k scrollback,
focus-events for vim/nvim, vi mode, truecolor, fast escape-time. The
default statusline is deliberately glyph-free so it renders on any
font; opt into powerline decoration via the override hook below.

```bash
brew tap vyrwu/tap

# Minimum viable install — engine + Claude + workspaces + tool selector.
brew install \
  vyrwu/tap/atelier \
  vyrwu/tap/atelier-workspaces \
  vyrwu/tap/atelier-toolselector \
  vyrwu/tap/atelier-claude \
  vyrwu/tap/atelier-popupshell

# Add whatever else you want, à la carte.
brew install \
  vyrwu/tap/atelier-lazygit \
  vyrwu/tap/atelier-k8s \
  vyrwu/tap/atelier-pg \
  vyrwu/tap/atelier-aws \
  vyrwu/tap/atelier-ghdash \
  vyrwu/tap/atelier-ghenhance \
  vyrwu/tap/atelier-ccusage

atelier
```

`M-q` detaches your client — the tmux server keeps running so
background Claude sessions and other agents stay alive. Reattach with
`atelier` (or `tmux -L atelier attach`). Workspaces persist across
detach/reattach.

**Customizing the bundled mode.** Drop your tmux tweaks into
`~/.config/atelier/tmux.conf.local`; atelier sources it after every
default so your overrides always win. Start from
[`examples/atelier-extras.tmux`](examples/atelier-extras.tmux) — a
powerline-styled snippet you can `cp` into place (requires a Nerd
Font; details in the file header).

### 2. Embed into your own tmux (the real-world path)

```bash
brew tap vyrwu/tap
brew install vyrwu/tap/atelier vyrwu/tap/atelier-workspaces \
             vyrwu/tap/atelier-toolselector vyrwu/tap/atelier-claude
# ...plus any opt-in tools you want (see tap list above).
```

In your `~/.config/tmux/tmux.conf`:

```tmux
run-shell 'atelier init --bare | tmux source-file -'
```

`--bare` emits engine wiring only — bindings, hooks, statusline data
emitters — no theme, no statusline format opinions. Your existing
dracula / gruvbox / nord / powerline / nothing stays exactly as it is;
atelier just adds its behavior on top.

For the load-bearing details — how to wire freshness and attention
into your statusline format — see [docs/EMBEDDING.md](docs/EMBEDDING.md).

### Reference setups

[`examples/tmux/`](examples/tmux/) ships three runnable configs you
can copy as a starting point:

| File | What it is |
|---|---|
| [`minimal.conf`](examples/tmux/minimal.conf) | atelier on vanilla tmux. No theme, no plugins. The smallest possible embedding. |
| [`powerline.conf`](examples/tmux/powerline.conf) | atelier in a powerline-styled tmux. Shows how atelier's stamp-statusline injects emitters into a `` arrow-segment layout. |
| [`vyrwu.conf`](examples/tmux/vyrwu.conf) | **The author's actual daily-driver tmux config.** Dracula + TPM plugins + atelier underneath. The reference for what a real-world atelier setup looks like. |

The only line that matters for atelier integration is
`run-shell 'atelier init --bare \| tmux source-file -'`. Everything
else in each `.conf` is taste — replace, remix, ignore as you like.

### Verify either path

```bash
atelier doctor
# tmux:    tmux 3.6a
# atelier: ok
# Discovered tools (11): aws, ccusage, claude, ghdash, ghenhance, k8s, lazygit, pg, popupshell, toolselector, workspaces
```

---

## Quickstart (inside atelier or your embedded tmux)

Six keys do most of the work:

| Keys | What happens |
|------|--------------|
| `M-;` | **Tool selector** — fzf list of every discovered tool; picks route to the current workspace |
| `M-n` | **New workspace** — natural-language task description → Claude names the branch → worktree + Claude session spawn |
| `M-s` | **Select workspace** — switch between existing workspaces (recap + freshness per row) |
| `M-r` | **Recover workspace** — recently soft-closed workspaces float to top; recover or permanently delete |
| `M-?` | **Cheatsheet** — every active binding, scoped to current context |
| `M-q` | **Detach** — server keeps running; reattach later with `atelier` |

Every popup runs in its own backing tmux session. Opening a tool
doesn't disturb your work; closing it leaves it ready to resume.
Inside a tool's popup, `M-;` still works — you can pivot to another
tool without losing what's underneath.

---

## How it works

```
[ workspace = tmux window backed by a git worktree ]
        │
        │  bind c → set @atelier_outer_pane=$5
        │       → display-popup -E 'atelier tools claude open'
        ▼
[ claude popup session (_atelier_claude_5_3) ]
        │
        │  reads @atelier_outer_pane → knows outer is $5
        │  M-; opens tool selector, which can spawn other tools
        │  on the same outer pane without closing claude
        ▼
[ k8s popup renders on $5, claude popup stays open ]
```

The engine tracks the outer pane in global tmux options. Tools inside
popups read those globals — no parsing of session names, no guessing
about ancestry. Process isolation means a crash in one tool can't
take down the others.

For the full architectural picture, see [DESIGN.md](DESIGN.md).

---

## Authoring a plugin

A plugin is any binary on `PATH` named `atelier-<name>` that:

1. Responds to `--atelier-manifest` by printing JSON.
2. Implements the subcommands the manifest declares.

The manifest declares the top-level keybinding, popup style, picker
contents, and per-popup bindings. Atelier emits `tmux bind` lines
from it at init time; the plugin owns everything inside the popup.

Minimal example (in any language):

```json
{
  "api_version": 1,
  "name": "myplugin",
  "description": "What this does",
  "binding":     { "key": "M-x", "style": "picker", "invoke": "open" },
  "ui":          { "icon": "工", "accent_color": "208", "popup_title": "My Plugin" },
  "popup":       "none",
  "subcommands": ["open"]
}
```

Full plugin authoring guide: [CONTRIBUTING.md](CONTRIBUTING.md).

---

## Configuration

Optional `$XDG_CONFIG_HOME/atelier/config.toml`:

```toml
[workspaces]
code_root       = "~/code/github"
worktree_root   = "~/code/.worktrees/github"
multi_repo_root = "~/code"

[k8s]
contexts = "~/.config/atelier/k8s/contexts.yaml"

[pg]
contexts = "~/.config/atelier/pg/contexts.yaml"
```

All fields have sensible defaults; tools read this directly.

---

## Development

```bash
make build           # build all binaries into bin/
make test            # unit tests (no tmux required)
make test-e2e        # e2e tests against isolated tmux servers
make test-tmux       # launch a sandboxed tmux server with the current build
```

E2E tests spin up `tmux -L atelier-test-<random>` servers — isolated
from your real tmux. Cleanup runs even on panic.

For the release process (release-please, conventional commits,
Homebrew tap publishing), see [RELEASING.md](RELEASING.md).

---

## Inspirations

Atelier exists because of:

- **[Claude Code](https://github.com/anthropics/claude-code)** — the
  daily driver. The workflow patterns atelier supports are shaped by
  what makes Claude Code productive: per-task scope, attention signals,
  resume-on-restart.
- **[k9s](https://github.com/derailed/k9s)** — for the proof that a
  thoughtful TUI in your terminal is preferable to most browser
  alternatives. Atelier-k8s is a thin shell around k9s.
- **[sesh](https://github.com/joshmedeski/sesh)** — for showing how
  a Go binary can extend tmux without becoming a tmux plugin in the
  TPM sense. atelier follows the same "binary on PATH" model.
- **[lazygit](https://github.com/jesseduffield/lazygit)** — for the
  per-workspace TUI surface. atelier-lazygit is just a launcher.
- **[Conductor](https://conductor.build)** — for crystallizing the
  multi-agent-development idea. Conductor is a desktop app; atelier
  takes the same thesis (parallel agents in isolated workspaces)
  into the terminal so you stay in your existing keyboard-driven
  flow.
- **[Neovim](https://github.com/neovim/neovim)** and its distros
  (LazyVim, AstroVim, NvChad) — for the engine-vs-distribution
  framing. atelier-the-engine is portable; atelier-the-bundled-
  launcher is a curated layer on top, the way LazyVim is a layer on
  Neovim.

---

## Status & roadmap

Currently shipping `v0.1.x`. Roadmap and prioritization live in
[FEATURE_REQUESTS.md](FEATURE_REQUESTS.md).

Known limitations of v0.1:
- macOS only in practice (Linux builds exist; not tested daily).
- Expects tmux ≥ 3.4 with `display-popup`.
- Single-author cadence; no SLAs.

---

## License

MIT — see [LICENSE](LICENSE).
