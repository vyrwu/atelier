# Refactor: workspace primitive owns window management

**Status:** Layer A complete (popup boilerplate); Layer B pending (workspace primitive).

## Layer A — popup boilerplate (DONE)

Extracted into `internal/popup`:
- `popup.Client` — minimal tmux surface interface; `*tmuxhost.Client` satisfies it
- `popup.ApplyStyle(h, session)` — replaces 5 identical copies (claude/lazygit/k8s/pg/popupshell)
- `popup.ResolveParentContext(h)` → `ParentContext{SessionID, WindowID, Cwd}` — replaces 3 copies
- `popup.OpenWorkspaceScoped(h, spec)` + `OpenWorkspaceScopedWithCmd(h, spec, fn)` — collapses the boilerplate

Migrated: `popupshell.go` (104 → 58 lines), `lazygit.go` (77 → 30 lines), `claude.go` (`OpenCommand` body 70 → 25 lines, applyPopupStyle removed), `k8s.go` (style-stamp block 11 → 3 lines), `pg.go` (same).

Tests added: 14 new unit tests in `internal/popup` covering ApplyStyle sequence + scope, ResolveParentContext priority chain (env / global / current / error / sigil-restore / mixed), OpenWorkspaceScoped orchestration order + idempotency + fn override + fn error.

Behavior change: ApplyStyle now returns error instead of swallowing. Three previously-swallowing callers (claude/lazygit/popupshell) inherit error-surfacing semantics through `OpenWorkspaceScopedWithCmd` — defensible since failures here always indicate user-visible issues.

## Layer B — workspace primitive (pending)

Layer A handled popup boilerplate. Layer B handles workspace lifecycle (session create, worktree window stamping, outer-client landing).

**Goal:** stop re-implementing window/session/popup-client management in every tool. Lift the shared mechanics into `internal/workspace` so a fix lands once, and the same bug class doesn't reappear with every new tool.

**Non-goal (deferred):** promoting any of this to a public `pkg/atelier/...` SDK, or building a YAML/JSON declarative tool format. See [DESIGN.md → "Window management belongs to the workspace primitive"](DESIGN.md) for the durable rule this refactor enforces.

---

## What gets extracted

Three helpers, all under `internal/workspace`. They're the smallest set that covers every workspace-related tmux call sprinkled across `internal/tools/workspaces/workspaces.go` today.

### 1. `workspace.EnsureSession`

```go
// EnsureSession creates the workspace's tmux session if absent and stamps
// @repo_path. The auto-created window 1 is pre-named to defaultBranch
// ("main") so the empty-query → open-default-branch flow has a target.
//
// Returns (created bool, err): when created==true, callers adding a
// worktree window should immediately kill the default-branch window
// (workspace.DropDefaultBranchWindow) — the user only asked for one
// workspace, and an unrequested `main` row in the picker is confusing.
// The default-branch window is lazily recreated by EnsureDefaultBranchWindow
// the first time the pull-default flow runs.
func EnsureSession(h *tmuxhost.Client, session, repoPath, defaultBranch string) (created bool, err error)
```

Current location: closure `ensureSession` in `runWorkspaceName` and `runWorkspacePrompt` — two near-identical copies as of writing.

### 2. `workspace.CreateWorktreeWindow`

```go
// CreateWorktreeWindow adds a new tmux window in `session` at `wtPath`
// named `name`, and stamps Claude/workspace metadata on the resulting
// window. Returns the new window's @ID. If session is fresh (caller
// just created it via EnsureSession returning created==true), the
// auto-named default-branch window is killed so the picker only shows
// what the user actually built.
type WorktreeWindowSpec struct {
    Session       string
    WtPath        string
    Name          string
    Prompt        string // → @claude_prompt
    Kind          string // → @claude_workspace_kind ("worktree" | "multi-repo" | "")
    KillDefault   string // default-branch window name to kill if session was just created
}

func CreateWorktreeWindow(h *tmuxhost.Client, spec WorktreeWindowSpec) (windowID string, err error)
```

Current locations: ~4 inline new-window + set-option blocks in `workspaces.go` (`runWorkspaceName` × 2, `runWorkspacePrompt`, `runAutoSession`).

### 3. `workspace.LandOuter`

