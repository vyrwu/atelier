# Feature Requests

Usability + diagnostic gaps surfaced during the bash→Go port shakedown,
and post-port iteration toward v0.1.0. Each entry is a user story +
acceptance criteria. Grouped by theme, prioritized within each group
(`P0` highest impact / lowest cost first).

**Status legend**: `[ ]` open · `[~]` partial · `[x]` shipped · `[BLOCKER]`
must land before v0.1.0 tag.

---

## v0.1.0 release scope

Target: a stable single-user terminal app for the author + a handful of
early adopters. Not external-SDK ready. Not yet a bundled tmux distro.

**In scope:** the P0-marked items below (`[BLOCKER]`). Everything else
slips to v0.2.0+.

**Ship-readiness checklist** (separate from FRs, tracked at the bottom).

---

## Shipped (post-bash port → present)

Listed for context — these used to be open FRs and are now in.

- **FR-2.1 multi-step build spinner** `[x]` — `spinner.SetStatus` stages: Asking Claude → Fetching → Building worktree → Stamping.
- **FR-2.2 recap freshness in picker** `[x]` — `@attention_recap_ts` rendered as `· 30s` / `· 2h`.
- **FR-5.1 clear `@atelier_outer_*`** `[x]` — emitted from `initgen.bindings`; covered by `bindings_test.go`.
- **FR-5.2 persistence (greatly expanded)** `[x]` — full state cache at `$XDG_CACHE_HOME/atelier/state-<host>.json`, write-through on SetRecap/SetAttention/RegisterCreatedWorkspace, on-disk restore on every tmux startup. Includes default-branch + clone-path persistence and live-state warmup for sessions that pre-date persistence.
- **FR-7.1 background pull on workspace open** `[x]` — every former sync `pullDefault` callsite now spawns `_bg-pull` detached (Setpgid so it survives parent popup close).
- **FR-7.2 `_bg-pull` subcommand** `[x]` — `atelier tools workspaces _bg-pull <repo> <branch> <wid>`; logs to debug.log; stamps `@workspace_pull_error` on failure.
- **FR-7.3 freshness tmux options** `[x]` — `@workspace_freshness_ts`, `@workspace_behind`, `@workspace_ahead`, `@workspace_pull_error` stamped per window.
- **Workspace primitive** (not previously an FR) `[x]` — `internal/workspace/` owns session/window lifecycle, persistence, restore, write-through. Tools no longer call `switch-client` / `new-window` directly (enforced in CLAUDE.md).
- **Statusline freshness icon** (extension of FR-7) `[x]` — `✔` / `↓N` / `↑N` / `↓N↑M` / `⚠ <msg>` rendered between window name and attention rollup, idempotent stamp-statusline survives `tmux source-file` loops, Powerline-glyph-aware injection anchor.
- **WarmupFreshness on every restore** `[x]` — scans live tmux for git windows lacking freshness data, fires bg-pull to populate. Robust default-branch resolution: `origin/HEAD` → `origin/{main,master}` → local HEAD.
- **M-? cheatsheet popup** (FR-1.1 + FR-1.2 merged) `[~]` — single popup carries Keybindings + Diagnostics sections. Per-picker scoping NOT done (see FR-1.1 below).
- **M-q kill-server quit** (not previously an FR) `[x]` — explicit quit binding so the user doesn't have to remember `prefix + : kill-server`.
- **Tool Selector** (not previously an FR) `[x]` — M-; opens the tool picker.
- **Detach stale popups on workspace switch** (bugfix) `[x]` — popups from prior workspaces no longer linger on the outer client after M-s.

---

## OPEN FRs — re-prioritized for v0.1.0

### 1. Discoverability & recovery

#### FR-1.1 (P2) — Per-picker keybind cheatsheet overlay
**Status:** `[~]` partial. Global M-? popup exists; per-picker scoped overlay does not.

**As a** user opening a picker for the first time,
**I want** `?` inside a picker to show *that picker's* binds with their actions,
**so that** I don't get the global cheatsheet when I want fzf-local bindings.

**Acceptance:**
- `?` in any atelier fzf picker opens an overlay listing key → action pairs scoped to *that* picker.
- Esc/`?` again closes overlay, restores prior query/cursor.
- Cheatsheet content lives next to the fzf-args builder.

