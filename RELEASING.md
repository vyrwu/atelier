# Releasing atelier

Atelier's releases are fully automated. You never run `git tag` by hand.
This document is the operating manual.

## The model

Three pieces work together:

1. **release-please** watches `main`. For every commit it parses against
   [Conventional Commits](https://www.conventionalcommits.org/) (`feat: …`,
   `fix: …`, `BREAKING CHANGE: …`) and maintains a single open "Release PR"
   that bumps the version in `.release-please-manifest.json` and updates
   `CHANGELOG.md`.
2. **You** review and merge the Release PR when you're ready to ship. That
   IS the release gesture.
3. **goreleaser** fires automatically on the tag release-please pushes,
   builds the `atelier` binary for 4 platforms (linux/darwin × amd64/arm64),
   publishes a GitHub Release, and pushes the cask `*.rb` file to
   `vyrwu/homebrew-tap`.

End-to-end: a normal merge to main → release-please updates the Release PR →
you merge the Release PR → ~5 minutes later, `brew upgrade atelier` works.

## Day-to-day: shipping a release

```bash
# 1. Normal feature work — Conventional Commits matter here.
git checkout -b PLA-123-add-foo
# … work …
git commit -m "feat(workspaces): [PLA-123] add foo"
git push -u origin PLA-123-add-foo
# Open PR, merge to main as usual.

# 2. Watch the release-please workflow update the Release PR.
gh pr list --label "autorelease: pending"
# (Title looks like: "chore(main): release 0.2.0")

# 3. Review the Release PR's CHANGELOG diff. Anything wrong? Land more
#    commits to main; the Release PR auto-updates.

# 4. Merge the Release PR. release-please tags `v0.2.0` and the release
#    workflow takes over.

# 5. Smoke-test the bundled launcher AND the embedding API:
brew update
brew upgrade atelier
atelier version              # should report v0.2.0
atelier doctor               # tmux + every plugin OK
atelier status freshness '0' '0' '' '1729094400' '/fake/repo'
                             # should print " #[fg=green]✔#[default]"
atelier status attention count
                             # should print "" or " #[fg=yellow]⏺ N#[default]"
```

If both emitters work, the public embedding API is intact. If
`atelier` (bundled launcher) opens a working tmux session and `M-q`
quits cleanly, the bundled distribution is intact.

That's it. No `git tag`, no `git push --tags`, no manual CHANGELOG edits.

## Commit format that drives version bumps

release-please reads conventional-commit prefixes:

| Prefix | Bump | Example |
|---|---|---|
| `fix:` / `fix(scope):` | patch (0.1.0 → 0.1.1) | `fix(workspaces): [PLA-101] handle empty repo_path` |
| `feat:` / `feat(scope):` | minor pre-v1.0 (0.1.0 → 0.2.0) | `feat(claude): [PLA-102] add resume-on-restore` |
| `feat!:` / `BREAKING CHANGE:` in body | major (0.1.0 → 1.0.0) | `feat(api)!: rename ToolManifest fields` |
| `docs:`, `chore:`, `ci:`, `test:`, `refactor:`, `build:`, `perf:` | no bump | shows up in CHANGELOG (or hidden, per `release-please-config.json`) |

The `[PLA-XXX]` Linear ID sits **after** the colon, in the description.
Linear picks it up from anywhere in the message; release-please's parser
ignores it. Both win.

Until `v1.0.0`, the config has `bump-minor-pre-major: true` — `feat:` bumps
the minor (0.x.0). Once you tag `v1.0.0` manually (or via a
`BREAKING CHANGE:` commit), bumps become standard semver.

## The first-ever release

The current `.release-please-manifest.json` says `"."": "0.0.0"`. When you
land the FIRST conventional-commit `feat:` on `main`, release-please opens
a Release PR proposing `v0.1.0`. Merge it; the rest takes over.

You can also seed the version manually via a commit like:

```
chore: release 0.1.0

Release-As: 0.1.0
```

The `Release-As: <version>` trailer is release-please's manual override.

## What lives where

