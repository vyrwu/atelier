# CLAUDE.md — atelier (v1)

A workshop for parallel Claude Code agents. The full spec is [V1.md](V1.md);
read it before making changes. User-facing docs are README.md; contributor
docs are CONTRIBUTING.md — keep all three in step with behaviour.

## Rules (V1.md §6 — tripwires, not sentiments)

- **One agent (Claude), one forge (GitHub), one renderer (Bubble Tea).** A second
  implementation means deleting the first. No abstraction exists for a
  hypothetical second one.
- **All UI is Bubble Tea** (`internal/ui`). No fzf, no second UI technology, no
  shelling out to draw.
- **State lives in one JSON file** (`internal/core`), never in tmux.
- **Nothing polls; no daemon.** State changes are event-driven (Claude hooks).
  One scoped exception: the PR view re-queries every minute *while it is open*,
  because GitHub can't push to us. The popup is the process, so closing it ends
  the poll — nothing runs in the background.
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
  / `mcp` / `install` / `version`

## Model & bindings

- A workspace ("space") = a dir under `~/ateliers/<slug>/` + a tmux session whose
  `claude` window runs the agent (named windows: `claude`, `shell`,
  `<repo>-<branch>` per worktree — nav is keyed on names, so
  `automatic-rename`/`allow-rename` are off).
- Lifecycle: create → **active**; `M-d` delete → Trash (kill processes, keep on
  disk); Trash `↵` restore (resume); Trash `M-d` delete permanently (confirm).
  `Retired bool` + `RetiredAt` on the workspace (zero value = active, no migration).
- Overlays: `M-s` spaces · `M-p` PRs · `M-w` worktrees · `M-t` trash · `M-n` new
  (background). `M-h` switches to the home splash (a session switch, not an
  overlay); the keymap, version, and dependency doctor live there — there is no
  Help screen. In-space: `M-a` agent · `M-c` shell · `M-q` detach. Leader is `M-`
  (Alt), hardcoded. New spaces build via a detached `atelier create`; feedback is
  a status-line spinner (`@atelier_spin`) that resolves into a check-mark.
- PR view: `↵` opens the diff as a window, `M-e` its checks, `M-b` the
  browser, `M-o`/`M-c` reopen or close (there is no draft action). The diff is local `git diff <base>...HEAD` in the worktree on the PR's
  head branch in the PR's repo, after fetching the base (instant, offline, no
  size limit — GitHub's diff endpoint refuses past 20k lines); with no such
  worktree or no known base it falls back to `gh pr diff`. It is piped to
  whichever of `diffnav` · `delta` · `less` is installed, chosen by the shell that
  runs it rather than atelier's PATH; none is a dependency. `M-e` opens the PR in
  gh-enhance in a window — as the `gh-enhance` binary if it's on PATH (a package
  manager install isn't registered with gh), else as `gh enhance` — falling back
  to `gh pr checks --watch`. The two windows swap in place: `M-e` in a `pr-*`
  window opens that PR's `ci-*` checks, `M-d` in a `ci-*` window its `pr-*` diff
  (`atelier win <session> checks|diff <window>`, which maps the window name back
  to its PR). Both bindings test the window name first and send the key through
  anywhere else (Alt-d is delete-word). `M-a` is the agent everywhere — never
  overload it, not even in a PR's windows.
- Status line is event-driven: the per-session marker (`@atelier_status`) and the
  attention badge (`@atelier_attention`) are pushed by the Claude hooks. PR status
  is one GraphQL query for the whole space (worktree branches + registered PRs by
  number) on opening `M-p` and once a minute while it stays open; the view shows
  the last sweep, marked `checking github…`, until the query lands. Worktree
  freshness is computed the same way, off the path that builds the model.
- PR rows name their base; stacked PRs (one's base is another's head) render as
  a contiguous run, base first, joined by a gutter line — derived per render,
  never stored. Rows are ordered open · draft · merged · closed, newest first; a
  stack takes its most active member's state.
- The generated tmux fragment advertises 24-bit colour (`terminal-features
  ",*:RGB"`) when `$COLORTERM` says the launching terminal has it.
- Bindings are regenerated from the binary: `up`/`install` always write and
  source the fragment, and the popup and window keys (`open`, `win`) re-sync it
  whenever the server's `@atelier_bindings` marker (a hash the fragment sets
  when sourced) differs from the running binary's — so a rebuild or upgrade
  under a live server fixes itself on first use instead of leaving keys dead.
  Likewise `up` restarts an already-running splash (`respawn-pane`), since a
  running process keeps the binary it started with.

## Working here

- **Tests cover the headless core only** — the domain (state file, paths,
  config), agent status + the Claude hook merge, worktree derivation, the PR query, and list rendering. Anything that shells out to tmux, `gh`, or
  Claude stays untested; don't add an abstraction to make it mockable.
  Run `make test`, `go vet ./...`, and `golangci-lint run ./...` before
  committing. Commits are Conventional Commits (release-please reads them).
- Delete dead code rather than keep it. Elegance and fitness-for-purpose over
  completeness or feature count.