#### FR-1.2 (P0) `[BLOCKER]` — `atelier doctor` extended runtime checks
**Status:** `[~]` partial. Doctor currently checks tmux version + per-tool `requires`. The FR's actual diagnostic content is missing.

**As a** user diagnosing a broken flow,
**I want** doctor to surface common drift,
**so that** I don't grep debug.log to find the obvious cause.

**Acceptance — checks to add:**
- Stale `_atelier_*` / `_claudepop_*` / `_popup_*` sessions with `__` suffix → suggest `atelier popup cleanup`.
- `~/.cache/atelier/claude/settings.json` exists; auto-create if absent.
- Statestore cache file exists and is parseable (warn on schema mismatch).
- Worktree directories referenced by the cache still exist on disk; offer prune.
- `atelier-*` binaries reachable on `PATH`.
- tmux config: `set -gF` used in atelier bindings; `escape-time` ≤ 50ms.
- Bundled init's freshness segments present in `window-status-format` (catch host-config wipe-out bugs).

#### FR-1.3 (P1) — Undo last workspace creation
**As a** user who accepted a bad Claude-generated branch name,
**I want** a one-shot undo,
**so that** I don't manually `git worktree remove` + `git branch -D` + `tmux kill-window`.

**Acceptance:**
- `atelier tools workspaces undo` (or M-z binding) operates on the most-recently-created workspace.
- Records last creation in `~/.cache/atelier/last-workspace.json` on every successful build.
- Confirms, then `git worktree remove --force` + `git branch -D` + `tmux kill-window`.
- Refuses to undo if HEAD has moved (real work done).

### 2. Status & feedback

#### FR-2.3 (P2) — Clean-completion indicator
**As a** user looking at the picker,
**I want** a muted ✓ on Claude-backed rows that completed without raising attention,
**so that** I can tell "Claude finished cleanly" apart from "Claude is mid-task".

**Acceptance:**
- New tmux option `@last_claude_completion_ts` set by `notify-attention` every time, regardless of attention.
- Icon priority: `❯` (current) → `⏺` (attention) → `✓` (recent clean completion, <5min) → `○` (claude running, no recent completion).
- 5-minute threshold configurable.

### 3. Flow ergonomics

#### FR-3.1 (P1) — Ctrl-z back to picker after creation
**As a** user who just created a workspace in the wrong repo,
**I want** a "go back" affordance,
**so that** I don't hit M-; → New Workspace from scratch.

**Acceptance:**
- Post-creation message hints `Ctrl-z within 5s to undo + reopen picker`.
- Ctrl-z within window: runs `atelier tools workspaces undo` (FR-1.3) + reopens creator pre-filled with the previous prompt.

#### FR-3.2 (P1) — Picker shows last-used + ahead/behind
**Status:** unblocked — `@workspace_behind/ahead` now exist (FR-7.3 shipped). The render-layer work is the remaining slice.

**As a** user with 10+ workspaces,
**I want** each row to show last-used time and divergence,
**so that** I can prioritize what to resume.

**Acceptance:**
- Row format: `<session>/<window>  <last-used>  <±N>  <recap>`
- `<last-used>` from `#{session_last_attached}` (epoch → relative).
- `<±N>` from the existing `@workspace_behind/@workspace_ahead` (no new git calls).
- Blank fallback when fields are empty.

#### FR-3.3 (P2) — Prompt history + retry pre-fill
**As a** user whose Claude-generated name failed validation,
**I want** the prompt picker to pre-fill with my last input,
**so that** I refine instead of re-typing.

**Acceptance:**
- On Claude validation failure, `runWorkspacePrompt` sets `query = prompt` before reopening fzf (verify still true).
- Last 5 prompts stored in `~/.cache/atelier/prompt-history.json`; Up-arrow recall in the prompt picker.

### 4. Naming & safety

#### FR-4.1 (P1) — Edit-before-commit Claude name
**As a** user about to accept a Claude-generated branch name,
**I want** a confirmation step (accept / edit / regenerate),
**so that** bad names don't require manual cleanup.

