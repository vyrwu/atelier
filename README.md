<div align="center">
<pre>
      _       _ _
  __ _| |_ ___| (_) ___ _ __
 / _` | __/ _ \ | |/ _ \ '__|
| (_| | ||  __/ | |  __/ |
 \__,_|\__\___|_|_|\___|_|
</pre>

<h1>atelier</h1>

<p><em>A switchboard for parallel Claude Code sessions.</em></p>

<p>
<a href="https://github.com/vyrwu/atelier/releases"><img src="https://img.shields.io/github/v/release/vyrwu/atelier?style=flat-square" alt="Release"></a>
<a href="https://github.com/vyrwu/atelier/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/vyrwu/atelier/ci.yml?branch=main&style=flat-square" alt="Build"></a>
<a href="https://goreportcard.com/report/github.com/vyrwu/atelier"><img src="https://goreportcard.com/badge/github.com/vyrwu/atelier?style=flat-square" alt="Go Report Card"></a>
<a href="LICENSE"><img src="https://img.shields.io/github/license/vyrwu/atelier?style=flat-square" alt="License"></a>
</p>
</div>

Running several Claude Code agents at once is powerful but taxing — you
context-switch constantly and are never quite sure which session is working,
which is blocked waiting on you, and which is done. **atelier makes parallel
agents something you _have_, not something you _manage_.**

Describe a task and it becomes a **space**: a named directory with its own agent,
launched and already running in the background before you've finished reading the
confirmation. One keystroke shows your active spaces; a single marker tells you
which agent actually needs you. Pull requests and worktrees surface on their own,
and the agents open their own draft PRs on your behalf — you review rather than
wrangle.

One binary. tmux for the panes, [Bubble Tea](https://github.com/charmbracelet/bubbletea)
for everything you see.

## Features

- **Kick off work without breaking focus.** New spaces build in the background —
  you stay on the task in front of you and come back when you want.
- **One glance to know where to look.** A single attention marker flags the agent
  that's blocked on you; everything else stays quiet.
- **The forge comes to you.** Agents open their own draft pull requests; PRs and
  worktrees surface on their own, with live CI, review, and comment status.
- **Change PR state in place.** Open, close, or convert to draft without leaving
  the terminal — the icon flips instantly and reconciles with GitHub.
- **Finish without losing anything.** Retire a space to clear it from your active
  set; restore it later and the agent picks the conversation back up.
- **Fresh by construction.** Agents branch worktrees off the freshly-fetched
  default and commit as _you_, not the machine.
- **No daemon, nothing polling.** State is event-driven off Claude's lifecycle
  hooks. Worktrees come from disk, PRs from `gh`, and it all runs on its own tmux
  socket so it never touches your main session.

## Installation

**Homebrew** (macOS/Linux):

```sh
brew install --cask vyrwu/tap/atelier
```

**Go:**

```sh
go install github.com/vyrwu/atelier/cmd/atelier@latest
```

**Releases:** grab a prebuilt binary from the [releases page](https://github.com/vyrwu/atelier/releases),
extract it, and move `atelier` onto your `PATH`.

**From source:**

```sh
git clone https://github.com/vyrwu/atelier && cd atelier
make install   # builds ./bin/atelier and links it into ~/.local/bin
```

## Getting started

```sh
atelier install   # writes the Claude hooks + tmux bindings (run once)
atelier           # starts atelier's own tmux server and drops you on the splash
```

Run `atelier` from a plain terminal — not from inside another tmux. You land on a
splash; press **`M-n`** to describe your first task, and it starts building
straight away. (`M-` is <kbd>Alt</kbd>/<kbd>Meta</kbd>.)

## Keys

Everything is a keystroke away. Overlays open from anywhere:

| Key | Overlay |
|-----|---------|
| `M-s` | **Spaces** — your active working set |
| `M-p` | **Pull requests** — for the current space |
| `M-w` | **Worktrees** — for the current space |
| `M-t` | **Trash** — retired spaces, restorable |
| `M-n` | **New** space |
| `M-h` | **Help** — the full keymap, version, and a dependency check |

Inside a space:

| Key | |
|-----|---|
| `M-a` | jump to the **agent** (Claude) — respawns and resumes it if you exited |
| `M-c` | a **command line** in the space directory |
| `M-q` | **detach** atelier — back to the shell you launched it from |

Actions within each overlay:

| Overlay | Keys |
|---------|------|
| **Spaces** | `↵` open · `M-d` delete (→ Trash) |
| **Trash** | `↵` restore (resumes the conversation) · `M-d` delete permanently |
| **Pull requests** | `↵` open in browser · `M-o` reopen · `M-c` close · `M-d` convert to draft · `M-y` copy as a Markdown link |
| **Worktrees** | `↵` open a dedicated, persistent shell in that worktree |
| **New** | `↵` start in the background · `⌥↵` start & switch in · `^j` newline |

## How it works

- A **space** is a directory under `~/ateliers/<slug>/` plus a tmux session. It's
  named instantly with a memorable handle (`spry-otter`) and renamed to a
  descriptive title by Claude a moment later, mid-work. The status line shows the
  agent's live state — working, blocked on you, or idle — pushed straight from
  Claude's lifecycle hooks.
- **Worktrees** are created by the agent with a `create_worktree` MCP tool that
  branches off the repository's freshly-fetched default branch — never stale. The
  Worktrees overlay shows each one's freshness (ahead/behind the default) at a
  glance; opening one gives it a dedicated persistent shell.
- **Pull requests** are opened by the agent as drafts (`create_pr`, rebased onto
  the latest default) and surface in the Pull requests overlay grouped by repo,
  with GitHub Octicon state, CI, review, and comment status. Status refreshes when
  you open the view (cached briefly for snappy reopens).
- **Attribution is yours.** Agents commit under your git/GitHub identity, not the
  machine's default — even under an isolated dev instance.
- **Ground truth over bookkeeping.** Worktrees are read from disk, PRs from `gh`.
  atelier keeps only a small index + cache in one JSON file
  (`$XDG_STATE_HOME/atelier/state.json`), never in tmux. Nothing polls; there is
  no daemon.

## Configuration

Optional, at `$XDG_CONFIG_HOME/atelier/config.toml`. Every field has a sensible
default, so a fresh install with no config just works:

```toml
root         = "~/ateliers"   # where spaces live
socket       = "atelier"      # dedicated tmux socket
naming       = true           # name spaces with Claude
naming_model = "sonnet"       # model used to name them
```

## Requirements

- **[tmux](https://github.com/tmux/tmux)**, **git**, and the **[GitHub CLI](https://cli.github.com)**
  (`gh`), authenticated with `gh auth login`.
- **[Claude Code](https://claude.com/claude-code)** (`claude`) — the agent atelier drives.
- A **[Nerd Font](https://www.nerdfonts.com)** in your terminal — the Pull
  requests and Worktrees views render GitHub Octicon glyphs.

macOS and Linux.

## Design principles

atelier is deliberately one-of-everything:

- **One agent (Claude), one forge (GitHub), one renderer (Bubble Tea).** No
  abstraction for a hypothetical second one.
- **All UI is Bubble Tea.** No second UI technology, no shelling out to draw.
- **State lives in one JSON file,** never in tmux — a cache and an index.
- **Nothing polls; no daemon.** State changes are event-driven off Claude's hooks.
- **No plugin system.**

## Status

atelier is a personal prototype under active shaping — expect sharp edges and
breaking changes. The design is written up in [V1.md](V1.md).

## License

[MIT](LICENSE)
