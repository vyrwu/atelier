# REDESIGN.md — atelier v1: the workspace becomes the intent

> **STATUS: IMPLEMENTED.** All of Phases 0–3 landed in the v1 redesign
> (WS-1 through WS-7 + WS-9; WS-8 multi-agent is out of scope as planned).
> The concepts now live in [`DESIGN.md`](DESIGN.md); the calls made where this
> plan left choices open are logged in [`DECISIONS.md`](DECISIONS.md). This
> document is retained as the historical plan/rationale — read DESIGN.md for
> what atelier *is*, this for *why it changed*.

Plan doc for the major redesign sketched in the design canvas. Breaks the
drawing into implementable workstreams, grounds each in the code that
exists today, and sequences them into shippable phases.

Companion to [`DESIGN.md`](DESIGN.md) (what atelier *is* today) — this doc
is what changes and in what order.

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

### Scope: one agent per workspace

The canvas's first panel draws three agents per workspace (Claude 1,
Claude 2, Codex). **That layer is out of scope for v1.** A workspace has
exactly one driver agent; everything else in the drawing is built on that
assumption and works fine under it.

Two consequences, both good:

- The `ai.*` metadata namespace — which issue #18 identified as the
  blocker, because it encodes one agent per window — is now exactly the
  right shape. It stays untouched.
- The "window = agent or window = worktree?" question collapses: a
  workspace is **one driver window** plus whatever inspection shells the
  user opens. See WS-8 for what stays forward-compatible so this is a door
  rather than a wall.

### The single load-bearing change

> **Today the unit of work is a branch. After this, the unit of work is an intent.**

`session = repo, window = worktree/branch` becomes
`session = workspace/intent, window = driver agent`, with worktrees demoted
from first-class UI objects to **filesystem artifacts** the agent produces.

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
| Agent agency | **None.** Claude is launched into a popup and reads/writes files. It cannot create worktrees or tell atelier about a PR it opened. No MCP server exists | — |
| Invariants | `internal/state` — topology, 9 violation codes, `reconcile --fix` | `state/validate.go` |

One thing worth flagging up front: the statestore's own doc says *"Schema
v2 wipes v1 cache. Migration plumbing added when v2 ships"* — that plumbing
was never written, so v3 has to build it rather than wipe live workspaces.

The other historical worry, that `ai.*` **encodes one agent per window**,
is retired by the single-agent scope. It is no longer a cost.

---

## 3. Workstreams

Nine, of which eight are built. WS-1 gates most of them; WS-6 is the one
substantial parallel track; WS-8 is a not-doing, recorded so the other eight
don't foreclose it.

### WS-1 — Workspace entity: the intent container ⟵ *keystone*

**Change.** Promote the workspace from "a tmux session that happens to be a
repo" to a first-class entity with its own identity, title, intent text,
root directory, and owned sets (agent, worktrees, PRs). A window becomes the
**driver agent**, not a worktree.

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
  `Agents` is a **list holding exactly one entry**, enforced by an invariant
  rather than by the type. Single-agent is the v1 scope, but a one-to-one
  field would force a schema v4 the day that changes; the list costs nothing
  now and keeps the door open.
- `internal/state`: window taxonomy grows driver-vs-shell; new invariants
  (`workspace_root_missing`, `dangling_worktree_link`, `orphan_pr`,
  `multiple_drivers`).
- **Delete** the `worktree | multi-repo` kind split (`@ai_workspace_kind`,
  `workspace.Listable`). Per issue #83, multi-repo is the only mode; a
  one-repo workspace is just a workspace whose agent needed one worktree.

**Cost.** High. Roughly 25 e2e files under `internal/workspace` and ~8.5k
lines of tests under `internal/tools/workspaces` assert the current model.
Budget the test rewrite at roughly the size of the feature work — CLAUDE.md's
testing rule makes it non-optional.

