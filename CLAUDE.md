# CLAUDE.md — atelier (v1)

A switchboard for parallel Claude Code sessions. The full spec is [V1.md](V1.md);
read it before making changes. This is a prototype under active shaping.

## Rules (V1.md §6 — tripwires, not sentiments)

- **One agent (Claude), one forge (GitHub), one renderer (Bubble Tea).** A second
  implementation means deleting the first. No abstraction exists for a
  hypothetical second one.
- **All UI is Bubble Tea** (`internal/ui`). No fzf, no second UI technology, no
  shelling out to draw.
- **State lives in one JSON file** (`internal/core`), never in tmux.
- **Nothing polls; no daemon.** State changes are event-driven (Claude hooks).
- **No plugin system.**
- **Ground truth over bookkeeping:** worktrees derive from disk, PRs from `gh`.
  Stored state is a cache + index.

## Layout

- `internal/core` — domain types, the state file, config, paths, slug
- `internal/tmux` — tmux CLI wrapper (dedicated socket)
- `internal/git` — worktree derivation + git queries
- `internal/forge` — `gh` PR queries + open-in-browser
- `internal/agent` — Claude launch/resume, per-session status, hooks, Claude
  config (trust + MCP registration), the per-workspace guide
- `internal/mcp` — stdio MCP server (`register_pr`, `create_worktree`, `create_pr`)
- `internal/ui` — the Bubble Tea overlay + the home splash (the only UI)
- `cmd/atelier` — one binary: `up` / `open` / `home` / `create` / `win` / `hook`
  / `status` / `mcp` / `install`

## Model & bindings

- A workspace = a dir under `~/ateliers/<slug>/` + a tmux session whose `claude`
  window runs the agent (named windows: `claude`, `shell`, `<repo>-<branch>` per
  worktree — nav is keyed on names, so `automatic-rename`/`allow-rename` are off).
- Lifecycle: create → **active**; `M-d` retire (kill processes, keep on disk);
  `M-w` → `↵` restore (resume); `M-x` delete permanently. `Retired bool` on the
  workspace (zero value = active, no migration).
- Overlays: `M-a` active · `M-w` all · `M-n` new (background) · `M-r` PRs · `M-t`
  worktrees. In-workspace: `M-c` Claude (respawns if exited) · `M-s` shell · `M-q`
  detach. New workspaces build via a detached `atelier create`; feedback is a
  tmux `display-message` toast, not a spinner.

## Prototyping phase

- **No tests yet** (by request); CI + release-please + goreleaser are wired.
  Run `go build ./...`, `go vet ./...`, and `golangci-lint run ./...` before
  committing. Keep docs minimal.
- Delete dead code rather than keep it. Elegance and fitness-for-purpose over
  completeness or feature count.
