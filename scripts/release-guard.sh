#!/usr/bin/env bash
# Resolve a stable release tag, refuse tags outside main and prevent old runs
# from overwriting newer releases. Run again immediately before publishing.
set -euo pipefail

tag="${1:?Usage: release-guard.sh vX.Y.Z [expected-commit]}"
if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "::error::Expected an explicit stable release tag (vX.Y.Z)."
  exit 1
fi

git fetch origin main --tags
revision="$(git rev-parse --verify "refs/tags/${tag}^{commit}")"
# Fetch keeps deleted local tags. Check the live remote ref as well, peeling
# annotated tags before comparing it with the commit CI will build.
remote_refs="$(git ls-remote --exit-code origin "refs/tags/${tag}" "refs/tags/${tag}^{}")"
remote_revision="$(printf '%s\n' "$remote_refs" | awk '
  /\^\{\}$/ { peeled = $1; next }
  { direct = $1 }
  END { print peeled ? peeled : direct }
')"
if [[ "$revision" != "$remote_revision" ]]; then
  echo "::error::The release tag changed on origin."
  exit 1
fi
git merge-base --is-ancestor "$revision" origin/main
if [[ -n "${2:-}" && "$revision" != "$2" ]]; then
  echo "::error::The tag moved after CI validated it."
  exit 1
fi
latest="$(git tag --merged origin/main --sort=-v:refname | sed -nE '/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/p' | sed -n '1p')"
if [[ "$tag" != "$latest" ]]; then
  echo "::error::${tag} is older than ${latest}; refusing to publish over it."
  exit 1
fi
printf 'tag=%s\nrevision=%s\n' "$tag" "$revision" >> "${GITHUB_OUTPUT:-/dev/stdout}"
