#!/usr/bin/env bash
# shellcheck disable=SC2016 # The backticks are Markdown, not command substitution.
# The fingerprints of the public keys in packaging/keys, as README.md and
# docs/release-security.md publish them for people to check a downloaded key against.
#
#   fingerprints.sh          write them into README.md and docs/release-security.md
#   fingerprints.sh --check  fail unless packaging/keys holds an OpenPGP and an
#                            RSA public key, and both files list exactly those
#
# new-keys.sh runs it after writing new keys; CI runs it with --check.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
gpg_pub="${root}/packaging/keys/atelier.asc"
apk_pub="${root}/packaging/keys/atelier.rsa.pub"

die() {
  echo "fingerprints.sh: $*" >&2
  exit 1
}

case "$#:${1:-}" in
  0:) check=false ;;
  1:--check) check=true ;;
  *)
    echo "Usage: packaging/signing/fingerprints.sh [--check]" >&2
    exit 2
    ;;
esac

[[ -f "${gpg_pub}" && -f "${apk_pub}" ]] ||
  die "packaging/keys has no public keys. Run packaging/signing/new-keys.sh and commit what it writes."

GNUPGHOME="$(mktemp -d)"
export GNUPGHOME
trap 'rm -rf "${GNUPGHOME}"' EXIT
fpr="$(gpg --batch --with-colons --show-keys "${gpg_pub}" 2>/dev/null |
  awk -F: '$1 == "pub" { k = 1 } k && $1 == "fpr" && !n++ { print $10 }')" || fpr=""
[[ -n "${fpr}" ]] || die "packaging/keys/atelier.asc isn't an OpenPGP public key."
openssl rsa -pubin -in "${apk_pub}" -noout 2>/dev/null ||
  die "packaging/keys/atelier.rsa.pub isn't an RSA public key."
sha="$(openssl dgst -sha256 -r "${apk_pub}" | cut -d' ' -f1)"

gpg_prefix='| `atelier.asc` |'
apk_prefix='| `atelier.rsa.pub` |'
gpg_row="${gpg_prefix}"' `.rpm`, `.deb`, `checksums.txt` | `'"${fpr}"'` |'
apk_row="${apk_prefix}"' `.apk` | SHA-256 `'"${sha}"'` |'

# lists DOC PREFIX ROW: DOC has one line with PREFIX, and it is ROW.
lists() {
  [[ "$(grep -cF "$2" "$1")" == 1 ]] && grep -qxF "$3" "$1"
}

for doc in README.md docs/release-security.md; do
  if [[ "${check}" == true ]]; then
    if ! lists "${root}/${doc}" "${gpg_prefix}" "${gpg_row}" || ! lists "${root}/${doc}" "${apk_prefix}" "${apk_row}"; then
      die "${doc} doesn't list the keys in packaging/keys. Run packaging/signing/fingerprints.sh and commit it."
    fi
  else
    awk -v g="${gpg_row}" -v a="${apk_row}" -v gp="${gpg_prefix}" -v ap="${apk_prefix}" '
      index($0, gp) == 1 { $0 = g; n++ }
      index($0, ap) == 1 { $0 = a; n++ }
      { print }
      END { exit n == 2 ? 0 : 1 }
    ' "${root}/${doc}" >"${GNUPGHOME}/doc" ||
      die "${doc} needs one table row for each key, starting \"${gpg_prefix}\" and \"${apk_prefix}\"."
    cat "${GNUPGHOME}/doc" >"${root}/${doc}"
  fi
done

if [[ "${check}" == true ]]; then
  echo "README.md and docs/release-security.md list the keys in packaging/keys (OpenPGP ${fpr})."
fi
