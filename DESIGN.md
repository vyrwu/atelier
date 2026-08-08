# atelier — Design Doc

Terminal-centric agentic dev framework. Per-workspace tool clusters in tmux, driven by a Go engine.

## TL;DR

- **Repo:** `github.com/vyrwu/atelier`
- **Language:** Go
- **Architecture:** one Go binary — a load-bearing kernel (ports) + config-selected adapters + config launchers.
- **Multiplexer:** tmux is a hard dependency. Not replaced.
- **Distribution:** Homebrew / nix binary. Runs as a bundled tmux runtime (`atelier`) or embeds into your own tmux (`atelier init --bare`).

## Concepts

| Concept | Definition |
|---|---|
| **Workspace** | First-class canvas. A git worktree + tmux window + state attached to that pair. |
| **Tool** | A TUI program scoped per workspace (Claude, k9s, pgcli, lazygit, granted, popup shell). |
| **Atelier** | The binary that manages workspaces and orchestrates tools. |

Workspaces are the substrate. Tools operate on or within workspaces. The workspace-picker is a tool; the workspace itself is not.

## CLI surface

```
atelier                            # boot the bundled tmux runtime (default)
atelier workspace list|info|create|switch|delete   # workspace primitive
atelier tools list                 # list registered tools + their capabilities
atelier tools <name> <action>      # e.g. atelier tools k8s open
atelier ai open | set-prompt | recap        # drive the configured AI integration
atelier status freshness … | attention count | forge …   # status-line emitters
atelier state show [--json] | sync | restore   # topology + invariant report / cache ops
atelier reconcile [--fix]          # report (default) / repair invariant violations
atelier init [--bare]              # generate the tmux.conf snippet
atelier doctor                     # verify tmux, fzf, tools, requirements
```

Plain English. Metaphor lives at the binary name only; not in the API.

### State model & recovery

`internal/state` owns one validated model of atelier's tmux entity graph —
sessions (repos), windows (workspaces), popups (tools), clients, the launcher,
and the outer-focus pointer — captured from a single tmux read. The model is
**derive-and-validate, not cache-and-trust**: persisted tmux options are a
hint, re-derived when stale. Two commands expose it:

- `atelier state show [--json]` — one-shot topology + invariant report.
  Read-only. Replaces the `tmux list-*/show-*` archaeology a wedged state used
  to require.
- `atelier reconcile` — reports invariant violations (dry run). `--fix`
  repairs the **fixable** ones. It operates only on live tmux; the persisted
  cache is reconciled separately by `atelier state sync`.

Violations split into two classes, and this distinction is load-bearing for
what `--fix` will and won't do:

| Class | Violations | `--fix` behavior |
|---|---|---|
| **Fixable** | orphan popup, outer-on-launcher/popup/stale, detached outer-client hint, client-moving hook armed at rest, attention stranded on a **popup** window | auto-repaired (kill / clear / disarm) |
| **Report-only** | attention on a non-listable **workspace** window, working-dir-gone, >1 workspace client per tty | surfaced only — the fix is a human action |

(Attention *misrouted onto a popup-backing window* is fixable — a popup must never carry it and the rollup ignores popups, so clearing is safe. Attention on a real-but-non-listable workspace window is report-only, since it might be a genuine signal on a workspace that lost its metadata.)

**`reconcile` is not for clearing the attention badge.** Attention on a
*listable* workspace (`⏺`) is a legitimate per-workspace signal — an agent
finished and you haven't looked — not a fault. It clears when you visit the
workspace (the `after-select-window` hook). Only attention on a *popup or
non-listable* window is a violation (`misrouted_attention`), and that is
report-only. So a healthy server with pending attention correctly reports
"no violations".