**Acceptance:**
- Tiny picker after Claude returns: candidate as pre-filled query, header `feat/foo-bar · Enter=accept · Ctrl-R=regenerate · type to edit`.
- Edit → revalidate against `conventionalBranchRe`. Ctrl-R → re-ask Claude.
- Opt-out via `[claude] auto_accept_branch_name = true`.

#### FR-4.2 (P0) `[BLOCKER]` — Refuse nested worktree creation
**As a** user inside an existing worktree,
**I want** atelier to refuse creating a new worktree from inside another worktree,
**so that** I don't accidentally nest them and corrupt git's bookkeeping.

**Acceptance:**
- PickCommand checks `cwd` against `git worktree list --porcelain`.
- If cwd is itself a worktree, error: `cannot create worktree from inside another worktree — open from the main repo`.
- Override via `--allow-nested` (CLI only; not exposed in picker).

**Why blocker:** silent data-loss surface for v0.1.0 users who happen to start atelier from inside a worktree.

### 6. Diagnostics

#### FR-6.1 (P0) `[BLOCKER]` — `atelier debug summary`
**As a** user (and the maintainer responding to bug reports),
**I want** a one-shot summary that classifies the debug log,
**so that** I don't grep manually.

**Acceptance:**
- `atelier debug summary [-n duration]` (default: last 5 min) groups entries into:
  - **Errors** — `err` lines, `cmd ... → ERR` lines.
  - **Slow tmux calls** — `cmd` entries with end-to-end > 100ms (requires entry+exit timestamps).
  - **Hot paths** — top 5 most-called tmux commands by count.
  - **Decision log** — every `log` line.
- Single-screen friendly, ANSI-colored.

**Why blocker:** the only way bug reports get triaged in <5 min instead of "send me your full debug.log."

#### FR-6.2 (P2) — `atelier replay`
**As a** maintainer reproducing a user-reported bug,
**I want** to replay the tmux command sequence from a captured debug.log against a sandbox tmux.

**Acceptance:**
- `atelier replay <debug.log> [--socket sandbox]` parses every `cmd tmux <args>` and re-issues them in order.
- Stubs popup `-E` invocations with the expected stdout from the log.
- Prints a diff between log's recorded output and replay's actual output at each step.

#### FR-6.3 (P3) — JSON-lines log format
**As a** maintainer auditing flows over weeks,
**I want** the debug log in JSON-lines format.

**Acceptance:**
- `ATELIER_DEBUG_FORMAT=json` flips `debuglog.LogCmd/Logf/LogErr` to JSON.
- Fields: `ts`, `pid`, `binary`, `kind`, `args`, `stdout`, `exit`, `duration_ms`, `context`.
- `atelier debug tail/last` sniffs the format from the first line.

### 7. Freshness (FR-7.1/7.2/7.3 shipped — picker + UX polish remain)

#### FR-7.4 (P1) — Picker freshness column
**Status:** options exist (FR-7.3 shipped). Render-layer work only.

**As a** user looking at Select Workspace,
**I want** a status column showing each row's sync state,
**so that** I know which workspaces are stale before opening them.

**Acceptance:**
- Row layout: `<sigil> <session>/<window>  <freshness>  · <recap>` — same encoding as the statusline icon (`✔` / `↓N` / `↑N` / `↓N↑M` / `⚠`).
- BuildSessionList reads `@workspace_*` fields in the same `list-windows -F` call (already covers `@attention_recap_*`).

#### FR-7.5 (P2) — Stale-data hint after threshold
**As a** user who opens a workspace they last viewed weeks ago,
**I want** the freshness column to indicate when the data itself is old,
**so that** I know `↓3` is from a fetch 2 weeks ago, not 30 seconds ago.

**Acceptance:**
- If `@workspace_freshness_ts` is older than `[workspaces] freshness_warn_age_s` (default 1h), prepend `?` (e.g. `?↓3`) and dim further.
- Surface in `atelier doctor` (FR-1.2): "workspace X has stale freshness data from 12d ago".

---

## Post-v0.1.0 (P2/P3 — kept for context, not in 0.1 scope)

These were already P2/P3 in the prior version of this doc and remain so.
Listed only by title; full text preserved in git history.

