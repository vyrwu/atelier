# REDESIGN.md — atelier v1: the workspace becomes the intent

Plan doc for the major redesign sketched in the design canvas. Breaks the
drawing into implementable workstreams, grounds each in the code that
exists today, and sequences them into shippable phases.

Companion to [`DESIGN.md`](DESIGN.md) (what atelier *is* today) — this doc
is what changes and in what order. When a workstream lands, its rationale
moves into DESIGN.md and its entry here is struck.

---

## 1. What the drawing says

Six panels, one model change and four surfaces.

| Panel | Claim |
|---|---|
| Conceptual model | A **Workspace** (= an *intent*) contains N **agents** (Claude 1, Claude 2, Codex). Each agent owns N **worktrees**. Each worktree has 0..N **PRs**. Agents can reach into each other's worktrees. |
| Create Workspace (M-n) | One free-text field: *"What are we doing today?"*. No repo picker. Becomes the **default screen on launch**. "Instead of picking repos, you just enter the task at hand. Atelier does the rest." |
| Claude Code | The workspace has a **dedicated directory**. Worktrees are **symlinked** into it, so `ls` shows `helm-charts/feat/something`, `web-app/fix/another`. The agent works from that root. |
| List Changes (M-c) | New view. Rows: `<FORGE> <REPO> #<PR-NO> <CI> <APPROVAL> <N-COMMENTS>` + title line. Footer `M-o open · M-c close`. Replaces per-workspace PR tracking with a **cross-repo aggregate**, and makes PRs **actionable**. |
| Select Workspace (M-s) | Rows: `<TIME> <N-ATTENTION> <N-FORGES> <WORKSPACE_NAME>` + summary line. One row **per workspace**, not per worktree. Footer `M-x delete · M-r rename`. |
| Background Daemon | Three feeds: keeps PR statuses updated, updates workspace summaries, gathers workspace context *into* the agent. |

### The single load-bearing change

> **Today the unit of work is a branch. After this, the unit of work is an intent.**

`session = repo, window = worktree/branch` becomes
`session = workspace/intent, window = agent`, with worktrees demoted from
first-class UI objects to **filesystem artifacts** the agents produce.

Everything else in the drawing is downstream of that sentence.

---

## 2. Where we are today

Grounded, so estimates are honest rather than optimistic.

| Concern | Today | Where |
|---|---|---|
| Workspace identity | tmux session named after a repo slug, stamped `@repo_path` | `internal/workspace/lifecycle.go`, `workspace.go` |
| Unit of work | tmux window = one git worktree = one branch | `CreateWorktreeWindow` |
| Multi-repo | Exists but degenerate: an AI-named session `cd`'d to `~/code` with **no worktrees** | `runAutoSession`, `workspaces.go:2052` |
| Creation flow | repo picker → prompt → AI branch name → worktree window. `M-m` toggles multi-repo | `PickCommand`, `runWorkspacePrompt`, `buildClaudeNamedWorkspace` |
| M-s picker | One row **per worktree window**, columns age/attention/forge-badge/session·window/tag, recap on line 2 | `sessionlist.go`, `forge_badge.go` |
| PR tracking | One state per window (`open`/`draft`/`merged`/`closed`) from `gh pr view --json state,isDraft`, cached in `@forge_state`. No number, CI, approval, comments, or title. `M-o` opens in browser. No write actions | `internal/adapters/github/github.go`, `forge_badge.go` |
| Persistence | statestore **schema v2**: `Workspace(session) → Window(worktree) → Metadata["ai.*"]` | `internal/statestore/statestore.go` |
| Daemon | One loop: git freshness, forge badge, per-window recap + 3-state agent status, loop-safe reconcile | `refresh_loop.go` |
| Agent agency | **None.** Claude is launched into a popup and reads/writes files. It cannot create worktrees, spawn siblings, or tell atelier about a PR it opened. No MCP server exists | — |
| Invariants | `internal/state` — topology, 9 violation codes, `reconcile --fix` | `state/validate.go` |