**Continuous self-heal + badge refresh (the background loop).** Reconcile is
also pull-based on its own — it only runs when invoked. A single long-lived
process (`atelier tools workspaces _refresh-loop`, started from the generated
config via `run-shell -b`) closes that gap on a heartbeat. Each tick it (1)
refreshes freshness + forge badge *data* (rendering was already continuous; only
the data was event-tied), TTL-throttled — with per-repo fetch dedup and a
per-tick cap — so most ticks stamp nothing, (2) runs a **loop-safe** reconcile
(`ReconcileLoop`) so misrouted/phantom attention and orphan popups repair
themselves without a manual run, and (3) is the **workspace observer**: for each
active workspace it re-reads the agent's session transcript and, in one cheap
haiku pass, re-derives the one-line recap AND a 3-state attention verdict —
*blocked* (waiting on you → yellow ⏺), *running* (working / waiting on a
sub-agent → blue ⏺), or *idle* (→ no dot). This replaces the old Claude Stop
hook: atelier installs nothing into the agent's config; recap + attention are
pulled, throttled on the transcript mtime so a quiet workspace costs only a
stat. Only *blocked* raises `@needs_attention` (the status-line rollup + the
clear-on-visit hook), so the noisy "every stop = attention" signal is gone. The
`AIIntegration` port is now pull-only. It deliberately does *not* auto-run the
repairs whose safety depends on "no popup is mid-open" (disarming a
`client-detached` hook, clearing the outer-client hint) — a 45s snapshot can't
tell a leaked hook from one `OpenOnOuter` armed a millisecond ago, so those stay
with the human-invoked `reconcile --fix`. It is a loop **around** the kernel —
Topology/Validate/Reconcile stay the authority; the tick is only "go look", the
reconcile against live tmux is the truth (edge-triggered wakeup, level-triggered
reconcile). Sweeps run synchronously in-process — never a detached child per
tick, which is what once leaked hundreds of `_bg-pull` git procs. Lifecycle is
pid-bound (the loop self-exits when its tmux server is gone or replaced) and
singleton-guarded with per-tick self-eviction, not reliant on a SIGHUP. A
`tmux -C` control-mode source could later replace the ticker as the wakeup
without touching the kernel. See issues \#17 / \#81.

## Architecture — kernel (ports) + integrations (adapters) + launchers

atelier is **one binary**, structured as **ports & adapters
(hexagonal)**. There is no runtime plugin registry, no subprocess
manifest protocol, no PATH scan. The kernel is load-bearing and
predictable; the swappable, capability-specific logic lives in bounded
adapters selected by config. Predictable over dynamic.

**1. Kernel.** Owns everything structural AND all presentation: the
workspace lifecycle (worktree + window + session), popup lifecycle,
window/workspace state, the *views* (workspace creator/selector/history,
tool picker, help, statusline), and the **capability slots** those views
expose — the per-row AI *summary*, the *attention* sigil, the code-forge
*badge*, branch *naming*. The kernel defines what slots exist and how
they render/sort; it does NOT know who fills them. Lives in
`internal/workspace`, `internal/popup`, `internal/statestore`, the view
code (`internal/tools/workspaces`, `internal/tools/toolselector`), and
the ports in `internal/integration`.

**2. Integrations (adapters).** A capability the kernel can't implement
itself — an AI summary, a forge status — is a **port** it defines and
*calls*. An **integration** is an adapter that satisfies a port; it is a
bounded provider, never a driver. The kernel pulls; integrations don't
push into open slots.

- `AIIntegration` — the agent that inhabits a workspace: open-in-popup,
  branch naming, and pull-based recap + attention verdict (`RefreshRecap`
  re-reads the transcript; there is no stop-hook). Adapters:
  `internal/adapters/claude` (default), `mock` (tests/dev). A
  codex/gemini adapter would implement the same port.
- `ForgeIntegration` — per-workspace code-forge status → the picker
  badge + open-in-browser. Adapter: `internal/adapters/github`. A
  GitLab adapter would implement the same port.

The kernel keeps the *policy* (naming system-prompt + conventional-commit
validation; the forge state vocabulary + glyph + sort order); the adapter
supplies the raw value. Selected + configured via `[integrations]`; when
unset the capability degrades gracefully (no summary/badge, manual
naming). **Dependency rule:** integrations import the kernel's ports; the
kernel imports NO integration; `cmd/atelier` (composition root) is the
only place that maps config → concrete adapter and installs it via
`integration.SetActive`.

```toml
[integrations]
ai    = "claude"   # AIIntegration adapter (default: claude; "" disables)
forge = "github"   # ForgeIntegration adapter (default: off)
```

**3. Launchers (config).** A plain TUI is not an integration — it fills
no capability slot. Register *any* command as a launcher with a
`[tools.<name>]` block; atelier binds a key, opens it in a popup, and
owns the window state. This is how a user adds a tool without Go — e.g.
wrap k9s with AWS SSO in a `granted-k9s` script:

```toml
[tools.k9s-aws]
launch       = "granted-k9s"     # any executable on PATH
popup        = "global"          # workspace | global | none
key          = "K"               # optional tmux binding
requires     = ["granted-k9s"]   # doctor checks these
icon         = "胡"
accent_color = "110"
title        = "K9s (AWS)"
```

