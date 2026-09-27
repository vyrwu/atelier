# atelier — Design

Why atelier is shaped the way it is, and the rules that keep it that way. What
it does, key by key, is in the [README](README.md); this is the reasoning
behind it. Read it before changing anything.

---

## 1. What atelier is

atelier is a **workshop for parallel Claude Code agents**: a terminal-based
agentic development environment. What an IDE is to writing code, atelier is to
directing the agents that write it.

**Goal:** reviewing a pull request should be as easy as making a manual code
change or running a command in a repository. atelier is terminal-based,
keyboard-centric, and fully multiplexed through tmux.

You describe a task in plain language. atelier gives it a workspace — a
directory, a name, and an agent — and gets out of the way. The agent does the
work: finds the repositories, creates the worktrees, writes the code, opens the
pull requests. atelier's job is to let you run several of these at once and
always know which one wants you.

Four things, and nothing else:

1. **Start work** — one prompt, one keystroke, an agent running in a directory,
   started in the background so kicking off work never breaks your focus.
2. **Know where to look** — an ambient signal telling you which session is
   blocked, without leaving what you are doing.
3. **Get there fast** — switch between sessions, worktrees, and pull requests
   with no perceptible delay; retire finished work and restore it later.
4. **Review in place** — a pull request's diff and CI are one key away, in the
   same terminal the work happened in.

### Non-goals

atelier is **not**:

- a tmux distribution, configuration framework, or theme
- a tool launcher, plugin host, or registry
- a multi-agent orchestrator
- a git porcelain, or a replacement for GitHub's review and CI tooling — it
  opens a PR's diff and checks in tools built for them (a diff pager,
  gh-enhance) rather than drawing its own
- a general-purpose terminal multiplexer

It supports **one agent** (Claude Code) and **one forge** (GitHub). This is a
design decision, not a limitation to be worked around. Any abstraction that
exists to support a second implementation is out of scope until a second
implementation actually exists.

---

## 2. Domain model

Four entities. Everything in the system is one of these.

| Entity | Definition | Identity | Lifetime |
|---|---|---|---|
| **Workspace** | One task. A directory, a title, a tmux session, and an agent. Either **active** or **retired**. | Immutable slug (directory name) | Until deleted |
| **Worktree** | A git worktree belonging to a workspace, living inside its directory. | `<repo>/<branch>` path under the workspace root | Until removed |
| **Agent session** | A Claude Code process in the workspace session's `claude` window. | tmux session + window name | Until the process exits |
| **Pull request** | A GitHub PR whose head branch matches one of the workspace's worktrees, or that the agent registered. | `owner/repo#number` | Owned by GitHub |

One workspace has one agent session, N worktrees, and N pull requests. A
worktree belongs to exactly one workspace. A pull request is associated with a
workspace by branch, not by ownership.

**Retire is soft, delete is hard.** Retiring kills a workspace's processes but
keeps its directory, worktrees, and Claude conversation, so restoring resumes
the agent. Deleting removes the worktrees through git (so no source repository
keeps stale metadata), then the session, directory, and record, behind a confirm
that names dirty worktrees. Retire/restore is the one lifecycle state that
shipped; it is not an undo of delete.

**The slug is immutable.** Git worktree metadata stores absolute paths, so
renaming the directory after a worktree exists would break every repository the
workspace touched. The slug is an adjective-noun handle (`spry-otter`) assigned
the instant the workspace is created; Claude's title arrives later, lives in
state, and may change freely.

**The directory is the record of its worktrees.** A `.git` *file* (not
directory) marks one. Nothing else tracks them.

```
~/ateliers/<slug>/          workspace root — the agent's cwd
  CLAUDE.md                 symlink to the central guide
  <repo>/<branch>/          a git worktree, created by the agent
    .git                    file — marks a worktree
```

**Windows are named, not indexed.** A workspace session's windows are `claude`,
`shell`, one per worktree, and a PR's diff and checks. All navigation is keyed
on those names, so atelier's tmux server runs with `automatic-rename` and
`allow-rename` off — a silent rename would drift the identity navigation depends
on.