Two things worth flagging up front: the `ai.*` metadata namespace **encodes
one agent per window** (called out in issue #18), and the statestore's own
doc says *"Schema v2 wipes v1 cache. Migration plumbing added when v2
ships"* — that plumbing was never written, so v3 has to build it.

---

## 3. Workstreams

Nine. WS-1 gates most of the rest.

### WS-1 — Workspace entity: the intent container ⟵ *keystone*

**Change.** Promote the workspace from "a tmux session that happens to be a
repo" to a first-class entity with its own identity, title, intent text,
root directory, and owned sets (agents, worktrees, PRs). Windows become
**agents**, not worktrees.

**Work.**
- New option keys in `internal/workspace`: `@workspace_id` (stable slug),
  `@workspace_title` (human, renameable), `@workspace_intent` (the M-n
  prompt), `@workspace_root` (the dedicated dir). `@repo_path` becomes
  optional — a workspace spans repos.
- Split **identity from label**: session name stays the slug (tmux targets
  depend on it); `M-r` rename edits the title only. Today they are the same
  string, which is why rename doesn't exist yet.
- statestore **schema v3** + a real migration path (see WS-9). Shape:
  `Workspace{ID, Title, Intent, Root, CreatedAt, Agents[], Worktrees[], PRs[]}`.
- `internal/state`: window taxonomy grows agent-vs-shell; new invariants
  (`workspace_root_missing`, `dangling_worktree_link`, `orphan_pr`).
- **Delete** the `worktree | multi-repo` kind split (`@ai_workspace_kind`,
  `workspace.Listable`). Per issue #83, multi-repo is the only mode; a
  one-repo workspace is just a workspace whose agent needed one worktree.

**Cost.** High. Roughly 25 e2e files under `internal/workspace` and ~8.5k
lines of tests under `internal/tools/workspaces` assert the current model.
Budget the test rewrite at roughly the size of the feature work — CLAUDE.md's
testing rule makes it non-optional.

**Blocks.** WS-2, WS-3, WS-4, WS-7, WS-8.

---

### WS-2 — Workspace directory + worktree symlink layout

**Change.** Every workspace gets `~/ateliers/<slug>/` (configurable). Git
worktrees stay where they are today (`~/code/.worktrees/github/<owner>/<repo>/<branch>`)
— repo-local, git bookkeeping unchanged — and are **symlinked** into the
workspace root as `<repo>/<branch>`, exactly as the drawing's `ls` shows.

**Work.**
- Create/repair/GC the link tree; a worktree removed underneath leaves a
  dangling link → new invariant + `reconcile --fix` sweeps it.
- Agent popups `cd` to the workspace root, not to a worktree.
- **Path-canonicalization audit.** Every consumer that derives meaning from
  a path must agree on real-vs-link: `forgeWorkspaceCwd`,
  `splitWorktreePath`, `worktreePathForBranch`, `recoverPickerRows`,
  `SpawnBgPull`, and — most importantly — `claudeproj.LatestTranscriptPath`.

> ⚠ **Sharpest hidden landmine.** Claude derives its project directory from
> the encoded cwd. A symlinked cwd hashes to a *different* project dir than
> the real path, so recap, attention, and `--resume` all silently target the
> wrong (or an empty) transcript. Decide a canonical-path policy — probably
> "resolve symlinks before anything reaches an adapter" — and pin it with a
> test before any of WS-3/4 is built on top.

**Cost.** Medium. **Risk.** High, and mostly invisible until it bites.

---

### WS-3 — M-n: intent-first creation

**Change.** The repo picker stops being the entry point. M-n opens the
prompt directly; atelier derives everything else.

**Work.**
- `PrimaryInvoke` moves from `pick` to the prompt flow; repo picker demoted
  to a fallback bind or dropped.
- AI naming call returns **three** values instead of two: a human title
  ("Helm Chart Testing"), a slug (`helm-chart-testing`), and the optional
  grouping tag. `parseNameAndTag` + `autoSessionNameRe` extend.
- Create root dir → create session → stamp intent/title/root → open the
  driver agent with the intent as first prompt. Mostly a generalization of
  `runAutoSession`.
- **Launcher default screen.** With no resumable last-active workspace,
  `atelier` lands on M-n instead of a bare shell — `resolveLaunchSession` /
  `runBundled` in `internal/cli/run.go`.

**Open problem — "Atelier does the rest."** A workspace created from a bare
intent has *zero* worktrees. Something has to pick the repos:

- **(a) AI-selected from a repo index** — scan the code root, hand the
  index to the naming call, let it name the repos the intent touches, and
  create those worktrees eagerly. Self-contained; pulls issue #61's repo
  index forward. **Recommended for v1.**
- **(b) The agent creates them** via the control surface in WS-5. Truer to
  the drawing, but makes M-n depend on the largest unbuilt piece.

Pick (a) to ship, (b) to grow into. They compose — (b) is additive.

**Cost.** Medium.

---

### WS-4 — M-s: aggregate workspace picker

**Change.** One row per workspace. Columns become **rollups**, and repo /
branch leave the row entirely.

```
17h  >  3PR  Helm Chart Testing
     ○ PRs completed, work pending your action
3d   2o 3PR  New CICD Flow for Wawa Clinic
     ○ PRs completed, work pending your action. All good mate.
```

**Work.**
- Rollups: attention count = agents blocked in this workspace; PR count =
  registered PRs (WS-6). `BuildSessionList` currently walks
  `list-windows -a` and emits one row per window — becomes a group-by.
- Search moves to title (`--nth=1` on the title field).
- Footer: `M-x delete`, `M-r rename`, `M-? help`. **`M-r` currently means
  workspace history** — that binding has to move (see §5).
- **Delete gets much heavier.** Destroying a workspace now means: kill the
  session, remove N worktrees, tear down the link tree, and decide what
  happens to open PRs. Needs a confirm screen that *enumerates* what will
  be destroyed, not today's one-line prompt.
- Absorbs issue #69 (mute repo/branch so tag color leads) — those columns
  no longer exist.

**Cost.** Medium.

---

### WS-5 — Agent control surface: making Claude active

**Change.** The agent gains the ability to act on the workspace. This is
issue #83's "Claude is passive" and the precondition for the drawing's
"agent makes PRs, those are registered by atelier."

**Work.**
- **Kernel first, protocol second.** Expose the capabilities as CLI verbs
  the kernel owns — `atelier workspace worktree add <repo> <branch>`,
  `atelier workspace worktree list`, `atelier workspace context`,
  `atelier pr register <url>`, `atelier workspace agent spawn`. These are
  the contract.
- Then `atelier mcp serve` (stdio MCP server) as a **thin wrapper** over
  those verbs, registered into the interactive agent's generated settings.
  MCP is Claude's transport; a Codex adapter exposes the same verbs its own
  way. This keeps the `AIIntegration` port honest — the capability is
  kernel, the plumbing is adapter.
- Note the launch paths differ: background `claudegen` calls deliberately
  run `--tools ""` and must **not** gain MCP; only the interactive popup
  agent does.

**Cost.** High. **Deferrable** — WS-6 can discover PRs by polling instead
of being told (see below), so this need not gate v1.

---

### WS-6 — PR model + M-c "List Changes"

**Change.** The forge port grows from a single enum to a real PR record,
and gets its own aggregate view with actions.

**Work.**
- `integration.ForgeStatus{State}` → `PullRequest{Number, Repo, Title,
  State, Draft, CI, ReviewDecision, Comments, URL, Branch, UpdatedAt}`.
  Kernel owns the glyphs for CI and approval — extend the existing
  `forgeGlyphs` table rather than letting adapters render.
- **Batch the queries.** Today it is one `gh pr view` per window per minute
  (`forgeLoopRefreshTTL`). At workspace × repos × PRs that will hit
  GitHub's secondary rate limits. Move to one `gh pr list --json …` (or a
  single GraphQL query) **per repo**, with conditional requests. Do this
  before M-c ships, not after it breaks.