Built-in **tools** (k8s/pg/aws context+auth pickers) carry real
pre-launch logic — interactive context/credential selection before the
TUI opens — so they stay compiled-in as packages under
`internal/tools/<name>` (registered in `internal/tools/all`, dispatched
via `atelier tools <name>`). Simpler tools with no pre-launch logic
(lazygit, the shell popup, gh-dash, gh-enhance, ccusage) are `[tools.*]`
config launchers, not compiled in. Built-ins + config launchers
merge into one list at `plugin.Discover()`; every consumer (dispatcher,
`atelier init`, selector, `doctor`) reads that merged view. Built-ins win
a name collision.

### Why this shape

When an integration's behavior becomes load-bearing or changes
presentation, the *kernel* absorbs the feature + its contract (defines a
port, wires it into a view, owns the rendering); the adapter keeps only
the irreducible provider logic behind the port. We grow the kernel's
port surface deliberately — never a dynamic injection mechanism. That
keeps the kernel both predictable and swappable, and makes it testable:
inject the `mock` `AIIntegration` and the summary/attention/naming paths
exercise with zero Claude, zero network.

## Repo layout

```
atelier/
├── cmd/atelier/             # the single binary: cobra root + composition root (integrations.go)
├── internal/
│   ├── cli/                 # cobra command tree (ai, state, reconcile, status, popup, server, …)
│   ├── integration/         # KERNEL PORTS: AIIntegration, ForgeIntegration, active Set
│   ├── adapters/            # ADAPTERS (imported only by cmd/atelier):
│   │   ├── claude/          #   AIIntegration (default AI agent; pull-based recap/attention)
│   │   ├── github/          #   ForgeIntegration (PR badge via gh)
│   │   └── mock/            #   AIIntegration + ForgeIntegration (deterministic; tests/sandbox)
│   ├── plugin/              # tool registry: RegisterBuiltin, Discover, launcher specs
│   ├── manifest/            # Manifest type (in-tree literal / synthesized from [tools.*])
│   ├── toolmain/            # in-process tool dispatch (cancel/error → exit code)
│   ├── workspace/           # workspace-lifecycle primitive + option-key constants (kernel)
│   ├── popup/               # popup-lifecycle primitive (kernel)
│   ├── host/popup/          # host-facing popup helpers used by the kernel
│   ├── state/               # live tmux topology + invariants (derive-and-validate; reconcile)
│   ├── statestore/          # persisted disk cache (~/.cache/atelier/state.json) + rehydrate
│   ├── initgen/             # `atelier init` binding/hook/statusline/theme generation
│   ├── tmuxhost/            # sole tmux shell-out layer (testable)
│   ├── awsassume/           # `granted assume` shell-function wrapper (aws tool)
│   ├── config/              # TOML loader + path expansion
│   ├── dispatch/            # shell-string builders for `atelier tools …` dispatch
│   ├── fzf/ + fzfstyle/     # fzf invocation/parsing + shared picker styling
│   ├── spinner/             # progress-indicator popup UI
│   ├── seed/scenarios/      # scenario YAML → real repos/worktrees/state (sandbox + e2e)
│   ├── perf/ + debuglog/    # perf instrumentation + ~/.cache/atelier/debug.log
│   ├── testtmux/            # isolated tmux-server fixtures for e2e tests
│   └── tools/               # kernel views + built-in logic-carrying tools
│       ├── all/             # registers every built-in tool (blank-imported by main)
│       ├── workspaces/      # kernel view: creator/selector/history + forge badge + refresh loop
│       ├── toolselector/    # kernel view: M-; tool picker
│       ├── k8s/             # built-in tool: k9s context picker (requires k9s)
│       ├── pg/              # built-in tool: pgcli context picker (requires pgcli)
│       └── aws/             # built-in tool: granted-assume profile picker (requires granted)
├── sandbox/                 # ephemeral seeded demo runtime (package main, not shipped)
├── examples/tmux/           # reference embeddings (minimal / powerline / vyrwu)
├── flake.nix · go.mod · Makefile · .golangci.yml
└── .github/workflows/       # ci.yml, release.yml, release-please.yml
```

## Engine ⇄ tmux split

| Responsibility | Lives in |
|---|---|
| State (active workspace, tool contexts, attention flags) | Go binary, typed structs in-process |
| Tool invocation logic, popup orchestration | Go binary |
| tmux bindings, hooks, status-line interpolations | tmux plugin (generated by `atelier init`) |
| Status-line data | Go binary via `atelier status` queries |
| Status-line layout / colors | User's tmux config (Dracula etc.) |

Tmux options and env vars are IPC for invocation only, not authoritative state. This eliminates the "scripts disagree about state" bug class from the bash setup.

## Status-line integration

Standard `#( ... )` interpolation pattern (parallels `gitmux`, `tmux-cpu`):

```tmux
set -g status-right "#( atelier status attention count ) | %H:%M"
```

