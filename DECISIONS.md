# DECISIONS.md — v1 intent-workspace redesign

Decision log for the REDESIGN.md implementation. Captures the choices made
where the plan left them open, so they can be reviewed. Each entry: the
decision, and the reasoning. Grouped by the plan's open questions (§7) plus
the calls that surfaced during implementation.

## Plan §7 open questions

1. **Window = agent (resolved in plan).** A workspace is one driver window
   whose cwd is the workspace root, plus inspection shells. Worktrees are
   filesystem artifacts, never windows.

2. **Who creates the first worktrees → BOTH (a) and (b).** Creation-time
   AI repo-selection (option a) ships in Phase 1 via `internal/repoindex`,
   AND the agent can add worktrees via the MCP/CLI control surface (option b,
   Phase 3). They compose exactly as the plan predicted — (b) is additive.
   The full redesign is implemented, so both land.

3. **Workspace root + symlinks.** `~/ateliers/<slug>/` (configurable via
   `[workspaces] workspace_root`), git worktrees stay repo-local at
   `~/code/.worktrees/github/<owner>/<repo>/<branch>` and are symlinked into
   the root as `<repo>/<branch>` exactly as the drawing's `ls` shows.

4. **M-c in fzf, not bubbletea.** Per explicit user instruction to stick with
   fzf. Issue #86 (bubbletea) is closed as won't-do. M-c is a two-line-row
   fzf picker with confirm-gated actions, reusing the M-s live-reload +
   confirm-prompt patterns already proven in the sessions picker.

5. **Worktree history (`M-r`) → removed; M-r = rename.** The separate
   "Workspace History / Recover" picker is superseded: workspaces persist and
   restore via the statestore, so "reopen a closed workspace" is no longer a
   distinct filesystem-crawl flow. M-r now renames the current workspace's
   title (M-s footer). The recover/soft-close machinery is deleted (no dead
   code). Supersedes issue #49 (M-r "closed X ago" badge).

6. **atelier writes to the forge — yes, confirm-gated.** `M-c` close in the
   Changes view is the first mutating forge op. It goes through a `Close`
   port method with a two-step fzf confirm, mirroring the delete-confirm
   pattern. A `[forge] allow_write` config gate defaults to true but lets a
   user make the forge read-only.

## Model transformation

- **Session = workspace/intent; window 1 = driver agent.** The tmux session
  stays the unit (all the LandOuter / popup / restore primitives keep
  working), but its identity becomes the workspace slug (`@workspace_id`) and
  it carries `@workspace_title`/`@workspace_intent`/`@workspace_root`.
  `@repo_path` is retained only as an optional per-worktree hint; a workspace
  no longer needs it.

- **Slug/title split.** Session name = slug (immutable tmux target). Title is
  a separate renameable option. This is what makes M-r rename possible without
  moving tmux targets.

- **Worktrees are statestore records + symlinks, not windows.** A workspace
  owns `Worktrees[]` (repo, branch, path) and `PRs[]`. The link tree under
  the workspace root mirrors them; a repair/GC sweep + a `dangling_worktree_link`
  invariant keep it honest.

- **`Agents` is a length-1 list held by invariant.** Per WS-8 forward-compat:
  a list, not a one-to-one field, so multi-agent never forces a schema v4. A
  `multiple_drivers` invariant enforces exactly one driver window.

- **The `worktree | multi-repo` kind split is deleted.** Per issue #83, a
  workspace is a workspace; a one-repo workspace is just a workspace whose
  agent needed one worktree. `@ai_workspace_kind`, `workspace.Listable(repo,
  kind)`, and the kind-branching in creation all go. `Listable` now keys on
  `@workspace_id`.

## Path canonicalization (WS-2 landmine)

- **Policy: resolve symlinks before any path reaches an AI adapter or the
  claude project-dir derivation.** The driver agent's cwd is the *real*
  workspace root (`~/ateliers/<slug>`, a real dir), never a symlinked
  worktree, so its transcript hashes consistently. Defensively,
  `claudeproj` callers run `filepath.EvalSymlinks` so a symlinked cwd can
  never hash to an empty project dir. Pinned by a test.

## PR model + daemon

- **PRs are a workspace-level statestore section**, keyed by workspace, not a
  per-window tmux option (too many fields). Rich `PullRequest` record.
