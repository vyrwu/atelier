# Contributing to atelier

Thanks for your interest. Atelier's plugin architecture is designed for community contributions — you can ship a tool without forking the core.

## Writing a new tool

A tool is any executable named `atelier-<name>` on `PATH` that responds to the manifest contract.

### Minimum viable tool (in any language)

```bash
#!/usr/bin/env bash
# atelier-hello — a minimal demo tool.

if [ "$1" = "--atelier-manifest" ]; then
  cat <<'EOF'
{
  "api_version": 1,
  "name": "hello",
  "description": "Print hello to the popup",
  "binding": {
    "key": "h",
    "title": "Hello",
    "style": "full",
    "invoke": "open"
  },
  "popup": "none",
  "requires": [],
  "subcommands": ["open"]
}
EOF
  exit 0
fi

case "$1" in
  open)
    echo "hello, atelier!"
    read -n1 -s
    ;;
  *)
    echo "usage: atelier-hello open" >&2
    exit 1
    ;;
esac
```

Install it:

```bash
chmod +x atelier-hello
cp atelier-hello ~/.local/bin/

atelier doctor                  # should now list it
atelier tools hello open        # dispatches to atelier-hello open
atelier init                    # includes hello's binding block
```

Reload tmux and `prefix+h` (or whatever your prefix is, then `h`) opens the popup.

That's it. No atelier source modifications. No recompile. No registration.

## Manifest contract

Every tool must respond to `--atelier-manifest` with JSON matching this schema:

```json
{
  "api_version": 1,
  "name": "your-tool-name",
  "description": "short human description",
  "version": "0.1.0",
  "binding": {
    "key": "x",
    "key_table": "root",
    "title": "Your Tool",
    "style": "full",
    "start_cwd": true,
    "invoke": "open"
  },
  "popup": "workspace",
  "requires": ["fzf", "git"],
  "subcommands": ["open", "switch"]
}
```

| Field | Required | Description |
|---|---|---|
| `api_version` | yes | Must be `1` for current core |
| `name` | yes | Tool name (no `atelier-` prefix) |
| `description` | recommended | Shown in `atelier tools list` and the tool selector |
| `binding` | optional | Tmux key binding details |
| `binding.key` | if binding set | Tmux key (e.g. `"p"`, `"M-n"`) |
| `binding.key_table` | optional | Default `"root"` |
| `binding.title` | optional | Popup title bar text |
| `binding.style` | optional | `"full"` (rounded, bottom-anchored) or `"picker"` (-B compact) |
| `binding.start_cwd` | optional | If true, popup starts at `#{pane_current_path}` |
| `binding.invoke` | optional | Subcommand to call (default `"open"`) |
| `popup` | optional | `"workspace"` (per-window) / `"global"` (singleton) / `"none"` |
| `requires` | optional | External commands that must be on PATH |
| `subcommands` | optional | Documentation hint for help output |

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

- **Standalone binary on PATH**: drop `atelier-yourtool` anywhere on `PATH`.
- **Homebrew tap**: `brew install yourname/tap/atelier-yourtool`.
- **Nix package**: `nix run github:you/atelier-yourtool`.

The core only needs the binary on PATH. How it got there is your choice.

## Style + behavior expectations

- **Tools must never block on missing tmux state.** If `atelier state` reports no outer pane, return a clear error explaining the binding must set the `@atelier_outer_*` globals.
- **Tools must be cancellable.** If the user dismisses fzf, return exit 0 (not an error).
- **Tools must be idempotent.** Calling `open` twice should not create duplicate backing sessions.
- **Tools must not panic on missing dependencies.** Declare them in `requires` so `atelier doctor` can warn.

## Building tools in Go

Use the shared `toolmain` helper for boilerplate:

```go
package main

import (
    "github.com/spf13/cobra"
    "github.com/vyrwu/atelier/internal/manifest"
    "github.com/vyrwu/atelier/internal/toolmain"
)

var Manifest = &manifest.Manifest{
    APIVersion: manifest.APIVersion,
    Name:       "yourtool",
    // ...
}

func main() {
    toolmain.Run(Manifest, func(root *cobra.Command) {
        root.AddCommand(myOpenCommand())
    })
}
```

The helper handles `--atelier-manifest` dispatch and cobra root setup for you.

## Adding a tool to atelier's official set

If you want your tool shipped as a default-with-atelier extension, open a PR adding `cmd/atelier-yourtool/main.go`. It builds via `make build` and installs via `make install`. The core never changes.

## Claude Code hook integration

Atelier exposes `atelier tools claude notify-attention` as the hook entry
point — it raises `@needs_attention` on the workspace's parent window and
refreshes `@recap` from the latest Claude transcript.

Add to `~/.config/claude/settings.json`:

```json
{
  "hooks": {
    "Stop": [
      {
        "type": "command",
        "command": "atelier tools claude notify-attention"
      }
    ]
  }
}
```

The hook runs inside the Claude popup, which is an atelier-managed tmux
session. `atelier state` resolves the outer (workspace) window from the
chain, and `notify-attention` sets the option on that window. Your tmux
status line then shows " ● <N>" via `atelier status attention --count`.

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