```go
// LandOuter brings the outer (workspace) client onto session:window.
// Reads @atelier_outer_client to target the right client by name, falls
// back to a bare switch-client if absent. Order is select-window then
// switch-client -c <outer> -t =<session> — select-window doesn't accept
// -c, and the order is what makes the outer client display the chosen
// window (not the popup-client we're being invoked from).
func LandOuter(h *tmuxhost.Client, session, window string) error
```

Current locations: `SessionsCommand` (just fixed for the M-; → Select Workspace → opens-inside-popup bug), `runWorkspacePrompt`, `runAutoSession`. Each currently re-derives outer client lookup + ordering.

---

## Call sites that move to the primitive

| File | Function | Currently inlines | Will call |
|---|---|---|---|
| `internal/tools/workspaces/workspaces.go` | `runWorkspaceName` (closure `ensureSession`) | new-session, rename-window, set-option @repo_path | `EnsureSession` |
| `internal/tools/workspaces/workspaces.go` | `runWorkspaceName` (worktree-add branch and main branch) | new-window + kill-window of default | `CreateWorktreeWindow` |
| `internal/tools/workspaces/workspaces.go` | `runWorkspacePrompt` (closure `ensureSession`) | new-session, rename-window, set-option @repo_path | `EnsureSession` |
| `internal/tools/workspaces/workspaces.go` | `runWorkspacePrompt` (auto-named stamping) | new-window, kill-window, @claude_prompt, @claude_workspace_kind | `CreateWorktreeWindow` |
| `internal/tools/workspaces/workspaces.go` | `runAutoSession` (multi-repo new-session + set-option) | new-session, set-option @claude_prompt, @claude_workspace_kind | A new `workspace.EnsureMultiRepoSession` (similar shape) |
| `internal/tools/workspaces/workspaces.go` | `SessionsCommand` (final switch-client + select-window) | select-window + switch-client -c outer | `LandOuter` |
| `internal/tools/workspaces/workspaces.go` | `runWorkspacePrompt` (after build, switch to new window) | select-window + switch-client -c outer | `LandOuter` |
| `internal/tools/workspaces/workspaces.go` | `runAutoSession` (after build, switch to new session) | switch-client -t =name | `LandOuter` |

After this pass, `tmux switch-client` / `select-window` / `new-window` / `new-session` should appear ZERO times in `internal/tools/workspaces/workspaces.go` (and zero times in any future tool).

---

## Order of operations

1. **Add the three helpers under `internal/workspace`** (no callers yet). Each ships with unit tests against a fake `tmuxhost` (or e2e against an isolated tmux server via `testtmux`).
2. **Migrate `SessionsCommand` → `LandOuter`.** Smallest surface, just-fixed bug — locks in the fix as a re-usable helper.
3. **Migrate `runWorkspaceName` → `EnsureSession` + `CreateWorktreeWindow` + `LandOuter`.**
4. **Migrate `runWorkspacePrompt`.** Includes the multi-stage spinner — the helpers must accept a `stageReporter` interface (or no reporter; the spinner stays in the tool).
5. **Migrate `runAutoSession`.** Add `EnsureMultiRepoSession` if multi-repo semantics differ enough.
6. **Verification step:** `grep -rn "new-session\|select-window\|switch-client" internal/tools/` must come back empty (or only matches that intentionally bypass — none expected today).
7. **Add a lint test** that fails if any file under `internal/tools/` calls those tmux verbs directly via `h.Run(...)`. Lock in the rule.

---

## Acceptance

- All four `internal/tools/workspaces/...` call sites listed above route through `internal/workspace`.
- `grep` test added (step 7) passes.
- Existing tests still pass: `TestInterpretPickedRepo`, `TestFormatRecapAge`, the e2e suite, the AST-scan tests.
- New tests cover the three primitives (created/exists branches for `EnsureSession`, kill-default behavior in `CreateWorktreeWindow`, outer-client fallback in `LandOuter`).
- `DESIGN.md`'s "Window management belongs to the workspace primitive" rule is enforceable (the grep test enforces it).

---

## Deferred (do NOT pull into this refactor)

- Promoting helpers to `pkg/atelier/workspace/...` for third-party tool authors. Premature — API still moves week to week.
- YAML/JSON declarative tool manifests. Wait until SDK surface settles.
- Touching popup orchestration (`internal/host/popup`) — its surface is already separately owned and used directly by tools. Only fold popup mechanics into the workspace primitive if the same bug pattern shows up there.
- Spinner / fzf / picker UX. These stay tool-side — they're the part of UX the tool genuinely owns.
