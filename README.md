# atelier

[![ci](https://github.com/vyrwu/atelier/actions/workflows/ci.yml/badge.svg)](https://github.com/vyrwu/atelier/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/vyrwu/atelier?display_name=tag&sort=semver)](https://github.com/vyrwu/atelier/releases)
[![license](https://img.shields.io/github/license/vyrwu/atelier)](LICENSE)

**TUI-native agentic workspaces.** A tmux + git-worktree workspace
manager for parallel Claude/Codex/Aider sessions and the terminal
tools that go with them. Single Go binary, curated built-in tools,
config-declared launchers, opinion-free statusline API.

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
- **A load-bearing kernel, swappable integrations.** The kernel owns the
  views (workspace creator/selector/history, tool picker, help) and the
  *capability slots* in them — a per-row AI **summary**, an **attention**
  sigil, a code-forge **badge**. It doesn't own who fills them: an
  **integration** is a bounded adapter that satisfies a kernel port
  (`AIIntegration`, `ForgeIntegration`) and is selected in config. Claude
  is the default AI; swap it for codex/gemini/a mock by config, not a
  rewrite. GitHub fills the forge badge; a GitLab adapter would too.
  Predictable over dynamic — the kernel defines the contract; adapters
  satisfy it.
- **Extend with a launcher, not an SDK.** For a plain TUI, register *any*
  command with a `[tools.<name>]` block in `config.toml`: atelier binds a
  key, opens it in a popup, and owns the window state. Wrap k9s with AWS
  SSO in a `aws-vault-k9s` script and point a launcher at it — no Go, no
  protocol, no recompile. (Built-in tools that carry real logic — k8s,
  pg, workspaces — are compiled in and dispatched via `atelier tools <name>`.)
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
brew install vyrwu/tap/atelier
atelier
```

One binary ships every built-in tool. Their *external* dependencies
(k9s, pgcli, lazygit, gh, aws-vault, node, …) are optional — install
only what the tools you actually use need; `atelier doctor` tells you
what's missing. The cask pulls in the two hard deps, `tmux` and `fzf`.

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
brew install vyrwu/tap/atelier
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
# [PASS] tmux version            tmux 3.6a
# [PASS] tools registered        5 built-in
# ...
# Discovered tools (5):
#   k8s                  Singleton k9s popup ...
#     requires: k9s                  ok
#   workspaces           Workspace picker, session switcher, clone-from-URL ...
#     requires: git                  ok
#   ...
```

The AI agent (Claude) and code forge (GitHub) are **integrations**, not
tools — selected via `[integrations]` in config, not listed here.

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
        │       → display-popup -E 'atelier ai open'
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
about ancestry. Each popup spawns its own `atelier` process (one binary,
but a separate process per popup), so a crash in one tool can't take down
the others.

For the full architectural picture, see [DESIGN.md](DESIGN.md).

---

## Extending atelier

Three mechanisms, by what you're adding.

**1. A launcher (no code).** To run a plain TUI in a popup, register any
command with a `[tools.<name>]` block in `config.toml`. Atelier binds the
key, opens the command in a popup of the declared shape, and owns the
window state — it doesn't care that the command isn't an atelier binary.
This is the answer for "I want k9s, but authenticated through AWS SSO
first":

```toml
[tools.k9s-aws]
launch       = "aws-vault-k9s"   # any executable on PATH (a script you wrote)
popup        = "global"          # workspace | global | none
key          = "K"               # optional tmux binding
requires     = ["aws-vault-k9s"] # atelier doctor checks these
icon         = "胡"
accent_color = "110"
title        = "K9s (AWS)"
description  = "k9s with AWS SSO auth"
```

`atelier tools list` shows it; `atelier doctor` checks its `requires`;
`M-;` lists it in the selector.

**2. An integration (swap a capability).** To change *who fills a kernel
capability* — the AI agent that names branches / summarizes / raises
attention, or the code forge behind the PR badge — write an adapter that
satisfies the kernel port (`internal/integration`: `AIIntegration`,
`ForgeIntegration`) and select it in config:

```toml
[integrations]
ai    = "claude"   # the AI agent adapter (default: claude; "" disables it)
forge = "github"   # the code-forge adapter (default: off)
```

Bundled adapters live in `internal/adapters/{claude,github,mock}`.
Adding `codex`/`gemini`/`gitlab` = a new adapter implementing the same
port + one line in the composition root (`cmd/atelier/integrations.go`).
The kernel never changes — it drives whatever adapter is installed.

**3. A built-in tool (a PR).** Tools with real pre-launch logic (k8s /
pg / aws context+auth pickers) are Go packages under
`internal/tools/<name>` exposing a `Manifest` + `AddCommands`, registered
in `internal/tools/all`, dispatched via `atelier tools <name>`.

Full guide: [CONTRIBUTING.md](CONTRIBUTING.md).

---

## Configuration

Optional `$XDG_CONFIG_HOME/atelier/config.toml`:

```toml
[integrations]
ai    = "claude"   # AI agent adapter (default: claude; "" disables)
forge = "github"   # code-forge badge adapter (default: off)

[workspaces]
code_root       = "~/code/github"
worktree_root   = "~/code/.worktrees/github"
multi_repo_root = "~/code"

[k8s]
contexts = "~/.config/atelier/k8s/contexts.yaml"

[pg]
contexts = "~/.config/atelier/pg/contexts.yaml"

# [tools.<name>] launcher blocks (see "Extending atelier") register
# arbitrary TUIs in popups.
```

All fields have sensible defaults; the kernel and each tool read this
directly.

---

## Development

```bash
make build           # build the atelier binary into bin/
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
  per-workspace TUI surface. atelier ships it as a `[tools.lazygit]`
  config launcher, not compiled-in.
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

Currently shipping `v0.3.x`.

Known limitations:
- macOS only in practice (Linux builds exist; not tested daily).
- Expects tmux ≥ 3.4 with `display-popup`.
- Single-author cadence; no SLAs.

---

## License

MIT — see [LICENSE](LICENSE).