- Storage outgrows a per-window tmux option — PRs are a workspace-level set
  with more fields than an option value should carry. Give them a
  statestore section keyed by workspace.
- New `M-c` picker with the drawing's two-line rows.
- **Actions.** `M-o` open (exists). `M-c` close is the **first mutating
  forge operation in the port** — it needs a confirm step and a deliberate
  decision that atelier writes to the forge at all. Obvious near-neighbours
  worth scoping now: jump into the PR's worktree, view failing CI.
- **Binding collision:** `M-c` is bound to *Switch K9s context*
  (`internal/tools/k8s/register.go:29`). One of them moves.

**Cost.** High. **Independent of WS-1** — the richer forge model can land
under today's badge before the workspace model changes. Good parallel track.

---

### WS-7 — Daemon: three feeds

**Change.** The refresh loop grows the drawing's three arrows.

**Work.**
1. **PR sweep** — per-repo batched, TTL-throttled (WS-6).
2. **Workspace summaries** — today's recap is per-window from *one* agent
   transcript. The workspace summary must roll up N agent recaps + PR state
   into "PRs completed, work pending your action". That is a **second AI
   call shape**, and it only needs to re-run when an input changed.
3. **Context into the agent** — the vaguest arrow. Concretely: make
   `atelier workspace context` (WS-5) emit worktrees + PR states + sibling
   agent recaps, and feed it via the agent's session-start hook or as an
   MCP resource.

