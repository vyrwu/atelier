# Contributing to atelier

Thanks for your interest. atelier is **one binary**. There are two ways
to add a tool — pick by how much behavior you need. Neither involves a
subprocess manifest protocol or an `atelier-<name>` binary on PATH; that
model was removed (see [DESIGN.md](DESIGN.md) → "Non-goals").

## Option 1 — a launcher (no code, no recompile)

Register *any* command as a tool with a `[tools.<name>]` block in
`$XDG_CONFIG_HOME/atelier/config.toml`. atelier binds a key, opens the
command in a popup, and owns the window state. The command can be
anything on PATH — a script you wrote, another TUI, a wrapper.

```toml
[tools.hello]
launch      = "sh -c 'echo hello, atelier!; read -n1 -s'"
popup       = "none"     # workspace | global | none
key         = "h"        # optional tmux binding
title       = "Hello"
description = "Print hello to the popup"
requires    = []         # external commands doctor should verify
```

```bash
atelier doctor            # lists it under "Discovered tools", checks `requires`
atelier tools hello       # runs the launch command in a popup
atelier init              # includes hello's binding block
```

Popup shapes:

- `workspace` — a per-parent-window backing session (survives while the
  window lives). Set `start_cwd = true` to open at the pane's cwd.
- `global` — one singleton backing session shared server-wide (k9s /
  pgcli style). Your `aws-vault-k9s` wrapper goes here.
- `none` — exec the command directly in the popup pty; no backing
  session.

Launcher fields: `launch` (required), `popup`, `key`, `key_table`,
`requires`, `icon`, `accent_color`, `title`, `description`, `invoke`,
`start_cwd`.

That's it. No source changes, no recompile.

## Option 2 — a built-in (a PR)

Richer tools — those that provide a capability slot (attention, a picker
badge, a summary) or need deep integration with the workspace primitive
— live in-tree and compile into the one binary. A built-in is a package
under `internal/tools/<name>/` exposing two symbols, plus one
registration line.

```go
// internal/tools/yourtool/register.go
package yourtool

import (
    "github.com/spf13/cobra"

    "github.com/vyrwu/atelier/internal/manifest"
)

var Manifest = &manifest.Manifest{
    Tool:          true,                 // appears in the M-; selector
    Name:          "yourtool",
    Description:   "short human description",
    Popup:         manifest.KindWorkspace,
    PrimaryInvoke: "open",
    Binding:       &manifest.Binding{Key: "x", Style: manifest.StyleFull, StartCwd: true, Invoke: "open"},
    Requires:      []string{"fzf"},
}
// (To fill a kernel capability slot — AI summary/attention, forge badge —
//  write an integration adapter instead; see Option 3.)

func AddCommands(root *cobra.Command) {
    root.AddCommand(OpenCommand())
}
```

```go
// internal/tools/all/all.go — one line
plugin.RegisterBuiltin(yourtool.Manifest, yourtool.AddCommands)
```

`atelier tools yourtool open` now dispatches to your `OpenCommand` in
the same process; `atelier init` emits its binding; the selector lists
it (when `Tool: true`); `atelier doctor` checks `Requires`.

### Manifest fields

| Field | Description |
|---|---|
| `Name` | tool name (no `atelier-` prefix) |
| `Description` | shown in `atelier tools list` + selector |
| `Tool` | `true` to appear in the M-; selector; omit for pure providers |
| `Popup` | `KindWorkspace` / `KindGlobal` / `KindNone` — launch shape |
| `Binding` / `Bindings` | tmux key bindings emitted by `atelier init` |
| `Requires` | external commands `atelier doctor` verifies on PATH |
| `UI` | icon / accent color / popup title for the selector |
| `PickerBindings` | in-popup key hints for the cheatsheet |

(Presentation CAPABILITIES — AI summary/attention/naming, forge badge —
are NOT declared on a tool manifest. They are kernel ports filled by
swappable integration adapters. See Option 3.)

## Option 3 — an integration adapter (swap a capability)

To change *who fills a kernel capability* — the AI agent (branch naming,
summary, attention, the popup agent) or the code forge (PR badge) — write
an adapter that satisfies the kernel port in `internal/integration` and
wire it at the composition root. The kernel does not change.

```go
// internal/adapters/codex/codex.go
package codex

import "github.com/vyrwu/atelier/internal/integration"

type Adapter struct{}
func New() *Adapter { return &Adapter{} }
var _ integration.AIIntegration = (*Adapter)(nil) // implement the port's methods
```

```go
// cmd/atelier/integrations.go — one line in composeIntegrations()
case "codex":
    set.AI = codex.New()
```

Then `[integrations] ai = "codex"` selects it. Ports:

