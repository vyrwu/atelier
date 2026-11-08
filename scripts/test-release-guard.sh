#!/usr/bin/env bash
# Real local git histories exercise the publication boundary without GitHub.
set -euo pipefail
guard="$(git rev-parse --show-toplevel)/scripts/release-guard.sh"
fixture="$(mktemp -d "${TMPDIR:-/tmp}/atelier-release-guard.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=Test GIT_COMMITTER_NAME=Test
export GIT_AUTHOR_EMAIL=test@example.invalid GIT_COMMITTER_EMAIL=test@example.invalid
git init --bare -q "$fixture/origin.git"
git init -q -b main "$fixture/repo"
cd "$fixture/repo"
git remote add origin "$fixture/origin.git"
git commit -q --allow-empty -m initial
git tag v1.9.0
git commit -q --allow-empty -m next
git tag -a v1.10.0 -m annotated
git push -q origin main --tags
revision="$(git rev-parse HEAD)"
export GITHUB_OUTPUT="$fixture/output"
"$guard" v1.10.0 "$revision"
grep -Fx "revision=$revision" "$GITHUB_OUTPUT"
reject() {
  if "$guard" "$@" > "$fixture/rejected.log" 2>&1; then
    echo "Unexpectedly accepted release: $*" >&2
    exit 1
  fi
}
reject v1.9.0
reject main
reject v01.10.0
reject 'v1.10.0; echo injected'
reject v1.11.0
reject v1.10.0 "$(git rev-parse HEAD^)"
# A high version outside main must neither publish nor block main's release.
git switch -q -c unreviewed
git commit -q --allow-empty -m unreviewed
git tag v99.0.0
git push -q origin unreviewed --tags
reject v99.0.0
git switch -q main
"$guard" v1.10.0 "$revision"
# A new release makes reruns of the old release ineligible.
git commit -q --allow-empty -m newer
git tag v1.11.0
git push -q origin main --tags
reject v1.10.0
"$guard" v1.11.0 "$(git rev-parse HEAD)"
# Fetch --tags alone retains a deleted remote tag locally.
git push -q origin :refs/tags/v1.11.0
reject v1.11.0
echo 'Release guards reject stale, unreviewed, moved, deleted and invalid targets.'