- **Batched `gh pr list --json … --limit N` per repo**, TTL-throttled, one
  call per repo per sweep — never one `gh pr view` per window. Conditional on
  the repo actually appearing in a live/persisted workspace.
- **Workspace summary is a second AI call shape**, gated on change-detection
  (a content hash of the workspace's worktrees + PR states) and metered like
  the recap call, with the existing budget accounting.

## Control surface (WS-5)

- **Kernel CLI verbs are the contract; MCP is a thin transport wrapper.**
  `atelier workspace worktree add|list`, `atelier workspace context`,
  `atelier pr register|list|close`. `atelier mcp serve` is a hand-rolled
  stdio JSON-RPC MCP server (no new deps — the repo is dependency-frugal by
  design) that dispatches to the same verbs. Registered into the interactive
  agent via `--settings` (the `settingsPath` slot already threaded through
  `buildClaudeStartCmd`). Background `claudegen` calls keep `--tools ""` and
  never get MCP.

## Retired: per-window git freshness

- The status-line freshness segment (git ahead/behind) and its `_bg-pull`
  machinery are **retired**. They were per-*window* = per-*branch*; once a
  window is a driver at the workspace root (not a branch checkout) the signal
  has no window to attach to. WS-7 redefines the daemon's feeds as PR sweep /
  workspace summary / context — freshness is deliberately not among them. The
  branch-level "is there unpushed/unmerged work" signal is now carried by the
  PR state in the M-c view. `bgpull.go`, the `@workspace_freshness_*` options,
  `SpawnBgPull`, and the freshness statusline segment are removed (no dead
  code).

## Kept: workspace tags

- Tags (`M-t`, `@workspace_tag`) survive as an orthogonal grouping feature.
  The drawing's M-s row doesn't feature them, but they're useful and removing
  them is gratuitous churn. Intent-first creation still asks the AI for an
  optional grouping tag in the same naming call. The M-s rollup leads with the
  tag pill when present (absorbs issue #69: repo/branch columns are gone, so
  tag color leads by construction).

## Decisions that surfaced during implementation

- **Dropped clone-from-URL (M-u).** It was reachable only from the old
  repo-picker chain and is tied to the repo-centric model. In the intent model
  repos pre-exist in the code root and are AI-selected; cloning a new repo is a
  pre-req the user does in a shell. Removing it (rather than re-homing it)
  keeps the creation surface to one gesture. The M-u binding and CloneCommand
  are gone.

- **Worktree branch = the workspace slug.** The drawing shows per-repo branch
  names (`feat/x`, `fix/y`); deriving a distinct conventional branch per repo
  from one naming call is more than v1 needs. Each AI-selected repo gets a
  worktree on a branch named after the workspace slug — valid, consistent
  across the workspace's repos, and the agent can rename/rebranch later or add
  more via `atelier workspace worktree add <repo> <branch>` (which takes an
  explicit branch).

- **`@workspace_driver` marker.** An explicit window option marks the one
  driver-agent window, so inspection shells the user opens in the same session
  never count as a second agent (`multiple_drivers` invariant), steal the
  workspace's attention slot, or get a recap sweep. This is the concrete form
  of WS-8's "per-agent state stays addressed by window."

- **MCP via `--mcp-config`, gated by `[ai] mcp` (default on).** The claude
  adapter writes `~/.cache/atelier/claude-mcp.json` registering `atelier mcp
  serve` and passes `--mcp-config` on every interactive launch (fresh + resume).
  Background `claudegen` calls (naming/recap/summary) never get it. The MCP
  server is hand-rolled newline-delimited JSON-RPC — no new dependency (the repo
  is deliberately dependency-frugal).

- **k9s M-c → M-k.** Forced by the drawing's M-c = List Changes. History (old
  M-r) is gone (see §7.5), so M-r is free for rename.

- **Launcher default screen via a `client-attached` hook.** `atelier internal
  welcome` opens M-n when zero workspaces exist; guarded (no-op once one
  exists) + debounced. Bundled/full mode only — an embedded `--bare` user
  drives their own tmux.

## Scope honesty

- Multi-agent (WS-8 / issue #18) is NOT built — one agent per workspace. The
  three forward-compat constraints (Agents list, single resolver, per-window
  agent state) are honored.
- Bindings changed (`feat!:` major bump). Embedded (`--bare`) users must
  re-source — called out in release notes.
</content>
</invoke>
