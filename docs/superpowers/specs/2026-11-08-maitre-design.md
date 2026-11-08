# Maître — design

Status: implemented · 2026-11-08 · sub-project 1 of 2 (2: demo sandbox +
README pitch, specified separately once this ships)

## Intent

Atelier is driven by one human. Maître is an AI that drives it the same way:
it creates, renames, retires, restores, deletes, and swaps spaces, reads what a
space's agent shows, and types to it — answering its questions and permission
prompts — like the operator would. Comparable to Amp's Puck.

It is a **single meta-session per atelier instance**, above every space and
belonging to none, reached as a **dismissable popup** (`M-m`). The popup is a
viewport, not a workplace: closing it hides Maître, which keeps working; it is
not meant to be a long-running chat you live in.

Success: from the popup you can say "start spaces for these four tickets",
"what's blocked and why?", "answer the clinic-api agent: use the existing
fixture", "retire everything merged", or "take me to the one with red CI", and
it happens through atelier's own verbs — with nothing destroyed without your
yes.

## Decisions

| | Decision |
|---|---|
| Name | **Maître** (maître d'atelier). CLI `atelier maitre`; popup title `Maître`. |
| Cardinality | One Maître per instance (socket + state). A meta-session across all spaces. |
| Form | `M-m` toggles a popup attached to Maître's own tmux session. |
| Dismiss | Hide. Maître keeps running; `M-m` always resumes the same conversation. |
| Restart | `M-r` inside Maître starts a fresh conversation (same agent). |
| Agent | `maitre_agent` config key, default = `agent`. `M-x` inside Maître swaps it like any space. |
| Autonomy | Everything pre-allowed except destroying: `delete` and `purge` always need the operator's yes, asked by atelier itself (agent-agnostic). |
| Talking to agents | Screen-level, like a human: read the agent pane, type into it. Works the same for Claude Code, Codex, Grok Build. |
| Orchestration | None inside atelier. Atelier exposes operator verbs; Maître is an ordinary agent holding them. |

## Architecture

### Maître is a reserved space

Maître is a workspace record with the reserved slug `maitre` (`core.MaitreSlug`,
`Workspace.IsMaitre()`); no new field. Its directory is `<root>/maitre` (not a
repository); its tmux session is `maitre`. `FriendlyName` slugs are
adjective-noun, so no user space can collide.

Being a space, it reuses — unchanged — agent launch and resume
(`agent.EnsureAgent`/`RunAgent`), swap and handoff, status hooks, attention, and
usage refresh. Created on first `M-m` by `core.EnsureMaitre()`; if its directory
is gone, `gc`'s existing missing-directory rule drops the record and the next
`M-m` recreates it.

Excluded from every space listing and lifecycle sweep: `M-s`, Trash (`M-t`),
`atelier ls`/`spaces`, `purge`, `gc` adoption, and the PR poll. Not excluded
from attention: a blocked Maître (awaiting a confirm, or asking you something)
counts in the `●` badge, because it needs you.

Rejected: a separate entity type (duplicates launch/resume/handoff/hooks), and
an overlay-native chat calling a model API (needs API keys; wouldn't run on the
operator's agent subscriptions).

### Operator verbs

Every verb is both an `atelier` CLI command and a tool on a second MCP tool set,
`atelier mcp --maitre`, registered only in Maître's space. Space agents keep
their three tools (`register_pr`, `create_worktree`, `create_pr`) unchanged.
CLI and MCP share one implementation per verb (a new `internal/operator`
package); CLI prints text by default and JSON with `--json`, MCP returns JSON.

| Verb | Behaviour |
|---|---|
| `spaces [--trash] [--json]` | Active (or retired) spaces: slug, title, intent, agent, status (`working`/`blocked`/`idle`/`gone`), worktrees, cached PRs (state, CI, review, queued), idle time. Extends `ls`; never queries GitHub (reads the PR cache). Excludes Maître. |
| `create <intent> [--agent A]` | Builds a space and prints its slug once the agent launches. The CLI finishes after naming; the MCP caller returns on the slug and leaves the builder running detached, preserving `M-n` behaviour. |
| `rename <space> <title>` | Sets the title in state and on the status line. Slug never changes. |
| `retire <space>` / `restore <space>` | Moves to Trash / back with resume. The logic in `internal/ui` (`setRetired` and restore) moves to a shared package so UI, CLI, and MCP call one implementation. |
| `delete <space>` | `gc.Delete`, behind an operator confirm (below) whose prompt names any unsaved work (`gc.Unsaved`). |
| `purge [--older-than] [--all]` | Existing purge, behind one operator confirm listing the batch. |
| `swap <space> --to A` | Existing swap. |
| `read <space> [--lines N]` | The agent window's visible screen plus N lines of scrollback (default 60), plain text, joined wrapped lines. |
| `send <space> [text] [--key K]… [--no-enter]` | Types text literally into the agent window, then Enter unless `--no-enter`; `--key` sends named keys in order (`Enter`, `Escape`, `Up`, `Down`, `Tab`, `1`–`9`, `C-c`) for menus and permission prompts. A short pause before the Enter, so an agent's paste detection doesn't swallow it. |
| `wait [--timeout D]` | Blocks until any active space's status becomes `blocked` or `idle` after the call starts (or the timeout passes, default 5m), then returns those spaces as `spaces` entries. Watches `CacheDir()/agents/*` status files; no model or network calls. |
| `focus <space>` | Switches the client Maître was opened from to that space's agent window and closes the popup. |

Scope guards: `send`, `read`, `focus`, `swap`, `rename`, `retire`, `delete`
refuse the Maître space itself and unknown or retired slugs (except `restore`
and `delete`, which accept retired ones). `send`/`read` act only on the agent
window.

### Destroying needs the operator

`delete` and `purge`, when the caller is Maître (`ATELIER_SESSION=maitre`,
which every process in its session inherits — MCP server and Bash alike), ask
through tmux on the client(s) attached to the `maitre` session: a prompt naming
the space(s), and for `delete` whether worktrees are dirty. The verb blocks up
to 60s for the answer.

- No attached client (Maître is hidden) → refused at once: "the operator isn't
  looking; ask them to open Maître and retry".
- No, or timeout → refused.
- `--yes`/`--force` are ignored when the caller is Maître; the confirm always
  happens. Outside Maître, the CLI behaves as today.

This holds for every agent, including Codex, which atelier runs without its own
approvals, because the guard lives in atelier, not in the agent.

### Maître's session

- **Directory and config.** `<root>/maitre`, prepared like a space
  (`Kind.Prepare`) but registering the MCP server as `atelier mcp --maitre`
  with `ATELIER_SESSION=maitre`. All Maître tools are pre-allowed in its
  project-local agent settings (Claude: project `permissions.allow`; Codex and
  Grok: their project config). Where an agent has an MCP tool timeout setting,
  it is raised above `wait`'s default (Codex defaults to 60s), so an idle
  Maître blocks in `wait` instead of polling with model turns. An agent without
  one gets a `wait` timeout below its limit, passed in Maître's guide.
- **Guide.** `~/.config/atelier/MAITRE.md`, generated once and user-editable,
  like `WORKSPACE_CLAUDE.md`, linked into Maître's directory as its
  `CLAUDE.md`/`AGENTS.md`. It says:
  - Delegate: coding happens in spaces; Maître writes no code and creates no
    worktrees.
  - Read a space's screen before typing to it.
  - Use `wait` instead of polling.
  - Approve routine permission prompts in spaces. Ask the operator before
    approving anything destructive or irreversible there (force-push, deleting
    data, prod changes).
  - Keep replies short; the popup is small.
- **Launch.** Started fresh with no prompt. Resumed by `EnsureAgent` on every
  `M-m`, so a Maître whose agent exited comes back.

### Popup and keys

- `M-m` (global): records the opening client in `@atelier_maitre_outer`, then
  opens a borderless 70% tmux popup running Bubble Tea. `atelier maitre`
  ensures the record and session, then `ui.RunMaitre` embeds `tmux attach`
  through a PTY and terminal emulator. Bubble Tea renders the shared frame,
  title, separator and legend. Maître's tmux status line is off; the native
  terminal occupies only the body. Closing the PTY detaches the client.
- `Esc` or `M-m` inside Maître (the client's session is `maitre`): `detach-client`,
  which closes the popup. Maître keeps running.
- `M-r` inside Maître: `atelier maitre --restart` respawns its agent window with
  a fresh conversation, the same path as a swap to the same agent. Elsewhere
  `M-r` passes through (readline's revert-line).
- `M-x` inside Maître: the existing swap confirm, for Maître.
- The overlay keys (`M-n`, `M-s`, `M-t`, `M-p`, `M-w`, `M-h`) do nothing inside
  Maître. They'd act on the popup's nested client, not yours; `focus` is how
  Maître moves you. Mechanism: Maître's session sets tmux's `key-table` to
  `maitre`, which binds `Esc`, `M-m`, `M-r`, and `M-x`; every other key goes to
  Maître's agent. `M-m` records the opening client in `@atelier_maitre_outer`
  (not `@atelier_outer`, which `M-x` inside the popup overwrites).
- Splash keymap: `M-m Maître` under "Anywhere", with close, restart and
  swap shortcuts in the Maître group.

## Documentation changes

- **DESIGN.md §1:** a fifth thing, *Delegate the driving*: Maître, an agent that
  operates atelier through the same verbs the operator uses. The non-goal "a
  multi-agent orchestrator" is narrowed to "atelier contains no planning or
  coordination logic; Maître is an agent using atelier's verbs".
- **DESIGN.md §2:** the reserved `maitre` record.
- **DESIGN.md §7:** Maître decisions (popup over long-running session; screen-level
  agent I/O; destroy-confirm owned by atelier).
- **README:** a Highlights bullet, `M-m`/`M-r` in Keys, the operator CLI in a
  short section, a How-it-works line, and `maitre_agent` in Configuration.
- **CLAUDE.md:** layout entry for `internal/operator`; command list.

## Testing

Headless core only, per the repo rule. Anything that shells out to tmux, `gh`,
or an agent stays untested, without new abstractions to mock it.

- Reserved record: `EnsureMaitre` is idempotent; Maître is excluded from `ls`,
  `spaces`, Trash, `purge` candidates, `gc` adoption, and the poll's due set,
  and included in `BlockedCount`.
- `spaces` JSON shape and filters, from a fixture state and PR cache.
- `wait` change detection over a temp status directory: fires on
  working→blocked/idle, ignores Maître and retired spaces, honours the timeout.
- Verb argument validation and scope guards, including refusing the Maître slug.
- Maître-caller rule: `--yes`/`--force` ignored when `ATELIER_SESSION=maitre`;
  refusal when no client is attached (the attached-client lookup is passed in as
  a function, no tmux).
- MCP: the `--maitre` tool list and dispatch, and that the space tool set is
  unchanged.
- Config merges: Maître's MCP registration, allow-list, and tool-timeout entries
  for Claude, Codex, and Grok, preserving existing content.
- `send` key-name validation.

## Out of scope

- The demo sandbox, the Maître brief for it, the demo-repo reset, and the README
  hero recording (sub-project 2).
- Maître editing code or creating worktrees itself.
- Scheduling or recurring Maître runs.
- More than one Maître per instance, or Maître on remote spaces (#104).
