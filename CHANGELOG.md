# Changelog

All notable changes to this project will be documented in this file.

The format loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project aims for [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] — first public cut

### Architecture

- Core binary (`atelier`) is fully tool-agnostic. Tools live as separate
  `atelier-<name>` binaries discovered on `PATH`.
- Plugin contract: tools respond to `--atelier-manifest` with versioned
  JSON describing their name, bindings, popup kind, and requirements.
- Workspace primitive (`atelier workspace list|info|create|switch|delete`)
  lives in core. Tools query it for cwd/repo/branch instead of coupling.
- State object (`atelier state`) gives every tool typed runtime context:
  current pane, in-popup detection, outer-chain tracking.
- `atelier popup outer <cmd>` renders a popup on the outer (non-popup)
  client without detaching the inner — replaces bash `tmux_outer_popup`.

### Bundled tools

- `atelier-popupshell` — per-window persistent shell popup
- `atelier-lazygit` — per-window lazygit popup
- `atelier-claude` — per-window Claude Code popup with per-window prompt
  seeding (`@claude_prompt`) and recap parsing from transcripts
- `atelier-k8s` — singleton k9s popup with context switching from
  `~/.config/atelier/k8s/contexts.yaml` (aws-vault + EKS auth supported)
- `atelier-pg` — singleton pgcli/pgcenter with endpoint switching, AWS SSM
  password fetching, libpq URI construction
- `atelier-aws` — aws-vault profile picker that respawns the outer pane
- `atelier-workspaces` — fzf repo picker + git worktree creation, session
  switcher (sorted by attention/recap/name), multi-repo (non-git) sessions,
  clone-from-URL
- `atelier-toolselector` — fzf master picker over every discovered tool

### Distribution

- Prebuilt binaries for linux/macos × amd64/arm64 via goreleaser
- GitHub Actions CI runs build + unit + e2e on
  linux-amd64, linux-arm64, macos-amd64 (intel), macos-arm64 (apple silicon)
- GitHub Actions release workflow triggered on `v*.*.*` tag push
- Source-install path via `make install` (default `$HOME/.local/bin`)
- Nix dev shell with pinned tmux, go, fzf, jq, yq, golangci-lint, goreleaser

### Documentation

- `README.md` — install, wiring, CLI surface, state architecture
- `CONTRIBUTING.md` — plugin authoring guide with a 10-line bash example
- `DESIGN.md` — full architecture + bash → Go feature-parity inventory

[Unreleased]: https://github.com/vyrwu/atelier/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/vyrwu/atelier/releases/tag/v0.1.0
