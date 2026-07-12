# Changelog

All notable changes to this project will be documented in this file.

The format loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project aims for [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.1](https://github.com/vyrwu/atelier/compare/v0.3.0...v0.3.1) (2026-07-12)


### Bug fixes

* **workspaces:** kill tmux before TempDir in become-race e2e ([#25](https://github.com/vyrwu/atelier/issues/25)) ([1b1897b](https://github.com/vyrwu/atelier/commit/1b1897b97c1e1cb4e4ec362e00f8f8f8bd3ac627))

## [0.3.0](https://github.com/vyrwu/atelier/compare/v0.2.1...v0.3.0) (2026-07-12)


### Features

* **kernel:** single-binary kernel with adapter ports ([#16](https://github.com/vyrwu/atelier/issues/16)) ([4f04dca](https://github.com/vyrwu/atelier/commit/4f04dca40f1557ac8cbb08bb7946da8c6474c842))
* **statusline:** forge PR badge after attention ([#22](https://github.com/vyrwu/atelier/issues/22)) ([6830b94](https://github.com/vyrwu/atelier/commit/6830b9431b026df63e05eb21917c99d59ddab058))
* **workspaces:** forge badge TTL 5m→1m ([#19](https://github.com/vyrwu/atelier/issues/19)) ([3e60bc5](https://github.com/vyrwu/atelier/commit/3e60bc5776b4d04ed1a63c72bb97bb5198337ca0))
* **workspaces:** recap under name in M-s picker ([#21](https://github.com/vyrwu/atelier/issues/21)) ([78243ab](https://github.com/vyrwu/atelier/commit/78243abeac66efa4af3c0bf0fd2fbe3253760952))


### Bug fixes

* **claude:** Resume respawned sessions over stale prompt ([#20](https://github.com/vyrwu/atelier/issues/20)) ([26406e9](https://github.com/vyrwu/atelier/commit/26406e9b1b03888ee38b47a65fa3ab7a72e921f1))
* **workspaces:** stop flaky ai.prompt loss in e2e ([#23](https://github.com/vyrwu/atelier/issues/23)) ([e919576](https://github.com/vyrwu/atelier/commit/e9195764422f9966cb9f934ad4ba8d816c8203e8))

## [0.2.1](https://github.com/vyrwu/atelier/compare/v0.2.0...v0.2.1) (2026-07-08)


### Bug fixes

* **workspaces:** handle branch-exists gracefully in creator ([#11](https://github.com/vyrwu/atelier/issues/11)) ([00b50a7](https://github.com/vyrwu/atelier/commit/00b50a7455358b6cff4564b54b40a44a0e92576b))
* **workspaces:** make Claude session restore survive delete + recover ([#9](https://github.com/vyrwu/atelier/issues/9)) ([1fd2ffb](https://github.com/vyrwu/atelier/commit/1fd2ffb87968b2ead6b1fc803a80b69bc2ffda65))


### Performance

* **workspaces:** speed up M-s picker + add loading box ([#10](https://github.com/vyrwu/atelier/issues/10)) ([024cc19](https://github.com/vyrwu/atelier/commit/024cc199dfadb7e782cc4bcb5580f6424d67df12))


### Refactors

* **workspaces:** move PR badge after attention icon ([#15](https://github.com/vyrwu/atelier/issues/15)) ([1ea47bf](https://github.com/vyrwu/atelier/commit/1ea47bf797d8ef6dc8d475a7537219a4a508eb40))

## [0.2.0](https://github.com/vyrwu/atelier/compare/v0.1.0...v0.2.0) (2026-07-08)


### Features

* **ghpr:** per-workspace GitHub PR status badge + open ([#13](https://github.com/vyrwu/atelier/issues/13)) ([9acbde9](https://github.com/vyrwu/atelier/commit/9acbde9319beb414dee99f26cd1bbbbb72dd2387))


### Bug fixes

* **release:** remove release-as pin so version bumps ([#6](https://github.com/vyrwu/atelier/issues/6)) ([71b0ef3](https://github.com/vyrwu/atelier/commit/71b0ef3bd4b662a380c89753a2404c7a4775c9db))
* **statusline:** show only the current workspace in the bar ([#12](https://github.com/vyrwu/atelier/issues/12)) ([e15c2ef](https://github.com/vyrwu/atelier/commit/e15c2efc81eee3a7ba15f02ce629af4a091661eb))
* **workspaces:** dim workspace selector highlight ([#2](https://github.com/vyrwu/atelier/issues/2)) ([7f98214](https://github.com/vyrwu/atelier/commit/7f982148e1692022b0a551011e807058d256b97f))
* **workspaces:** render build spinner over Claude popup ([#5](https://github.com/vyrwu/atelier/issues/5)) ([4c00214](https://github.com/vyrwu/atelier/commit/4c00214a237fe3b6da2e428b28ce4e3e00d487f7))
* **workspaces:** switch instead of detach on active delete ([#8](https://github.com/vyrwu/atelier/issues/8)) ([596d3ff](https://github.com/vyrwu/atelier/commit/596d3ff3ebdb7dc3eb050fb2bf0d59b2c2ee559a))


### Performance

* **logging:** add always-on operation timing to debug log ([#4](https://github.com/vyrwu/atelier/issues/4)) ([dc7877b](https://github.com/vyrwu/atelier/commit/dc7877ba0f016bf8eef6605f773c991970907a70))

## 0.1.0 (2026-07-07)


### Features

* **ccusage:** stack blocks/weekly/monthly with auto-refresh ([8884661](https://github.com/vyrwu/atelier/commit/88846619739e4d6bc183eb03724e4c59e2e7a029))
* **k8s:** M-c reopens the context picker ([9a21f93](https://github.com/vyrwu/atelier/commit/9a21f934db39aa3e3b112426705f33fa6829c3c5))
* **server:** detach-by-default exit + atelier server kill/gc ([cb8aa77](https://github.com/vyrwu/atelier/commit/cb8aa775ef65410080748a7cd65ed179c4ba51af))
* **tools:** add gh-dash, gh-enhance, ccusage ([e8dc201](https://github.com/vyrwu/atelier/commit/e8dc2010dd6804c385a1193e477d65882ef3c8b2))
* **toolselector:** add Recover Workspace; rename Kubernetes → K9s ([6d99b95](https://github.com/vyrwu/atelier/commit/6d99b95e31bbdc8097ea7123e4c1f598f37705b0))
* **toolselector:** M-n/M-s/M-r swap sibling workspace pickers ([9b684e3](https://github.com/vyrwu/atelier/commit/9b684e3b03d6f4db0efeef39ead5412bf5247eaf))
* **workspaces:** M-l List Workspaces picker ([83ba073](https://github.com/vyrwu/atelier/commit/83ba0739427e9d993fed39dd7f3acb3ba9a06076))
* **workspaces:** M-r badges live workspaces with green ● live ([69772a7](https://github.com/vyrwu/atelier/commit/69772a73d2f28facdc10d1241cdde0e5da183397))
* **workspaces:** M-s M-x is a SOFT close — worktree stays on disk ([dc44cdf](https://github.com/vyrwu/atelier/commit/dc44cdf6d7d45bf8f108da2c534c22e5dc9f5e58))
* **workspaces:** rank soft-closed worktrees at top of M-r picker ([b0fef34](https://github.com/vyrwu/atelier/commit/b0fef3473211dca923e4521944cb03a64b5ad9cd))
* **workspaces:** rename to Recover Workspace (M-r) + delete orphans ([ee05ef8](https://github.com/vyrwu/atelier/commit/ee05ef84512b1528eb81b1b2a358412b3baaf2d7))
* **workspaces:** track remote branch when name matches ([51d68b8](https://github.com/vyrwu/atelier/commit/51d68b80e28338b440c21157d96e24937435c2da))


### Bug fixes

* **ccusage:** icon 金, loading hint before npx cold start ([8174dad](https://github.com/vyrwu/atelier/commit/8174dad7bac5ea558c8f275b8e165f809abe8059))
* **claudegen:** hard-disable tools; treat URLs as opaque in naming ([0505055](https://github.com/vyrwu/atelier/commit/050505598160cb9b27840d1f342aba0c9d0d67ba))
* **claudesettings:** also wire Notification hook to notify-attention ([165642c](https://github.com/vyrwu/atelier/commit/165642c10b9330cb097fd40ceb72cc9dbf4d7176))
* **k8s:** context picker renders in a small popup; K9s TUI is a ([b7960a7](https://github.com/vyrwu/atelier/commit/b7960a783321479b0d4d33314ffb8410a1af33db))
* **k8s:** M-c from inside K9s popup no longer spawns a duplicate ([f86acf7](https://github.com/vyrwu/atelier/commit/f86acf7e398176c3ac18c9298dfa0ec9a7c1f465))
* **k8s:** queue full K9s popup against the outer client ([9b8e77d](https://github.com/vyrwu/atelier/commit/9b8e77d645d49d5f2e2650ae23c3e273f62b12df))
* **k8s:** route K9s popup through OpenOnOuter (handles inner detach) ([27d4ce2](https://github.com/vyrwu/atelier/commit/27d4ce239a8df2030c6da9394bc8d94b6d75302a))
* **pg:** resolve context picker after fzf strips ANSI ([4b1caac](https://github.com/vyrwu/atelier/commit/4b1caacfc184a1d1e6a0648dfa836a3ca6611d58))
* **popup:** apply canonical style to SessionGlobal on Ensure ([14ecb74](https://github.com/vyrwu/atelier/commit/14ecb74f498bdaef8575f2cf85c8cdd85dfa3981))
* **popup:** size new sessions to outer client; gh-dash renders full ([59842e1](https://github.com/vyrwu/atelier/commit/59842e16c1079e3226f5b3b7f6e810c80457e966))
* **server:** use detach-client -t, not -c ([94b1032](https://github.com/vyrwu/atelier/commit/94b1032198aaf72ff4b24436f941f98b80b7f758))
* **statusline:** inject only when window-status-format has #W ([763da10](https://github.com/vyrwu/atelier/commit/763da10322023719fade60885d1dfebf02aceec4))
* **tools:** GH Enchance title, less-R trap for popups, Make rebuild deps ([c83149f](https://github.com/vyrwu/atelier/commit/c83149f63de2796d17c55df045dd12dfbabe0876))
* **workspace:** LandOuter re-stamps [@atelier](https://github.com/atelier)_outer_* after switch ([e0ad2c6](https://github.com/vyrwu/atelier/commit/e0ad2c6686eb27eeeade0aa7f77efd33c385ca1e))
* **workspaces:** branch-name gen uses haiku + truncates prompt ([4194d9d](https://github.com/vyrwu/atelier/commit/4194d9df13b76017fd7f5bd973ef79ac8921d749))
* **workspaces:** brighten M-s picker's selected-row background ([de72f24](https://github.com/vyrwu/atelier/commit/de72f2498cdb5b977b3e3a679e7a32e9880808f0))
* **workspaces:** drop one of the two spaces before sessions-picker recap ([7473b8e](https://github.com/vyrwu/atelier/commit/7473b8efefb9764bf0b64f65b8d5d6c9562d4b44))
* **workspaces:** harden naming prompts; sonnet for branch/session inference ([6f769d6](https://github.com/vyrwu/atelier/commit/6f769d6399bdf43b590c2dc3fa78a8dc4e486a85))
* **workspaces:** M-r badges render on the right, not the left ([450c40c](https://github.com/vyrwu/atelier/commit/450c40c7b4b7eb592627eb657e6658405b5d8a06))
* **workspaces:** pin claude popup cwd to new worktree ([7202260](https://github.com/vyrwu/atelier/commit/720226014a69eebff8c10170717a67147589d6d3))
* **workspaces:** preserve picker when M-x deletes the current workspace ([2ba01c1](https://github.com/vyrwu/atelier/commit/2ba01c13c5d9db0bd2e4ab401d210fce9884660f))
* **workspaces:** queue claude popup before LandOuter ([1632eb7](https://github.com/vyrwu/atelier/commit/1632eb78694ff14161aa3f5d8dcb6a0c46e6c266))
* **workspaces:** recover lands shell IN the worktree ([73b9c3a](https://github.com/vyrwu/atelier/commit/73b9c3a6e06f3d8a091bf5eaf8dea36b293406d3))
* **workspaces:** recover queues claude resume popup ([84fd7b4](https://github.com/vyrwu/atelier/commit/84fd7b42ff634b9a80cb2b6e50e2b7a99e3995d4))
* **workspaces:** repair claude popup -E command formatting ([cb6469f](https://github.com/vyrwu/atelier/commit/cb6469f17e5070d296f92c6385ccdfced2d18e8e))
* **workspaces:** route _delete-row's outer hop through LandOuter ([1ad7787](https://github.com/vyrwu/atelier/commit/1ad7787c3cdcaa6b888d60512f6d74552efe5fc9))


### Refactors

* **workspaces:** defer build into spinner popup ([3e764ea](https://github.com/vyrwu/atelier/commit/3e764eac48d13282f4209a467b7de94cc48755a9))


### Docs

* refresh README + purge private planning docs ([97bcdb0](https://github.com/vyrwu/atelier/commit/97bcdb0133b56e2102e5226b459ff0d487dddebd))

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
