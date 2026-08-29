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

4. **M-c in fzf; bubbletea reintroduced for the M-n input only.** The pickers
   (M-s, M-c) stay fzf — two-line-row pickers with confirm-gated actions,
   reusing the proven live-reload + confirm-prompt patterns. But per a later
   user decision, **bubbletea is re-adopted for the M-n intent input** (see the
   M-n entry below): a free-text box is not a picker, and `bubbles/textarea`
   is the right widget for it. So issue #86 is **not** closed won't-do
   wholesale — bubbletea lands, scoped to this one view; fzf remains the
   picker technology.

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

- **M-n is a free-text INPUT field (bubbletea textarea), not an fzf picker.**
  The intent prompt is text entry ("what are we doing today?"), not selection —
  fzf is the wrong tool (its query line is a filter, not a paragraph input).
  M-n opens a **compact, centered, rounded-border FLOATING popup**
  (`manifest.StyleInput` → `-b rounded -T "New Workspace" -w60% -h30%`, no `-y`
  anchor so tmux centers it) hosting a `bubbles/textarea` inside a small
  bubbletea program (`internal/textprompt`): a kanji-prefixed heading
  (`栽 What are we doing today?`, the workspaces tool icon), the field, and **no
  in-view legend** (the border title frames the popup instead). The model's
  Update/View is pure and unit-tested.
  - **textarea's default editor styling is kept, not flattened.** `textarea.New()`
    ships the look the user asked for — line numbers, a ThickBorder left-gutter
    prompt, and a cursor-line highlight — so we DON'T disable line numbers or
    override the prompt. (An earlier pass flattened all of that; reverted.)
  - **Enter SUBMITS; newline is an explicit gesture.** `InsertNewline` is
    rebound off Enter onto `shift+enter` / `alt+enter` / `ctrl+j`. Enter (no
    Alt) is intercepted to submit; Alt+Enter falls through to insert a newline.
    Shift+Enter works only where the terminal reports enhanced keys (kitty /
    modifyOtherKeys); Alt+Enter and Ctrl-J are the always-works fallbacks under
    tmux. Unit-tested: Enter submits + never newlines, Alt+Enter and Ctrl-J
    insert a newline without submitting.
  - **Border title "New Workspace".** `StyleInput` gained `-T` title support;
    the `M-n` binding sets `Title: "New Workspace"`, and the launcher welcome
    popup (`atelier internal welcome`) mirrors the same StyleInput geometry +
    title so the keybind path and the first-run path look identical.
  - **Iteration history:** first pass reused fzf's `--print-query` (wrong — a
    filter, not input); second pass was a hand-rolled `golang.org/x/term`
    editor in a bordered `StyleFull` popup; the bubbletea textarea in a floating
    `StyleInput` popup is the accepted design, per the user's explicit choice to
    re-introduce bubbletea and use `bubbles/textarea`. Deps added: `bubbletea`,
    `bubbles`, `lipgloss` (promoted from indirect).