- **FR-8.1** Manager v1: conductor with whitelisted tools (P2)
- **FR-8.2** Manager v2: bidirectional handoff (P3)
- **FR-8.3** Manager: external-surface control (P3 / vision)
- **FR-8.4** Manager: mobile remote view (P3 / vision)
- **FR-9.1** `pkgs/` layout + first migration (P2)
- **FR-9.2** Tighten exported surface as a contract (P3)
- **FR-9.3** Cross-cutting bug fixes become `pkgs/` helpers (P3)
- **FR-9.4** Versioned SDK + external plugin (P3)
- **FR-10.1** Standalone entry point owning the tmux server (P2)
- **FR-10.2** Plugin mode preserved as power-user secondary path (P3)
- **FR-10.3** Versioned tmux config + compatibility hint (P3)

The Manager (FR-8) is a thesis-level addition that should land *on top
of* a stable v0.1 — not as part of it. The pkgs/ rework (FR-9) and the
standalone pivot (FR-10) retroactively justify a lot of the
"composability" workarounds in the codebase, but both are gated on
having external users; neither is required to ship v0.1.0 to the
author + early adopters.

---

## v0.1.0 ship-readiness checklist (non-FR items)

These are gates separate from feature work. Track here so they aren't
forgotten when the FR backlog hits empty.

### Bugs blocking ship
- [ ] **Statusline shows two checkmarks** (reported 2026-06-16; repro unclear) — capture a screenshot or `tmux show-options -gv window-status-current-format` snapshot on next occurrence so root-cause is grounded in actual state instead of theory.
- [ ] **`vyrwu/atelier` workspace shows `⚠ fetch failed`** — correct behavior given the repo has no remote branches, but verify the truncation/render doesn't break user theme alignment on a wide range of error messages.

### Docs
- [ ] README: install (one path, the one we recommend), first-run, the 4 keybindings the user needs to know (M-;, M-s, M-?, M-q).
- [ ] CHANGELOG with the v0.1.0 entry — what works, what's beta, what's deferred to v0.2.
- [ ] CLAUDE.md still accurate (verify after FR-1.2 / FR-4.2 land).
- [ ] `atelier --help` text reviewed for every top-level subcommand.

### Quality bar
- [ ] `make test` + `make test-e2e` both clean on macOS aarch64 and x86_64.
- [ ] Clean-machine install: throwaway VM / fresh Mac account → `make install` → verify the M-; flow runs end-to-end with no prior cache.
- [ ] Statusline injection survives a full tmux config re-source (regression check post FR-1.2).
- [ ] `atelier doctor` exits 0 on a clean install, exits non-zero with actionable hints when broken (FR-1.2 acceptance).
- [ ] No goroutine leaks in `_bg-pull` (run 100x in a loop, check `ps aux | grep atelier-workspaces`).
- [ ] Debug log doesn't grow unbounded — confirm there's a rotation or size cap, or document the expected size.

### Release mechanics
- [ ] Version embedded in the binary (`atelier --version`) and reported by `atelier doctor`.
- [ ] Tag + GitHub release with notes.
- [ ] At least one alpha tester running it for a week before tag.

---

## Priority summary (v0.1.0 scope)

| Priority | Count | Items |
|----------|-------|-------|
| **P0 BLOCKER** | 3 | FR-1.2, FR-4.2, FR-6.1 |
| **P1 (v0.1.0)** | 4 | FR-1.3, FR-3.1, FR-3.2, FR-4.1, FR-7.4 |
| **P2 (post-v0.1)** | 4 | FR-1.1, FR-2.3, FR-3.3, FR-6.2, FR-7.5 |
| **P3 / vision** | 11 | FR-6.3, FR-8.*, FR-9.*, FR-10.* |

P0 themes for v0.1.0: **safety** (FR-4.2 nested-worktree refuse), **diagnostics** (FR-1.2 doctor checks, FR-6.1 debug summary). Plus the open statusline-duplicate bug and the ship-readiness checklist above. Everything else slides to v0.2.

The "implemented since the last revision of this doc" section at the
top is the most-load-bearing change: ~40% of the original P0 backlog
shipped (the entire freshness + persistence + spinner-stages + statusline
icon stack). That's why P0 here looks small — most of what was P0 is
done.