- `AIIntegration` — `OpenAgent`, `SetPrompt`, `GenerateName`, `OnStop`,
  `Summarize`, `EnsureHooks`, `AgentPopupSession`, `HasResumableState`.
  The KERNEL owns the naming instruction + conventional-commit validation;
  the adapter runs its model and manages its own resume/session semantics.
- `ForgeIntegration` — `Status` (classify into the kernel's `ForgeState`),
  `Open`. The KERNEL renders the glyph + sort order.

**Dependency rule:** an adapter imports `internal/integration` (the port)
+ kernel primitives; it must NEVER be imported by the kernel. Only
`cmd/atelier` maps config → adapter. Test your adapter against the port
(`var _ integration.AIIntegration = ...`) and add a unit test; the `mock`
adapter shows the minimum.

## Host services

Tools call back into the core for shared services. CLI surface:

```bash
# Inspect runtime state (where am I? what's the outer pane?)
atelier state

# Get info about the workspace containing a pane
atelier workspace info --format=json
atelier workspace info --format=cwd
atelier workspace info --format=repo

# List all workspaces
atelier workspace list

# Create a new workspace
atelier workspace create --dir=/path --name=feat/foo

# Switch to one
atelier workspace switch <session:window>

# Open a popup on the outer (non-popup) client
atelier popup outer <command>

# Clean up orphaned popup sessions (called from hooks)
atelier popup cleanup

# Ensure a backing popup session exists
atelier internal ensure-workspace-popup --tool=mytool --cmd=mycmd
atelier internal ensure-global-popup --tool=mytool --cmd=mycmd

# Attach a tmux client to a session
atelier internal attach --session=mysession
```

Go-written tools can import `github.com/vyrwu/atelier/internal/popup`,
`internal/state`, `internal/workspace`, etc. directly for in-process speed.

## Distribution

- **Launcher**: nothing to distribute — it's a `[tools.*]` block in the
  user's `config.toml` pointing at a command they already have on PATH.
- **Built-in**: ships inside the single `atelier` binary once your PR
  merges. `brew install vyrwu/tap/atelier` gets it. There are no
  per-tool packages.

## Style + behavior expectations

- **Tools must never block on missing tmux state.** If `atelier state` reports no outer pane, return a clear error explaining the binding must set the `@atelier_outer_*` globals.
- **Tools must be cancellable.** If the user dismisses fzf, return exit 0 (not an error).
- **Tools must be idempotent.** Calling `open` twice should not create duplicate backing sessions.
- **Tools must not panic on missing dependencies.** Declare them in `requires` so `atelier doctor` can warn.

## Adding a built-in to atelier's official set

Open a PR that:

1. Adds `internal/tools/yourtool/` with your command constructors and a
   `register.go` exposing `Manifest` + `AddCommands` (see Option 2 above).
2. Adds one `plugin.RegisterBuiltin(yourtool.Manifest, yourtool.AddCommands)`
   line to `internal/tools/all/all.go`.

It builds via `make build` and installs via `make install`. `cmd/atelier`
never changes — the registry is the single wiring point. In-process
dispatch (cancel → exit 130, error → pause-and-exit) is handled for you by
`toolmain.Dispatch`; you don't call it directly.

## Agent stop-hook integration

The kernel exposes `atelier ai on-stop` as the agent stop-hook entry
point. The active AI adapter's `OnStop` raises `@needs_attention` on the
workspace's parent window (unless the popup is attached) and refreshes the
summary (`@attention_recap`) from the agent's latest transcript — both via
the kernel verbs `workspace.SetAttention` / `SetRecap`.

The Claude adapter installs this automatically (via `EnsureHooks` →
atelier's `--settings` file), so you don't hand-edit
`~/.config/claude/settings.json`. The canonical hook it writes is:

```json
{
  "hooks": {
    "Stop": [
      { "type": "command", "command": "atelier ai on-stop" }
    ]
  }
}
```

The hook runs inside the agent popup, which is an atelier-managed tmux
session. `OnStop` resolves the outer (workspace) window from the chain and
sets the options on it. Your tmux status line then shows the attention
rollup via `atelier status attention count`.

Atelier also reads two window options that you can set per-workspace from
the workspaces tool or by hand:

- `@claude_prompt` — initial prompt passed to claude on next popup open
- `@claude_workspace_kind` — `single-repo` | `multi-repo`. When
  `multi-repo`, claude is launched with `--append-system-prompt
  <claude.multi_repo_system_prompt>` from atelier's config.

## Repository conventions

- Format: `gofmt`.
- Lint: `golangci-lint run` must pass.
- Tests: each tool's logic library (under `internal/tools/<name>/`) should have unit tests for naming/parsing/pure logic and e2e tests for tmux-interacting behavior. E2e tests use `internal/testtmux` for isolation.
- Commits: follow the existing repo style.
- PRs: include `atelier init` output before/after if your change affects bindings.