- **Notifications = a bubbletea toast popup (`internal/notify`), NOT bubbleup.**
  The two user-facing toasts (create-failed, no-forge) moved off tmux
  `display-message` (a status-line flash) onto a centered, rounded-border,
  severity-colored toast that auto-dismisses after 2.5s or any key. It's a small
  bubbletea/lipgloss program on `/dev/tty`, matching textprompt + the spinner.
  bubbleup was considered and rejected: it's an *in-app overlay* meant to be
  embedded in a long-running bubbletea TUI, but atelier's notifications fire
  from short-lived popup commands (there's no host TUI to overlay). A
  self-contained toast is the fitting analog, and needs no extra dependency.
  No tty → `notify.Show` is a no-op, so the e2e/test paths are unaffected.

- **Spinner rebuilt on `bubbles/spinner`.** `internal/spinner`'s `BoxSpinner`
  keeps its public API (`NewBox`/`Run`/`SetStatus`/`Delay`) but its renderer is
  now a bubbletea program with a `bubbles/spinner` (braille frames, yellow) in a
  centered lipgloss box; `SetStatus` drives it via `program.Send`. The unused
  inline `Spinner` and the hand-rolled raw-ANSI box renderer are deleted (no
  dead code). Headless (no controlling tty) runs the task with no UI, which is
  also the deterministic unit-test path (a `ttyForRender` seam).

## Tooling + M-; changes

- **"Popup" is a first-class built-in again.** The bash selector had a built-in
  "Popup" (a scratch shell in a workspace-scoped popup); the Go port left
  `popupshell` in the selector's canonical order but only surfaced it if a plugin
  by that name was *discovered* — so with no built-in and no config entry it
  never appeared. Restored as a built-in tool (`internal/tools/popupshell`,
  registered in `tools/all`): workspace-scoped, no dedicated key (M-; menu only),
  labeled "Popup". A user's `[tools.popupshell]` config would shadow-skip in
  favor of the built-in, so no duplicate.

- **Workspace-scoped popups open in the active workspace dir.** A workspace-
  scoped popup (popupshell, the AI agent, …) opens in the parent session's
  `@workspace_root`, not wherever the driver pane happens to have `cd`'d. One
  change in `popup.OpenWorkspaceScopedWithCmd` (read `@workspace_root`, use it as
  the start dir, fall back to the pane cwd when the parent isn't a workspace).

- **Lazygit always picks a worktree first.** A workspace spans multiple
  worktrees and its root holds only symlinks (not a git repo), so a per-repo git
  TUI has to choose WHICH worktree. `atelier tools workspaces worktree-open
  <cmd…>` resolves the active workspace, then: 0 worktrees → toast + exit, 1 →
  open there, ≥2 → an fzf worktree picker; then `cd <picked> && exec <cmd>` in
  the popup pty. The sandbox lazygit launcher becomes `worktree-open lazygit`
  with `popup = "none"` so the picker runs FRESH every open (a persistent
  workspace-scoped session would pin one worktree and skip the pick). The 1-vs-≥2
  rule is a deliberate softening of "always pick" — a one-item picker is
  pointless friction.

- **Pgcenter removed.** The `pgcenter` selector entry dispatched to a
  `pg pgcenter` command that never existed (the `pg` tool only ships pgcli) — a
  dead entry. Removed from the selector; pgcli stays.

- **Workspace gestures removed from the M-; tool menu.** "Select / New / Recover
  Workspace" are no longer tool-menu entries — they're top-level M-s / M-n / M-c
  bindings, and "recover" was deleted with the history flow entirely. The
  `workspaces` tool is now suppressed from the selector like `toolselector` is.
  The in-fzf swap binds are fixed too: alt-n → `new` (was a dead `pick`), alt-s →
  `sessions`; the dead alt-r → `recover` bind is gone.

- **AWS Assume tool removed; EKS assume-role shell added.** The `aws` tool (a
  granted profile picker that assumed into the caller pane) is superseded and
  deleted (package + selector entry + `_awspop_`-era references left only as
  inert legacy taxonomy). In its place, a new **EKS** built-in
  (`internal/tools/eks`): pick a context → `granted assume <admin-role>` →
  point kubectl at the matching cluster → drop into an interactive **shell** in
  a popup (so you can run kubectl/helm). It's the k9s tool with the popup
  running `$SHELL` instead of k9s — same `contexts.yaml` shape (name / context /
  authCmd / initCmd), same per-context kubeconfig cache + granted-assume wrap +
  respawn-on-change singleton, but keyed under `atelier/eks` and env-prefixed
  `EKS_CONTEXT_NAME`. Bound to `M-e` and in the M-; menu.
  - **Own `contexts.yaml`, respawn-per-context** (user's calls): EKS reads its
    own `$XDG_CONFIG_HOME/atelier/eks/contexts.yaml`; `open` always shows the
    picker and respawns the shell only when the chosen context differs (same
    context → attach, shell + scrollback preserved).
  - **Cloned, not extracted.** EKS mirrors the k8s tool rather than sharing a
    `kubectx` primitive — extracting one would refactor the load-bearing k8s
    tool (and move its tested unexported helpers) mid-iteration. The duplication
    is the deliberate tradeoff; a shared `kubectx` primitive is a clean future
    follow-up (noted in the tool-isolation allowlist TODOs too).

- **Worktree directories are FLAT within a repo.** A slashed branch ("feat/x")
  used to nest the worktree dir + symlink (repo/feat/x); now the branch leaf is
  flattened to a single segment ("feat-x") via `workspace.WorktreeDirName`, at
  every path-building site (create, the `worktree add` verb, the seed hydrator)
  and in `WorktreeLinkPath`. The git branch name is unchanged — only the on-disk
  directory is flattened. Bonus: the reconcile GC's two-level `<repo>/<branch>`
  link walk is now exactly right (a slashed branch previously nested deeper than
  it walked).

- **Launcher default screen via a `client-attached` hook.** `atelier internal
  welcome` opens M-n when zero workspaces exist; guarded (no-op once one
  exists) + debounced. Bundled/full mode only — an embedded `--bare` user
  drives their own tmux.

- **Statusline: top bar = description + attention + clock; no repo, no forge
  badge.** Per explicit user request the bundled bar moves to `status-position
  top` and `status-left` shows the current workspace's **description** (its
  `@workspace_title`, via a new `atelier status description <session>` emitter
  that reads it through `show-options` — a session user-option doesn't resolve
  in a status FORMAT on every tmux) followed by the global ⏺N attention rollup;
  the clock sits at the right. The repo/session name (`#S`) is gone — a
  workspace is an intent, not a repo. All three `window-status-*` formats are
  empty; the workspace lives in `status-left`, so a repo session's many
  worktree windows never flood the bar.
  - **Attention rollup delivery is mode-split.** Bundled mode renders it from
    `status-left`. Plugin/`--bare` mode never gets `ThemeBlock`, so
    `stamp-statusline` still injects the attention segment non-destructively
    into the user's own `window-status-current-format` (after `#W`). `doctor`'s
    statusline check now accepts the segment in EITHER location.
  - **Launcher session shows "default" in the description slot** (the emitter
    falls back to the session name when `@workspace_title` is unset). Acceptable
    cosmetic; the launcher is transient and the bar is never blank.

- **Removed the per-window forge badge + its `@forge_state` tmux option (dead
  code).** The old bar injected a per-window PR glyph read from a
  `@forge_state` window option. In the intent model a workspace spans multiple
  repos/PRs, so forge state is an **aggregate** on the statestore workspace
  record, rendered in the M-s rollup and M-c view — nothing writes
  `@forge_state` anymore. So the whole read side is dead and removed:
  `atelier status forge` (+ `formatForgeIcon`), the `forgeSegment`/`forgeEmitter`
  and forge alternation in the strip-regex, and `state.Window.ForgeState` (with
  its `@forge_state` capture field, dropping the window capture from 13→12
  fields). The rich `integration.ForgeState` PR model is untouched — only the
  vestigial tmux-option delivery is gone.

## Scope honesty

- Multi-agent (WS-8 / issue #18) is NOT built — one agent per workspace. The
  three forward-compat constraints (Agents list, single resolver, per-window
  agent state) are honored.
- Bindings changed (`feat!:` major bump). Embedded (`--bare`) users must
  re-source — called out in release notes.