**Watch the spend.** Background token cost is already metered (`atelier ai
usage`). Workspace summaries add a recurring second call per workspace —
gate it on change-detection and give it a budget guard.

**Cost.** Medium.

---

### WS-8 — Multiple agents per workspace (issue #18)

**Change.** The drawing's Claude 1 / Claude 2 / Codex.

**Work.**
- The `ai.*` metadata namespace encodes one agent per window. Note that
  *Claude 1* and *Claude 2* are two **instances of the same adapter**, so
  namespacing by adapter name (`claude.active_session_id`) is not enough —
  agents need instance ids.
- Composition root: `Active().AI` (one) → a registered set with per-window
  resolution.
- Agent list/switch UI within a workspace.

**Cost.** High. **Recommend deferring past v1.0** — M-n, M-s and M-c are
all coherent with a single driver agent, and this is the piece with the
gnarliest state-isolation testing burden (see #18's own checklist).

---

### WS-9 — Migration, compatibility, release mechanics

**Work.**
- **statestore v3 with a real migration.** v2's own comment admits the
  plumbing was never written. Do not wipe — users have live workspaces.
  Map each v2 repo-session into a single-agent workspace whose title is
  derived from the repo/branch, and whose root is materialized on first use.
- Config additions: `[workspaces] workspace_root`, `[forge]` poll cadence,
  `[ai]` summary model/prompt. Renames are `feat!:` → release-please major
  bump (see RELEASING.md).
- `atelier doctor`, `state show`, `reconcile` all learn the new entities
  and invariants.
- Docs: DESIGN.md (concepts, repo layout, state model), README, CLAUDE.md
  (new rule: *tools never write PR state directly* — it belongs to the
  kernel's forge slot), EMBEDDING.md (bindings changed).
- Keybinding table changes mean `atelier init` output changes: **embedded
  (`--bare`) users must re-source**. Call it out in the release notes.

**Cost.** Medium, and easy to under-budget.

---

## 4. Cross-cutting risks

| # | Risk | Mitigation |
|---|---|---|
| 1 | **Symlinked cwd breaks Claude's project-dir derivation** — recap, attention, and `--resume` silently target the wrong transcript | Canonical-path policy + a test, landed in WS-2 *before* anything is built on it |
| 2 | **`gh` call volume** — per-window-per-minute polling won't survive workspace × repos × PRs | Batch per repo (one `gh pr list`/GraphQL call) as the first commit of WS-6 |
| 3 | **Test surface** — ~8.5k lines of picker tests + 25 workspace e2e files encode the old model | Budget test rewrite ≈ feature work; sequence WS-1 as its own phase so the churn is contained |
| 4 | **fzf ceiling** — M-c wants columns, per-row state, actions, confirmations. That is issue #86 (bubbletea) arriving on schedule | Decide before Phase 2: pilot bubbletea on M-c, or push fzf one more surface. M-s already proves fzf can do two-line rows + `--listen` live reload — M-c's *actions* are where it starts costing more than it saves |
| 5 | **Scope** — the drawing is ~4 major features; shipping them as one release means a long red branch | Phase gates below; each phase is independently shippable |
| 6 | **Recap-cost growth** — a second per-workspace AI call on a heartbeat | Change-detection gate + budget guard in WS-7 |

---

## 5. Binding changes

| Key | Today | After |
|---|---|---|
| `M-n` | New workspace (repo picker) | New workspace (**intent prompt**) |
| `M-s` | Active workspaces (per worktree) | Active workspaces (**per workspace**) |
| `M-c` | **Switch K9s context** ⚠ | **List Changes** — k9s must move (suggest `M-k`) |
| `M-r` | Workspace history | **Rename workspace** (in M-s) ⚠ — history needs a new home |
| `M-o` | Open forge item | Open PR (unchanged) |
| `M-x` | Delete workspace | Delete workspace (**confirm now enumerates worktrees + PRs**) |

Two collisions, both forced by the drawing's own footers. Worth resolving
in Phase 0 so no one learns a binding twice.

---

## 6. Sequencing

Five phases. Each ends somewhere shippable.

### Phase 0 — Foundations *(no user-visible change)*
- statestore v3 + migration (WS-9)
- Workspace entity + state taxonomy/invariants (WS-1, model only)
- Rich `PullRequest` behind the existing badge — adapter returns everything,
  picker still renders one glyph (WS-6, model only)
- Batched `gh` queries (WS-6)
- Path-canonicalization policy + tests (WS-2)
- Resolve the two binding collisions (§5)

*Ships as a patch release. Nothing looks different; everything underneath moved.*

### Phase 1 — The new spine → **v1.0-alpha**
- Workspace root + symlink layout (WS-2)
- M-n intent-first + launcher default screen (WS-3)
- AI-selected repos from a repo index — option (a) (WS-3)
- M-s workspace rollup rows (WS-4)
- Delete/rename semantics (WS-4)

*The model change is now visible and the drawing's left half is real.*

### Phase 2 — Changes view → **v1.0**
- M-c picker with two-line PR rows (WS-6)
- PR sweep in the daemon (WS-7)
- Open / close actions (WS-6)
- Workspace-level AI summary (WS-7)

*Cut v1.0 here. All four drawn surfaces exist.*

### Phase 3 — The active agent → **v1.1**
- Kernel CLI verbs for worktree/PR/context (WS-5)
- `atelier mcp serve` + interactive-agent wiring (WS-5)
- Context feed into the agent (WS-7)

### Phase 4 — Multi-agent → **v1.2**
- Per-agent metadata namespacing, adapter set, switching UI (WS-8, issue #18)

**Alternative cut:** hold v1.0 until Phase 3 if "Atelier does the rest"
should mean *the agent* creates the worktrees rather than a creation-time
heuristic. That is the main strategic call in this plan — see §7.

---

## 7. Decisions needed before Phase 1

1. **Window = agent, or window = worktree + a driver window?** This plan
   assumes **window = agent** (worktrees are filesystem artifacts). It is
   the cleanest read of the drawing and of issue #83, and it is what makes
   M-s a workspace rollup — but it is a one-way door.
2. **Who creates the first worktrees** — creation-time AI repo-selection
   (ship Phase 1 standalone) or the agent via MCP (Phase 3 gates v1.0)?
3. **Workspace root location** and whether symlinks or real in-place
   worktrees. Plan assumes `~/ateliers/<slug>/` + symlinks, per the drawing.
4. **M-c in fzf or bubbletea?** The actions are the deciding factor.
5. **Where does worktree history (`M-r`) go** once M-r means rename?
6. **Does atelier write to the forge at all?** Close-PR is the first
   mutating action. Confirm-gated, or out of scope for v1?

---

## 8. Issue mapping

| Existing issue | Relationship |
|---|---|
| #83 — no task/intent layer; Claude is passive | **The written form of this drawing's left half.** WS-1, WS-3, WS-5 |
| #18 — multi-agent primary/switching (M-a) | WS-8 |
| #61 — manager harness / repo index | Its repo index is the input to WS-3 option (a) |
| #86 — adopt bubbletea | Risk 4; decide at Phase 2 |
| #69 — mute repo/branch in M-s | **Absorbed** — those columns disappear in WS-4 |
| #39 — close-popup + window navigation | Adjacent; window navigation semantics change under WS-1 |
| #53 / #35 / #36 — help hub, shortcuts on load | Phase 1 changes what they document; sequence after |