---

## 3. Principles

Numbered so code comments can cite them.

### Performance

**NFR-P1 — Nothing on the hot path touches the network.** Opening any view
renders from local state. GitHub calls happen asynchronously and update the view
when they land.

**NFR-P2 — Workspace creation never makes you wait.** Enter closes the popup
and hands the work to a detached builder. It reserves the handle, lays out the
directory, and launches the agent before asking Claude for a title, so the one
slow call is off everyone's path.

**NFR-P3 — Views render instantly from local state.** The overlay runs fresh
in the popup each time — small and local, so it draws at once — rather than as a
warm attached process. The one long-lived UI process is the home splash.

**NFR-P4 — In-overlay transitions are internal state.** Moving between the
overlay's screens is a state change in one process: no spawn, no clear-screen,
no visible reconstruction.

**NFR-P5 — Zero cost when idle.** With no interaction and no agent activity,
atelier uses no CPU: no timer, no tick, no polling loop. An open pull request
view is the one exception (§5), and it stops when the view closes.

### Reliability

**NFR-R1 — No daemon.** No component's correctness depends on a background
process staying alive. State changes are pushed by events, not discovered by
sweeps.

**NFR-R2 — Ground truth over bookkeeping.** Where reality can be read cheaply,
it is read rather than tracked: worktrees from the filesystem, pull requests from
GitHub. Stored state is a cache and an index, never the only record of something
that exists elsewhere.

**NFR-R3 — State survives everything.** Workspaces, titles, and cached PR data
survive a tmux server restart, a machine restart, and a crash. Nothing atelier
needs is stored in tmux.

**NFR-R4 — Concurrent writes are safe.** The UI, the workspace builder, and the
MCP server all write state. Writes are serialised under a file lock and replaced
atomically; a crash mid-write leaves the previous state intact.

**NFR-R5 — Stale status is detected.** An agent whose process died shows as
**gone**, not its last status. A **working** status with no fresh signal for 20
minutes (a turn that died without its stop event) reads as **idle** until the
next tool event.

**NFR-R6 — Agent cooperation is never load-bearing.** Nothing atelier shows
depends on the agent choosing to call an atelier interface, except
`register_pr`, which is explicitly a gap-filler (§4.2).

**NFR-R7 — tmux is a fallible dependency, handled as one.** All tmux use goes
through one seam with a short timeout, so a wedged server is an error, not a
hang (attach is the one legitimate block, via `syscall.Exec`). Window and
worktree names are sanitised because tmux reads `:` and `.` as target syntax.
There is deliberately **no** reconcile/repair kernel: the overlay captures the
invoking client when it opens and uses it at once, so the stale-client and
orphan-popup failures that kernel existed to fix do not arise.

### Simplicity

**NFR-S1 — Line budget: 5,000 lines of `internal/`.** Adding beyond it
requires removing first.

**NFR-S2 — One binary.** A single `atelier` executable serves the UI, the CLI,
and the MCP server.

**NFR-S3 — One state file, one config file.** Both plain text, both
hand-editable, both inspectable without tooling.

**NFR-S4 — atelier owns its own server's config, never yours.** It runs a
**dedicated** tmux server and generates and sources its bindings into that server
alone, regenerating them from the binary so an upgrade can't leave dead keys. It
never generates, rewrites, or takes authority over the user's tmux
configuration.

**NFR-S5 — One renderer.** All user interface is Bubble Tea. No second UI
technology, no external picker binary, no shelling out to draw.

### Operability

**NFR-O1 — Failures explain themselves in one line.** When atelier cannot start
or complete an action, it says why and what to do. Beyond the splash's
dependency check, there is no diagnostic subsystem.

**NFR-O2 — Platforms:** macOS and Linux. Requires tmux 3.3+, git, the GitHub
CLI, and Claude Code on `PATH`.

---

## 4. Architecture

### 4.1 One binary, four surfaces

