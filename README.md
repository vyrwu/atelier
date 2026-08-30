# atelier

A switchboard for parallel Claude Code sessions. Describe a task; atelier names
it (with Claude), gives it a directory and an agent, and starts it in the
background — so you kick off work without breaking focus, and always know which
session wants you.

## Build & run

```sh
make install     # builds ./bin/atelier and links it into ~/.local/bin
atelier install  # writes the Claude hooks + tmux bindings (once)
atelier          # starts atelier's own tmux server and drops you on the splash
```

Run `atelier` from a plain terminal (not from inside another tmux). You land on
a splash; summon everything with Alt-key bindings.

## Keys

Overlays — from anywhere:

| Key | |
|---|---|
| `M-n` | new workspace — `↵` start in the background · `⌥↵` start & open · `^j` newline |
| `M-a` | active workspaces — `↵` open · `M-d` retire · `M-x` delete |
| `M-w` | all workspaces (incl. retired) — `↵` restore · `M-x` delete |
| `M-r` | pull requests — `↵` open in browser |
| `M-t` | worktrees — `↵` open a dedicated shell in that worktree |

Inside a workspace:

| Key | |
|---|---|
| `M-c` | jump to Claude (respawns & resumes it if you exited it) |
| `M-s` | a shell in the workspace directory |
| `M-q` | detach atelier — back to the shell you started it from |

## The workspace lifecycle

- **New** (`M-n`) builds in the background: Claude names it, lays out its
  directory, launches the agent, and toasts when it's ready — the popup closes
  immediately so you keep working. `⌥↵` also switches you in when it's ready.
- **Active** (`M-a`) is your working set. **Retire** (`M-d`) kills a workspace's
  processes but keeps everything on disk. **Restore** it any time from **All**
  (`M-w` → `↵`), resuming the conversation where it left off. **Delete** (`M-x`)
  is permanent and confirmed.

## How it works

- A **workspace** is a directory under `~/ateliers/<slug>/` plus a tmux session.
  Its `claude` window runs the agent; the agent finds repos and creates worktrees
  itself with the `create_worktree` MCP tool (branched off the freshly-fetched
  default branch, never stale). Worktree windows are dedicated and persistent, so
  `M-t` swaps between them without losing background jobs.
- **Worktrees** are read from disk; **pull requests** from `gh`; agent status
  from Claude's lifecycle hooks. atelier stores only a small index + PR cache in
  one file (`$XDG_STATE_HOME/atelier/state.json`) — never in tmux. Nothing polls.
- Claude opens **draft PRs** on your behalf with the `create_pr` MCP tool
  (rebased onto the latest default branch, using the `gh` token over HTTPS so it
  never prompts for a key). The attention badge counts only sessions blocked
  waiting on you.

## Config

`$XDG_CONFIG_HOME/atelier/config.toml` (all optional):

```toml
root         = "~/ateliers"   # where workspaces live
socket       = "atelier"      # dedicated tmux socket
naming       = true           # name workspaces with Claude
naming_model = "sonnet"       # model used to name them
```

Requires `tmux`, `git`, and `gh` on `PATH`. macOS and Linux.

The design lives in [V1.md](V1.md).
