# Contributing to atelier

Thanks for your interest. atelier is small on purpose, and most of what makes a
contribution land is knowing what it deliberately does *not* do. Read the rules
below before writing code; they are enforced in review.

## Setting up

You need Go (the version in `go.mod`), tmux 3.2+, git, the GitHub CLI, and
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

These are tripwires, not preferences. The design doc, [V1.md](V1.md) §6, is the
source.

- **One agent (Claude Code), one forge (GitHub), one renderer (Bubble Tea).**
  Supporting a second means replacing the first. Don't add an interface for a
  hypothetical second implementation, or for a mock.
- **All UI is Bubble Tea**, in `internal/ui`. No second UI technology and no
  shelling out to draw. (Programs atelier opens *in a window*, like the agent, a
  shell, or a diff pager, aren't atelier's UI.)
- **State lives in one JSON file, never in tmux**, and it's a cache and an index.
  Worktrees come from disk and PRs from GitHub; don't store what can be derived.
- **Nothing polls, and there is no daemon.** Changes arrive as events from Claude
  Code's hooks. The one exception is the PR view re-querying GitHub while it is
  open, because GitHub can't push to us; it ends when the view closes.
- **No plugin system.**
- **Delete dead code** rather than keeping it around.
- **A feature ships once it has been wanted three separate times.** Open an issue
  first for anything that adds surface.

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