The user sees things in four places. Conflating them is the primary
architectural risk.

| Surface | Hosts | Lifetime | Why |
|---|---|---|---|
| **Status line** | Attention badge, current workspace title and agent status, create spinner | Visible everywhere | When you need to know which session wants you, you are looking at an agent pane, not a picker. The signal must be ambient, and a background create has to report back somewhere. |
| **Home splash** | Keymap, version, dependency check | Persistent | Attaching lands on a real screen, not a bare shell, and `M-h` returns here from anywhere. |
| **Overlay (popup)** | spaces · trash · new · pull requests · worktrees · confirm | Seconds | Peek and jump: an overlay is one transition, a window would be two. It runs fresh each open and captures the invoking client at once, so it never acts on a stale one. |
| **Window** | The agent, shells, a PR's diff and checks | Hours | Survives detach, keeps scrollback and copy mode, and shows in tmux's own session list. |

**Agent sessions are windows, never overlays.** An overlay is bound to a client
and does not survive detaching; a session you live in for hours must.

**Programs in windows aren't atelier's UI.** The agent, a shell, a diff pager,
and gh-enhance run in windows atelier opens; atelier draws none of them. That is
how review stays in the terminal without atelier becoming a review tool.

### 4.2 Capture design

The central problem: the agent creates worktrees and pull requests
autonomously. atelier must reflect them without being able to control what the
agent does.

Five mechanisms exist. They are not equally reliable:

| Mechanism | Reliability | Depends on |
|---|---|---|
| **Derive** — read ground truth on demand | Cannot fail; nothing is being captured | The data being derivable |
| **Constrain** — make the wrong path unnatural | N/A — makes derivation valid | Nothing |
| **Intercept** — an event hook fires | Deterministic where the event exists | Hook installation |
| **Register** — the agent calls an interface | Only as reliable as agent compliance | The agent choosing to |
| **Instruct** — the prompt asks | Probabilistic | The agent choosing to |

**Governing principle: agent compliance is never load-bearing** (NFR-R6).
Register and Instruct are conveniences layered on mechanisms that work without
them.

| Subject | Primary | Made valid by | Gap-filler |
|---|---|---|---|
| **Worktrees** | **Derive** — walk the workspace directory | **Constrain** — the agent's cwd is the workspace root, and `create_worktree` places worktrees where derivation sees them, off the freshly fetched default branch | — |
| **Pull requests** | **Derive** — one GraphQL query for the whole workspace: each worktree branch, and each registered PR by number | **Constrain** — `create_pr` rebases onto the latest default branch and opens a draft from a worktree branch | **Register** — `register_pr`, for PRs with no local worktree |
| **Agent status** | **Intercept** — prompt-submit and tool-use (working), session-start and stop (idle), and the notifications that need you (blocked) | Environment variables identify the workspace | **Derive** — liveness marks dead sessions **gone**; a stale **working** decays to **idle** (NFR-R5) |

Agent status is the one case where derivation genuinely fails: a running agent
looks the same whether it is thinking or waiting for you. That state is semantic
and must be pushed out by the agent's own lifecycle events.

**Hooks live in the user's global Claude settings**, guarded so they do nothing
without the workspace environment. That is more reliable than injecting
settings at launch — it also captures an agent started by hand inside a
workspace — and atelier doesn't have to own the launch command. Every write to
Claude's config honours `$CLAUDE_CONFIG_DIR`, or it lands in a file Claude never
reads.

### 4.3 Interfaces

| Interface | Purpose | Consumers |
|---|---|---|
| **UI** | Everything the user sees and does | The user |
| **CLI** | `hook`, `open`, `create`, `win`, `home`, `mcp`, `install`, `version` | Agent hooks, tmux bindings, Claude (MCP) |
| **MCP** | `create_worktree`, `create_pr`, `register_pr` | The agent |
| **tmux fragment** | Bindings and options, generated and sourced into atelier's own server | atelier itself |

