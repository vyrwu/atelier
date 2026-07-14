# CLAUDE.md — atelier

Project-level instructions for Claude Code (and any other agent working
in this repo). Short, durable, mechanically-enforceable rules. Read
this before touching tool code.

## Architecture principle: tools never re-implement window management

**Rule:** code under `internal/tools/<name>/` MUST NOT directly invoke
tmux verbs that mutate sessions, windows, or popup-client state, and
MUST NOT write atelier-managed window options as string literals. All
of that lives in `internal/workspace` and `internal/popup`.

Concretely, the following are *prohibited* in `internal/tools/...`:

- `tmux new-session`, `new-window`, `kill-session`, `kill-window`,
  `switch-client`, `select-window`, `respawn-pane` (the workspace-
  lifecycle verbs)
- `set-option`/`set-window-option` calls that write
  `@needs_attention`, `@attention_recap`, `@attention_recap_ts`,
  `@claude_prompt`, `@claude_workspace_kind`,
  `@claude_active_session_id`, `@repo_path` as string literals — use
  the `workspace.Opt*` constants
- The `set-option <key> + statestore.UpdateGlobal(<key>)` two-step for
  persisted tmux globals — use `workspace.SetPersistedGlobal`
- The "spawn workspace-scoped popup" four-step recipe (resolve parent
  context, ensure backing session, apply popup style, attach) — use
  `popup.OpenWorkspaceScoped` / `OpenWorkspaceScopedWithCmd`
- The `key-table popup ; status off ; prefix None ; prefix2 None ;
  aggressive-resize on` popup-style sequence — use `popup.ApplyStyle`
- The `TMUX_PARENT_SESSION_ID/WINDOW_ID env → atelier globals →
  current-pane` parent-context resolution — use
  `popup.ResolveParentContext`

**Why:** every tool that opens a popup, creates a workspace, lands the
outer client on a workspace, or stamps workspace metadata hits the
same edge cases — picking the right outer client, ordering
select-window before switch-client, killing auto-created default-branch
windows, sigil-restoring stripped `$`/`@` env values. Inlined in each
tool, one bug fix has to touch every tool. In the primitive, fixes
land once. The five-copy `applyPopupStyle` extraction (Layer A) and
the persistence write-through helpers (Layer B in progress) exist
because we kept hitting the same bug class until we accepted that.

**Where to add new behavior:**

- New per-window option key → `internal/workspace` constants block
- New cross-tool tmux operation → `internal/workspace` or
  `internal/popup`, picked by whether it's about the workspace or
  about a popup tool
- New tool-specific UX (fzf binds, picker logic, custom transforms) →
  tool's own package, no restriction

If you find yourself reaching around the primitive ("I'll just call
tmux directly here, it's faster"), STOP. Add the helper to the
primitive first, then call it. The fast path is the trap.

See [`DESIGN.md` → "Window management belongs to the workspace
primitive"](DESIGN.md) for the longer rationale, and
[`REFACTOR.md`](REFACTOR.md) for in-flight extraction work.

## Testing rule

Every bug fix and feature lands with tests. Pure-unit tests where the
helper is pure (e.g., `formatStageLabel`, `formatRecapAge`,
`interpretPickedRepo`, `dispatchMode`); integration tests via
`internal/testtmux` where tmux is involved. See `feedback_test_every_fix`
in user memory for the explicit "no manually-verified-only fixes" rule.

## Commit + PR rules

Per user's global `~/.config/claude/CLAUDE.md` — `[PLA-XXX] type(scope):
Description` commit subject, concise body, `Co-Authored-By: Claude
<model>` trailer. Pull requests follow the same title format; body has
`## Summary` (3-5 bullets), separator, repo PR template if present,
generated-by footer. Don't enumerate files in PR bodies; don't expand
design rationale.

## GitHub Issue conventions

Feature requests use `.github/ISSUE_TEMPLATE/feature_request.md`:
**Problem** (the friction/gap) → **Proposal** (the concrete change) →
**Notes** (where it lands in the code, constraints, alternatives;
optional). Keep them short and focused. Label every issue `enhancement`
plus exactly one area label — `ux`, `usability`, or `config`. Prefix the
title with the area when it aids scanning (e.g. `UX: divider between M-s
entries`).

## When the principle doesn't hold

If a future tool genuinely needs a tmux primitive that doesn't exist
yet (e.g., `move-window` for a workspace reorganizer), ADD IT TO THE
PRIMITIVE first, then use it. The rule is enforceable mechanically and
the deflection move ("I'll inline it just this once") is exactly the
pattern this rule exists to prevent.
