#!/usr/bin/env bash
# Generates the keys that sign atelier's releases. The private keys and passphrase
# go straight into the GitHub "release" environment as secrets; only the public
# keys are written, to packaging/keys, with their fingerprints in README.md and
# docs/release-security.md. Run it once, and with --rotate to replace the keys. It refuses
# unless protected tags from reviewed main are the only deployment refs. See
# "One-time repository setup" in docs/release-security.md.
set -euo pipefail

repo="vyrwu/atelier"
environment="release"
uid="Atelier releases <releases@users.noreply.github.com>"
apk_key_name="atelier"

root="$(cd "$(dirname "$0")/../.." && pwd)"
keys="${root}/packaging/keys"

usage() {
  cat <<EOF
Usage: packaging/signing/new-keys.sh [--rotate]

Generates the OpenPGP key that signs atelier's .rpm, .deb and checksums.txt and
the RSA key that signs its .apk. Stores the private keys and the passphrase as
secrets in the "${environment}" environment of ${repo}, writes the public keys
to packaging/keys and their fingerprints to README.md and docs/release-security.md. Nothing
secret is printed or left on disk.

Refuses unless only protected v* tags can deploy to "${environment}", with no admin bypass;
only the main-only release App can create those tags; main needs a review
after the last push; and GitHub Actions cannot approve pull requests.

  --rotate  Replace the keys in packaging/keys and the secrets
EOF
}

die() {
  echo "new-keys.sh: $*" >&2
  exit 1
}

rotate=false
case "$#:${1:-}" in
  0:) ;;
  1:--rotate) rotate=true ;;
  1:-h | 1:--help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

for tool in gpg gpgconf openssl gh; do
  command -v "${tool}" >/dev/null || die "${tool} isn't installed."
done

if [[ "${rotate}" == false && (-e "${keys}/atelier.asc" || -e "${keys}/${apk_key_name}.rsa.pub") ]]; then
  die "packaging/keys already has public keys. Run it with --rotate to replace them."
fi

gh auth status --hostname github.com >/dev/null 2>&1 ||
  die 'You are not signed in to GitHub. Run "gh auth login" first.'
gh api "repos/${repo}/environments/${environment}" >/dev/null 2>&1 ||
  die "Can't find the \"${environment}\" environment of ${repo}. Setting its secrets needs admin access to the repo."

# The tag-only environment is safe only when reviewed main is the sole source
# of release tags. This also checks that the tag-creating App key is main-only.
"${root}/packaging/signing/check-protections.sh"

umask 077
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/keys.XXXXXX")"
export GNUPGHOME="${work}/gnupg"
cleanup() {
  gpgconf --kill all >/dev/null 2>&1 || true
  rm -rf "${work}"
}
trap cleanup EXIT
mkdir "${GNUPGHOME}"

# RSA for the widest rpm, dpkg and gpg support; a signing-only primary key, so
# packages, signatures and debsig policies all name the same key ID.
openssl rand -base64 32 | tr -d '\n' >"${work}/passphrase"
gpg --batch --quiet --pinentry-mode loopback --passphrase-file "${work}/passphrase" \
  --quick-generate-key "${uid}" rsa4096 sign 2y ||
  die "gpg couldn't generate the OpenPGP key."
fingerprint="$(gpg --batch --quiet --with-colons --list-secret-keys | awk -F: '$1 == "fpr" && !n++ { print $10 }')"
gpg --batch --armor --export "${fingerprint}" >"${work}/atelier.asc"
gpg --batch --pinentry-mode loopback --passphrase-file "${work}/passphrase" \
  --armor --export-secret-keys "${fingerprint}" >"${work}/atelier.key"

# apk only verifies RSA signatures.
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:4096 -out "${work}/apk.key" 2>/dev/null ||
  die "openssl couldn't generate the apk key."
openssl pkey -in "${work}/apk.key" -pubout -out "${work}/apk.rsa.pub"

# gh reads each secret from stdin, so it never shows up in argv or the terminal.
gh secret set ATELIER_SIGNING_GPG_KEY --env "${environment}" --repo "${repo}" <"${work}/atelier.key"
gh secret set ATELIER_SIGNING_GPG_PASSPHRASE --env "${environment}" --repo "${repo}" <"${work}/passphrase"
gh secret set ATELIER_SIGNING_APK_KEY --env "${environment}" --repo "${repo}" <"${work}/apk.key"

install -d -m 755 "${keys}"
install -m 644 "${work}/atelier.asc" "${keys}/atelier.asc"
install -m 644 "${work}/apk.rsa.pub" "${keys}/${apk_key_name}.rsa.pub"
"${root}/packaging/signing/fingerprints.sh"

cat <<EOF

Stored ATELIER_SIGNING_GPG_KEY, ATELIER_SIGNING_GPG_PASSPHRASE and ATELIER_SIGNING_APK_KEY
in the "${environment}" environment of ${repo}, wrote the public keys and put
their fingerprints in README.md and docs/release-security.md:

  packaging/keys/atelier.asc
  packaging/keys/${apk_key_name}.rsa.pub

OpenPGP key fingerprint: ${fingerprint}
It expires in two years; rotate it before then.

Commit packaging/keys, README.md and docs/release-security.md and merge them. Releases fail
until the public keys on main match these secrets.
EOF