The per-window freshness segment (git ahead/behind) is injected into
`window-status-current-format` automatically by `atelier internal stamp-statusline`
(wired by `atelier init`), since it needs per-window args. Only the current
format is decorated, so the bar shows just the focused workspace; inactive
windows render nothing. The `attention count` rollup is a standalone emitter
you can place anywhere.
Coexists with any status-line framework. Go binary startup ≤30ms — fine
for 3s refresh.

## Distribution

Ship one binary; two ways to run it.

```bash
brew install vyrwu/tap/atelier   # or: nix run github:vyrwu/atelier
```

1. **Embed into your own tmux (default).** Add `run-shell 'atelier init
   --bare | tmux source-file -'` to your `tmux.conf`. Emits engine wiring
   only (bindings, hooks, statusline emitters) on top of your existing
   config. This is the author's daily driver.
2. **Bundled runtime.** `atelier` with no subcommand spawns its own tmux
   server on a dedicated socket (`tmux -L atelier`) with curated defaults —
   the zero-config path for anyone without a tmux setup.

See the README and [`docs/EMBEDDING.md`](docs/EMBEDDING.md) for the
load-bearing details.

## Window management belongs to the workspace primitive

**Rule:** tools MUST NOT call `tmux switch-client`, `select-window`, `new-window`, `new-session`, or stamp workspace metadata (`@ai_*`, `@repo_path`, `@attention_*`, `@agent_status`) directly. They go through `internal/workspace`.

**Why:** every tool that opens or transitions to a workspace hits the same set of edge cases — picking the right outer client, ordering select-window before switch-client, killing auto-created default-branch windows, propagating `@atelier_outer_client` through popup-pty chains. When this logic is inlined per-tool, a fix in one place leaves the same bug latent in every other tool. When it lives in the primitive, a fix lands once.

**The primitive owns:**
- Session creation (`workspace.EnsureSession`)
- Worktree-window creation + stamping (`workspace.CreateWorktreeWindow`)
- Outer-client landing — the select-window + switch-client -c outer dance (`workspace.LandOuter`)
- Workspace metadata read/write (`SetAttention`, `SetRecap`, etc.)

**Tools own:** their own popup UX, fzf binds, custom transformations, multi-stage spinner labels, plugin-specific keybinds. Anything where the user's MENTAL MODEL of the tool changes — not the underlying tmux mechanics.

If a tool needs a tmux operation that's not in the primitive, ADD IT TO THE PRIMITIVE — don't reach around it. The primitive stays narrow (no fzf, no picker logic, no spinner) — but it owns the entire workspace-lifecycle surface.

## Tool contract

A built-in tool contributes two symbols and one registration line:

```go
// internal/tools/<name>/register.go
var Manifest = &manifest.Manifest{
    Name:          "k8s",
    Description:   "k9s context picker",
    Popup:         manifest.KindGlobal,   // launch shape: workspace | global | none
    PrimaryInvoke: "open",
    Binding:       &manifest.Binding{Key: "…", Style: manifest.StyleFull, Invoke: "open"},
    Requires:      []string{"k9s"},       // doctor checks these on PATH
    Tool:          true,                   // appears in the M-; selector
    // also: Bindings, UI, PickerBindings
}

func AddCommands(root *cobra.Command) { root.AddCommand(OpenCommand(), ...) }

// internal/tools/all/all.go
plugin.RegisterBuiltin(k8s.Manifest, k8s.AddCommands)
```

The manifest is a compile-time Go literal (built-ins) or synthesized
from a `[tools.*]` block (launchers) — never marshalled across a
subprocess boundary. `Popup` classifies the backing-session lifecycle:

- **workspace** — popup session per parent window; dies when the window
  dies (the AI agent, popup shell, lazygit)
- **global** — singleton backing session across all workspaces (k9s,
  pgcli)
- **none** — no persistent popup (pickers, providers)

Presentation *capabilities* — the AI summary/attention, the forge badge —
are NOT declared on a manifest. They are kernel ports filled by swappable
integration adapters (see the kernel/integration split above), so a tool's
manifest carries only how it binds, its popup shape, and what it requires.

## Non-goals

- Multiplexer replacement. Tmux stays.
- A runtime plugin SDK / third-party manifest protocol. Extension is a
  config `[tools.*]` launcher (any command) or a built-in PR — not a
  discovered-binary contract. The out-of-process `atelier-<name>`
  manifest-protocol model was removed; it paid full IPC + versioning +
  distribution cost for a decoupling the project never wanted (see the
  single-binary rationale above).
- Cross-multiplexer abstraction layer. tmux is the only target for v1.
- IDE features (editor, LSP, debugging). Atelier orchestrates other tools; it isn't one.