The CLI exists so hooks and bindings have something fast to call. The status
line calls nothing; it renders options the hooks push. Users run only
`atelier`, `atelier install`, and `atelier version`.

---

## 5. Rules

Tripwires, not sentiments. Every one exists because its absence has a
predictable failure mode. They are enforced in review.

- **One agent, one forge, one renderer** — Claude Code, GitHub, Bubble Tea. A
  second implementation means deleting the first.
- **Extract an interface only when a second real implementation exists and is in
  production.** Never for a mock, never for a hypothetical.
- **All UI is Bubble Tea**, in `internal/ui`. No second UI technology, no
  shelling out to draw.
- **No process spawn per screen.** The overlay's screens are states of one
  process (NFR-P4).
- **State lives in one JSON file, never in tmux**, and it is a cache and an
  index. Don't store what can be derived (NFR-R2).
- **Nothing polls, and there is no daemon.** If something needs to know when a
  thing changed, an event tells it. The one exception: GitHub can't push to
  atelier, so an open pull request view re-queries once a minute. The popup is
  the process, so closing it ends the poll.
- **No plugin system, ever.**
- **Delete dead code** rather than keeping it around.
- **A feature ships only after it has been wanted three separate times.** Open
  an issue first for anything that adds surface.
- **5,000 lines of `internal/`.** Adding past it means removing first (NFR-S1).

---

## 6. Out of scope

Deferred deliberately. Each needs the three-times rule to return.

- Merging, checking out, commenting on, or approving pull requests (atelier
  shows a PR's diff and checks, and reopens, closes, or marks it ready — nothing
  more)
- CI failure triage (atelier opens a PR's logs in gh-enhance; it does not read
  them)
- Workspace rename, tagging, or grouping
- Any summary or recap of what an agent did
- Repository indexing, caching, or search
- Multi-agent or multi-forge support
- Recovery of **deleted** workspaces (retire/restore covers deactivated ones)

What is wanted next is in the README's [Roadmap](README.md#roadmap).

---

## 7. Settled decisions

1. **Handle first, title second.** Naming once came first and derived the slug
   from Claude's title, which put a multi-second model call ahead of the agent
   starting. Now the builder reserves an instant adjective-noun handle, starts
   the agent, and names it mid-work; `⌥Enter` can switch you in before the title
   exists.
2. **Title model.** Sonnet, configurable via `naming_model`. Haiku followed the
   "reply with only…" instruction too loosely — its preamble and quotes failed
   extraction and fell back to the raw prompt. The call runs with
   `--no-session-persistence`, or it would become the conversation
   `claude --continue` resumes.
3. **Entering a blocked workspace acknowledges it.** Its status becomes idle and
   the badge clears; the agent re-raises it with its next notification if it
   still needs you. Only notifications where Claude is stopped on you count —
   the idle reminder it sends after every turn would otherwise badge every agent.
4. **Diffs are local.** A PR's diff is `git diff <base>...HEAD` in the worktree
   on its head branch, after fetching the base: instant, offline, no size limit
   (GitHub's diff endpoint refuses past 20,000 lines). A stacked PR's base is the
   branch below it, so it shows only its own slice. With no worktree it falls
   back to `gh pr diff`. The pager is chosen by the window's own shell, since
   its `PATH` can differ from atelier's.
5. **Network git uses gh's token over HTTPS**, rewriting SSH remotes for that
   one command, so a background fetch or push never pops an SSH passphrase or
   keychain prompt, and never asks for credentials on the agent's terminal.
6. **Shells need no cleanup.** `shell` and per-worktree shells are named windows
   in the workspace session and die with it, on retire or delete.
7. **First run.** Attaching lands on the splash, never a bare shell. A fresh
   install with no config works on defaults, and with no workspaces yet any
   overlay opens straight on New.
8. **Uncommitted changes on delete:** warn and allow. The confirm names dirty
   worktrees; it does not refuse.
9. **Distribution.** A Homebrew cask that strips the quarantine attribute on
   install, so the un-notarized binary runs without a Gatekeeper prompt.