**Blocks.** WS-2, WS-3, WS-4, WS-7.

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
- Rollups: PR count = registered PRs (WS-6). `BuildSessionList` currently
  walks `list-windows -a` and emits one row per window — becomes a group-by.
- **`<N-ATTENTION>` needs a new definition.** With one agent per workspace
  its attention is a boolean, so a *count* has to mean something else: the
  number of things in this workspace wanting your eyes — the blocked agent
  (0 or 1) plus PRs with changes-requested or failing CI. That keeps the
  column earning its width, and it is a better signal than an agent tally
  would have been. Depends on WS-6 for the PR half.
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
  `atelier pr register <url>`. These are the contract. **No `agent spawn`** —
  sibling agents are out of scope (WS-8).
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
   `atelier workspace context` (WS-5) emit the workspace's worktrees and PR
   states, fed via the agent's session-start hook or as an MCP resource.
   (With one agent there are no sibling recaps to fold in — the feed is
   worktrees + forge state only.)

**Watch the spend.** Background token cost is already metered (`atelier ai
usage`). Workspace summaries add a recurring second call per workspace —
gate it on change-detection and give it a budget guard.

**Cost.** Medium.

---

### WS-8 — Multiple agents per workspace ~~(issue #18)~~ — **OUT OF SCOPE**

**Decision: not built.** One agent per workspace, indefinitely. The drawing's
Claude 1 / Claude 2 / Codex layer is deferred with no scheduled phase.

Kept here as a workstream entry for one reason only: **what the other eight
must not foreclose.** Three cheap constraints, all of which cost nothing to
honour now and are expensive to retrofit:

- `Workspace.Agents` is a **list** in schema v3, held at length one by
  invariant (WS-1). A one-to-one field means a schema v4 later.
- Anything that reads "the agent" for a workspace goes through **one
  resolver** rather than assuming the workspace's single window. A grep for
  callers is the migration later; scattered assumptions are a rewrite.
- Per-workspace state that is really per-*agent* (`ai.active_session_id`,
  agent status, recap) stays addressed by **window**, not by workspace. It
  already is — just don't collapse it while building WS-4's rollups.

Nothing else. No instance ids, no adapter set, no switching UI, no
per-adapter namespacing. `Active().AI` stays a single adapter.

Note for whenever this thaws: *Claude 1* and *Claude 2* are two instances of
the **same** adapter, so namespacing by adapter name would not have been
enough — agents would have needed instance ids. That, plus the state-isolation
checklist in #18, is the cost that is being avoided.

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

Four phases. Each ends somewhere shippable.

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

*(No Phase 4. Multi-agent is out of scope — see WS-8.)*

**Alternative cut:** hold v1.0 until Phase 3 if "Atelier does the rest"
should mean *the agent* creates the worktrees rather than a creation-time
heuristic. That is the main strategic call in this plan — see §7.

---

## 7. Decisions needed before Phase 1

1. ~~**Window = agent, or window = worktree + a driver window?**~~
   **Resolved** by the single-agent scope: a workspace is one driver window
   whose cwd is the workspace root, plus whatever inspection shells the user
   opens. Worktrees are filesystem artifacts, never windows.
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
| #83 — no task/intent layer; Claude is passive | **The written form of this drawing's left half.** WS-1, WS-3, WS-5 — minus its "spawn sibling agents" clause |
| #18 — multi-agent primary/switching (M-a) | **Out of scope.** WS-8 records only the forward-compat constraints |
| #61 — manager harness / repo index | Its repo index is the input to WS-3 option (a) |
| #86 — adopt bubbletea | Risk 4; decide at Phase 2 |
| #69 — mute repo/branch in M-s | **Absorbed** — those columns disappear in WS-4 |
| #39 — close-popup + window navigation | Adjacent; window navigation semantics change under WS-1 |
| #53 / #35 / #36 — help hub, shortcuts on load | Phase 1 changes what they document; sequence after |
