#!/usr/bin/env bash
# Runs a command, usually goreleaser, with atelier's release signing keys. The keys
# come from ATELIER_SIGNING_GPG_KEY, ATELIER_SIGNING_GPG_PASSPHRASE and
# ATELIER_SIGNING_APK_KEY (the "release" environment's secrets, in CI) and must
# match the public keys in packaging/keys.
#
#   with-keys.sh --check            check the keys, passphrase included
#   with-keys.sh COMMAND [ARGS...]  check the keys, then run COMMAND with them
#
# Both import the OpenPGP key into a throwaway GNUPGHOME and make a test
# signature, so a wrong passphrase fails here and not halfway through a release.
# COMMAND gets ATELIER_SIGNING_GPG_KEY_FILE, ATELIER_SIGNING_APK_KEY_FILE,
# ATELIER_SIGNING_GPG_FINGERPRINT, NFPM_ATELIER_PASSPHRASE and that GNUPGHOME, which
# .goreleaser.yaml signs with. The key files are 0600 in a 0700 directory under
# RUNNER_TEMP (or TMPDIR), removed when COMMAND exits. Everything COMMAND runs
# can read them, so give it only goreleaser, never repo or dependency code.
set -euo pipefail

apk_key_name="atelier"

root="$(cd "$(dirname "$0")/../.." && pwd)"
gpg_pub="${root}/packaging/keys/atelier.asc"
apk_pub="${root}/packaging/keys/${apk_key_name}.rsa.pub"

# say error|warning MESSAGE: an annotation in GitHub Actions, plain elsewhere.
say() {
  local level="$1"
  shift
  if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
    echo "::${level}::$*" >&2
  else
    echo "with-keys.sh: $*" >&2
  fi
}
die() {
  say error "$@"
  exit 1
}

if [[ "$#" -eq 0 ]]; then
  echo "Usage: packaging/signing/with-keys.sh --check | COMMAND [ARGS...]" >&2
  exit 2
fi

missing=""
for name in ATELIER_SIGNING_GPG_KEY ATELIER_SIGNING_GPG_PASSPHRASE ATELIER_SIGNING_APK_KEY; do
  [[ -n "${!name:-}" ]] || missing="${missing} ${name}"
done
[[ -z "${missing}" ]] ||
  die "Missing${missing}. atelier never ships unsigned: run packaging/signing/new-keys.sh to add the keys to the \"release\" environment."
[[ -f "${gpg_pub}" && -f "${apk_pub}" ]] ||
  die "packaging/keys has no public keys. Run packaging/signing/new-keys.sh and commit the keys it writes."

umask_before="$(umask)"
umask 077
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/sign.XXXXXX")"
export GNUPGHOME="${work}/gnupg"
cleanup() {
  gpgconf --kill all >/dev/null 2>&1 || true
  rm -rf "${work}"
}
trap cleanup EXIT
mkdir "${GNUPGHOME}"
# Never cache the passphrase: every signature reads it from NFPM_ATELIER_PASSPHRASE.
printf 'default-cache-ttl 0\nmax-cache-ttl 0\n' >"${GNUPGHOME}/gpg-agent.conf"

# Fingerprints and public keys are compared through pipes. gpg exits non-zero
# listing a secret key with no agent running, so only what it lists counts.
show_keys() {
  gpg --batch --no-autostart --with-colons --show-keys "$@" 2>/dev/null || true
}
# fingerprint sec|pub [FILE]: the first secret or public key's fingerprint.
fingerprint() {
  local kind="$1"
  shift
  show_keys "$@" | awk -F: -v kind="${kind}" '$1 == kind { k = 1 } k && $1 == "fpr" && !n++ { print $10 }'
}
apk_public_key() {
  openssl pkey "$@" -pubout -outform DER 2>/dev/null | openssl dgst -sha256
}

fpr="$(printf '%s\n' "${ATELIER_SIGNING_GPG_KEY}" | fingerprint sec)" || fpr=""
[[ -n "${fpr}" ]] || die "ATELIER_SIGNING_GPG_KEY isn't an OpenPGP secret key."
[[ "${fpr}" == "$(fingerprint pub "${gpg_pub}")" ]] ||
  die "ATELIER_SIGNING_GPG_KEY doesn't match packaging/keys/atelier.asc. Commit the public keys new-keys.sh wrote with these secrets."
apk_key="$(printf '%s\n' "${ATELIER_SIGNING_APK_KEY}" | apk_public_key)" ||
  die "ATELIER_SIGNING_APK_KEY isn't an RSA private key."
[[ "${apk_key}" == "$(apk_public_key -pubin -in "${apk_pub}")" ]] ||
  die "ATELIER_SIGNING_APK_KEY doesn't match packaging/keys/${apk_key_name}.rsa.pub. Commit the public keys new-keys.sh wrote with these secrets."

expires="$(show_keys "${gpg_pub}" | awk -F: '$1 == "pub" && !n++ { print $7 }')"
if [[ -n "${expires}" && "${expires}" -lt "$(($(date +%s) + 60 * 86400))" ]]; then
  say warning "atelier's signing key expires within 60 days. Rotate it with packaging/signing/new-keys.sh --rotate."
fi

printf '%s\n' "${ATELIER_SIGNING_GPG_KEY}" | gpg --batch --quiet --import ||
  die "gpg couldn't import ATELIER_SIGNING_GPG_KEY."
printf '%s' "${ATELIER_SIGNING_GPG_PASSPHRASE}" |
  gpg --batch --quiet --pinentry-mode loopback --passphrase-fd 0 --local-user "${fpr}" \
    --detach-sign --output - "${gpg_pub}" >/dev/null ||
  die "ATELIER_SIGNING_GPG_PASSPHRASE doesn't unlock ATELIER_SIGNING_GPG_KEY."

if [[ "$1" == --check ]]; then
  echo "The signing keys match packaging/keys and the passphrase unlocks them (OpenPGP key ${fpr})."
  exit 0
fi

printf '%s\n' "${ATELIER_SIGNING_GPG_KEY}" >"${work}/gpg.key"
printf '%s\n' "${ATELIER_SIGNING_APK_KEY}" >"${work}/apk.key"

export ATELIER_SIGNING_GPG_KEY_FILE="${work}/gpg.key"
export ATELIER_SIGNING_APK_KEY_FILE="${work}/apk.key"
export ATELIER_SIGNING_GPG_FINGERPRINT="${fpr}"
export NFPM_ATELIER_PASSPHRASE="${ATELIER_SIGNING_GPG_PASSPHRASE}"
unset ATELIER_SIGNING_GPG_KEY ATELIER_SIGNING_GPG_PASSPHRASE ATELIER_SIGNING_APK_KEY
# What COMMAND writes (packages, their contents) gets the caller's usual modes.
umask "${umask_before}"
"$@"