| File | Owner | Purpose |
|---|---|---|
| `.github/workflows/release-please.yml` | you (rarely edited) | Runs the bot on every push to main. |
| `release-please-config.json` | you | What bumps a release, what shows in CHANGELOG, etc. |
| `.release-please-manifest.json` | the bot | Current version. Don't hand-edit. |
| `CHANGELOG.md` | the bot | Generated. Don't hand-edit; if you want to add context, do it in commit messages. |
| `.goreleaser.yaml` | you | Build matrix, archive naming, homebrew_casks block, GH Release header/footer. |
| `.github/workflows/release.yml` | you | Test → goreleaser. Fires on every `v*.*.*` tag. |
| `vyrwu/homebrew-tap` (separate repo) | the bot | Each release rewrites `Casks/atelier.rb`. |

## When something goes wrong

### "Release-Please PR has the wrong version"

Almost always a commit-message issue. If a `feat:` accidentally landed as
`chore:`, the bot won't bump minor. Fix forward: land a corrective commit
(`feat: [PLA-...] correct prior version-bump intent`) and the bot will
update the Release PR.

### "Release PR was merged but tag didn't fire"

Check `Actions → release-please`. Most common cause: the bot couldn't tag
because the GH token didn't have `contents: write`. Workflow already grants
it (`permissions:` block), so this should not happen unless the workflow
file was edited.

### "Goreleaser failed — casks step couldn't push"

The `HOMEBREW_TAP_GITHUB_TOKEN` secret expired or has wrong scopes. Regen
the PAT (see `RELEASING.md → "Setup: one-time prerequisites"` below),
update the secret, re-run the failed `release` workflow from the Actions tab.

### "Wrong version got tagged"

Don't try to delete-and-retag. Land a `fix:` (patch bump) or `feat!:`
(major bump) to ship a corrected version. Yanking is harder than rolling
forward.

### "Brew install gets an old version"

User-side cache. `brew update && brew upgrade atelier`. The tap repo is
the source of truth; check `github.com/vyrwu/homebrew-tap/Formula/`
matches the latest GH Release.

## Setup: one-time prerequisites

You did these once when setting this up; documented here so future-you
can repeat them if you ever reset everything.

1. **Create `github.com/vyrwu/homebrew-tap`** as a public, empty repo
   (just a README).
2. **Generate a fine-grained PAT** with `Contents: read/write` scoped to
   that tap repo. 1-year expiration.
3. **Add the PAT** as a secret named `HOMEBREW_TAP_GITHUB_TOKEN` on the
   atelier repo (`Settings → Secrets and variables → Actions`).

That's it for setup. After this, the loop runs on its own.

## Public API surface (what stays stable across releases)

Atelier ships two contracts that external users depend on. Breaking
either requires a major version bump (post-v1.0) or a `BREAKING
CHANGE:` commit:

1. **Statusline data emitters** (see [docs/EMBEDDING.md](docs/EMBEDDING.md)):
   - `atelier status freshness <behind> <ahead> <pull_error> <freshness_ts> <repo_path>`
   - `atelier status attention count`

   These are invoked from user tmux configs via `#(...)`. The arg
   shape, exit code, and output format are part of the contract.
   Locked in by `internal/cli/status_emitters_e2e_test.go`.

2. **Launcher config schema** (`[tools.<name>]` in config.toml):
   The fields a user's launcher block may set (`launch`, `popup`,
   `key`, `requires`, `icon`, `accent_color`, `title`, …). Renaming
   or removing one breaks user configs. (Built-in tools' `Manifest`
   is an in-tree Go type — changing it is a normal code change, not a
   public-contract break.)

3. **Tmux init output structure** (`atelier init` / `atelier init
   --bare`): users source these into their tmux.conf. Adding new
   blocks is fine; removing existing bindings/hooks isn't.

The e2e test `TestInit_SourcesCleanlyOnFreshTmux` guards init
output against parse errors on a fresh tmux server.

## What's intentionally NOT automated

- **CHANGELOG curation past auto-generation**: release-please writes
  bullet points from your commit subjects. The output quality is exactly
  as good as your commit subjects. Don't try to post-process this in CI.
- **Brew cask hand-editing**: goreleaser regenerates the Ruby file
  on every release. Any edits in the tap repo get overwritten next tag.
  If you need to customize cask behavior, do it in
  `.goreleaser.yaml`'s `homebrew_casks:` block.
- **Tagging the first commit**: the manifest starts at `0.0.0`. The
  bot won't open a Release PR until there's a `feat:` or `fix:` commit
  to bump from. If you need to "seed" a version, use `Release-As: x.y.z`
  in a single commit (see "The first-ever release").
