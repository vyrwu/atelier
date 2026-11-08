#!/usr/bin/env bash
# Verify against the committed key before any provenance or tap update is made.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
version="$(jq -r .version dist/metadata.json)"
manifest="atelier_${version}_checksums.txt"
GNUPGHOME="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/verify.XXXXXX")"
export GNUPGHOME
trap 'rm -rf "$GNUPGHOME"' EXIT
gpg --batch --dearmor --output "$GNUPGHOME/trusted.gpg" packaging/keys/atelier.asc
gpgv --keyring "$GNUPGHOME/trusted.gpg" "dist/$manifest.sig" "dist/$manifest"
# GoReleaser hashes external public keys without copying them into dist.
cp packaging/keys/atelier.asc packaging/keys/atelier.rsa.pub dist/
(cd dist && shasum -a 256 -c "$manifest")
