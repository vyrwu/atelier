# atelier demo / scenario sandbox

A fully isolated, **ephemeral** atelier for demos and manual scenario
testing. Everything is **real** — real git repos, real worktrees, real
seeded workspace state (attention, recap, freshness, forge PR badges),
the real single-binary kernel — with no live agent process. It launches
in a throwaway temp dir on its own tmux socket and garbage-collects
itself (temp dir + server) on exit, so it never touches your real atelier
server, dotfiles, repos, or git identity.

## Quick start

```bash
make sandbox        # bundled launcher   (tmux -L atelier-sandbox)
make sandbox-tmux   # plugin / embed way (tmux -L atelier-sandbox-plugin)
make sandbox-keep   # bundled, but keep the temp dir + server on exit
```

Detach (`M-q`) or quit (`C-d`) and the sandbox tears itself down. Nothing
persists — relaunch for a fresh instance.

## What "isolated + ephemeral" means

The launcher (`./sandbox`, run via the Makefile) creates one temp dir
(`$TMPDIR/atelier-sandbox-*`) and redirects everything atelier reads into
it, then removes it on exit:

| Concern | Real setup | Sandbox |
|---|---|---|
| tmux socket | `-L atelier` | `-L atelier-sandbox` / `-L atelier-sandbox-plugin` |
| config | `~/.config/atelier` | `<tmp>/config` (`XDG_CONFIG_HOME`) |
| cache / state / logs | `~/.cache/atelier` | `<tmp>/cache` (`XDG_CACHE_HOME`) |
| binary | your `$PATH` | `<tmp>/bin/atelier` (symlink to your fresh `make build`) |
| git identity | `~/.gitconfig` | `<tmp>/gitconfig` (`GIT_CONFIG_GLOBAL`) |
| repos / worktrees | `~/code` | `<tmp>/code` (`ATELIER_CODE_ROOT`) |

The sandbox `config.toml` wires the kernel integration ports:

- `ai = "claude"` (default) → **`M-n` creates a workspace with the real
  Claude Code agent** (branch name **and tag both suggested by Claude**,
  real agent popup). The 10 seeded workspaces meanwhile carry **injected
  summaries + attention + workspace age + tags (`M-t`)** (statestore data,
  no live agent) to simulate a busy environment you switch between — the
  `M-s` picker sorts attention → tag → forge, so a Claude-suggested tag
  reusing the existing vocabulary lands the new workspace next to its
  siblings. Pass `--ai mock` (or edit config) for a no-auth/offline run —
  atelier's deterministic mock agent (which suggests a tag too).
- `forge = "mock"` → the per-workspace **PR badge** is classified by
  atelier's own mock forge adapter from a deterministic fixture
  (`mock-forge.json`, cwd→state), so the kernel's real badge refresh +
  rendering run offline — no `gh`, nothing suppressed.
- **lazygit (`M-; → Lazygit`, or `M-g`)** → a `[tools.lazygit]` config
  launcher, per-workspace, opens in the worktree. Requires `lazygit`.
- **k9s (`M-; → K9s`)** → wired to your real kube cluster. The sandbox
  seeds a `k8s/contexts.yaml` pointing at your kubeconfig's
  **current-context** (e.g. a kind cluster); its `initCmd` copies your
  real kubeconfig into the sandbox's `KUBECONFIG` on first open (original
  untouched). Requires `k9s` + a running cluster on the machine — the one
  piece that isn't sandboxed (intended: it shows your real kind cluster).
  No kubeconfig found → the K9s tool is simply inert.

## The seeded scenario — `acme-platform`

Ten real DevOps repos, several with multiple worktrees, every workspace
window carrying an agent recap. Real git throughout (real commits, a local
bare origin so `git fetch` freshness works, genuine ahead/behind
divergence, real uncommitted edits). Only the tmux-side state (attention
flag, recap, forge PR state) is pre-seeded — as real atelier persistence
(statestore), pointing at the real worktrees.

| Repo | Worktrees | Notable state |
|---|---|---|
| `helm-charts` | `feat/bump-ingress-nginx`, `feat/redis-pdb` | ⏺ + open PR on the ingress bump (dirty); draft PR on the PDB |
| `terraform-infra` | `main` (default), `feat/eks-1-30-upgrade` | genuinely **2 ahead / 1 behind**; ⏺ + open PR on the upgrade |
| `gitops-argocd` | `chore/sync-wave-annotations`, `fix/drift-detection` | merged PR + ⏺ open PR |
| `platform-scripts` | `main`, `fix/ci-cache-key` | `fix/ci-cache-key` is **soft-closed** — recover with `M-r` |
| `k8s-manifests` | `feat/kustomize-overlays` | closed PR |
| `docker-images` | `chore/base-image-cve` | ⏺ + open PR; **1 behind** (security base bump) |
| `ansible-playbooks` | `feat/node-hardening` | plain (no PR) |
| `observability-stack` | `feat/slo-alerts`, `chore/grafana-dashboards` | ⏺ draft PR + merged PR |
| `service-catalog` | `main` | landing workspace, plain |
| `ci-pipelines` | `fix/flaky-e2e` | ⏺ + open PR |

Icons in the `M-s` picker: `⏺` = attention, `❯` = the workspace you're on,
plus a colored forge PR badge (green=open, grey=draft, purple=merged,
red=closed) between the icon and the name, sorted open→draft→merged→closed.
Every row shows its recap. Landing session is `service-catalog` (no
attention), so the `⏺` badges are things you reveal via `M-s`.

**How the PR badges work offline:** `forge = "mock"` swaps in a
deterministic forge adapter that classifies each workspace by looking its
worktree path up in `mock-forge.json` (written by the seed). The kernel's
real on-open refresh runs and repopulates `@forge_state` from that fixture
— no `gh`, no network, nothing suppressed. The seed also stamps
`@forge_state` up front so the badge shows on the first picker open before
the async refresh. `M-o` (open PR) is a no-op in the mock.

Suggested beats: `M-s` scan the list — attention badges, PR states, and
recaps surface the work → switch into one (attention clears) → inspect a
dirty worktree via `M-;` → `M-n` create a workspace from a prompt (mock
agent, offline) → `M-r` recover the soft-closed `platform-scripts` worktree
→ `M-q` detach (tears down).

## Adding / customizing scenarios

Scenarios are **YAML specifications**, not code. The built-in one lives at
`internal/seed/scenarios/acme-platform.yaml` (embedded). Run your own:

```bash
go run ./sandbox --scenario /path/to/my-scenario.yaml
```

Schema (`internal/seed/scenario.go`): `repos` (`files`,
`originCommits`/`localCommits` for divergence, `worktrees` with `dirty`
edits + `softClosed`) and `workspaces` — one session per repo, each with
one or more `windows` carrying `attention`, `recap`, `lastSeen`/`recapAge`
(duration strings like `9m`), `pr` (open|draft|merged|closed), and
`metadata`. The `internal/seed` package is importable from e2e tests.

## Layout

```
sandbox/
  main.go        # launcher: temp dir → hydrate → launch → GC (package main, not shipped)
  plugin.conf    # embed-mode tmux config (go:embed) for `make sandbox-tmux`
  README.md
internal/seed/
  scenario.go               # scenario schema + YAML loading (Load/LoadFile/Builtin)
  hydrate.go                # Hydrate(root, scenario) → real repos/worktrees/state + config + isolation env
  scenarios/*.yaml          # bundled scenario specs (embedded)
  seed_test.go              # unit tests
  restore_e2e_test.go       # e2e: hydrate → atelier Restore → assert (build tag: e2e)
```
