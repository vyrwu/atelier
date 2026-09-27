# Contributing to atelier

Thanks for your interest. atelier is small on purpose, and most of what makes a
contribution land is knowing what it deliberately does *not* do. Read the rules
below before writing code; they are enforced in review.

## Setting up

You need Go (the version in `go.mod`), tmux 3.3+, git, the GitHub CLI, and
Claude Code.

```sh
git clone https://github.com/vyrwu/atelier && cd atelier
make dev    # build and run an isolated instance
```

`make dev` runs the freshly built binary with its own tmux socket, state,
config, and workspace root, all under `~/.atelier-dev`. It never touches your
real atelier. `make dev-clean` removes it.

## Before you open a pull request

```sh
make test               # go test ./...
go vet ./...
golangci-lint run ./...
```

CI runs the same checks on Linux and macOS, plus `gofmt` and a goreleaser check.

## The rules

They live in [DESIGN.md §5](DESIGN.md#5-rules), with the reasoning behind them.
They are tripwires, not preferences. Open an issue first for anything that adds
surface.

## Tests

Tests cover the headless core: the state file, paths and config, agent status,
the Claude hook merge, worktree derivation, the PR query and its parsing, and
the list rendering. Code that shells out to tmux, `gh`, or Claude
stays untested rather than growing an abstraction to be mocked.

Add a test for the behaviour a change pins, especially a bug fix, and check that
it fails without the fix.

## Commits and pull requests

Commits follow [Conventional Commits](https://www.conventionalcommits.org), which
[release-please](https://github.com/googleapis/release-please) reads to version
releases and write the changelog:

```
fix(forge): Keep a failed repo's PRs on a partial refresh
feat(ui): Open a PR's diff with Enter
```

- `feat` for features, `fix` for bug fixes. `perf`, `refactor`, and `docs` also
  appear in the changelog; `test`, `ci`, `chore`, and `build` don't.
- Mark a breaking change with `!` after the type, plus a `BREAKING CHANGE:`
  footer.
- Keep the subject short and in the imperative. Use the body to explain why.

Pull requests are squash-merged, so the PR title becomes the commit on `main`.
Give it the same format.

## Layout

| Path | |
|---|---|
| `cmd/atelier` | the one binary: `up`, `open`, `home`, `create`, `win`, `hook`, `mcp`, `install`, `version` |
| `internal/core` | domain types, the state file, config, paths |
| `internal/tmux` | the tmux wrapper (dedicated socket) |
| `internal/git` | worktree derivation and git queries |
| `internal/forge` | GitHub: the PR query and PR state changes |
| `internal/agent` | Claude Code: launch and resume, status, hooks, project setup |
| `internal/mcp` | the stdio MCP server agents use: `create_worktree`, `create_pr`, `register_pr` |
| `internal/ui` | the overlay and the splash |

## Reporting bugs and asking for features

Use the [issue templates](https://github.com/vyrwu/atelier/issues/new/choose). For
a bug, include `atelier version` and the dependency check from the splash
(`M-h`).

For security issues, see [SECURITY.md](SECURITY.md). Please don't open a public
issue.
