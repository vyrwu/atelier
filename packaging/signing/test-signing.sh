#!/usr/bin/env bash
# End-to-end signed snapshot with throwaway keys. No production secrets or
# GitHub mutations; use the real provisioning/wrapper scripts with a fake gh.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/sigtest.XXXXXX")"
cleanup() { rm -rf "$work"; }
trap cleanup EXIT
mkdir -p "$work/repo" "$work/bin" "$work/secrets"
chmod 700 "$work/secrets"
# Copy the working tree so local uncommitted packaging edits are tested too.
(cd "$root" && tar --exclude='./.git' --exclude='./dist' --exclude='./bin' -cf - .) |
  (cd "$work/repo" && tar -xf -)
cd "$work/repo"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=Test GIT_COMMITTER_NAME=Test
export GIT_AUTHOR_EMAIL=test@example.invalid GIT_COMMITTER_EMAIL=test@example.invalid
git init -q -b main
git add .
git commit -qm 'Test signed packaging'
git tag v0.0.0
git remote add origin https://github.com/vyrwu/atelier.git
# Test git fetches stay inside the fixture while retaining the real remote name.
git config "url.$work/repo/.insteadOf" https://github.com/vyrwu/atelier.git
export SIGNING_TEST_ROOT="$work"
cat > "$work/bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
# Normalize the one listing call whose pagination flag precedes the endpoint.
if [[ "${1:-}" == api && "${2:-}" == --paginate ]]; then shift 2; set -- api "$@"; fi
case "$1 ${2:-}" in
  'auth status') exit 0 ;;
  'secret set') cat > "$SIGNING_TEST_ROOT/secrets/$3"; exit 0 ;;
  'api '*)
    case "$2" in
      */deployment-branch-policies)
        if [[ "$2" == */release-automation/* ]]; then
          answer='{"branch_policies":[{"name":"main","type":"branch"}]}'
        else answer='{"branch_policies":[{"name":"v*","type":"tag"}]}'; fi ;;
      */environments/*/secrets) answer='{"secrets":[{"name":"RELEASE_APP_PRIVATE_KEY"}]}' ;;
      */environments/*)
        answer="{\"can_admins_bypass\":${SIGNING_TEST_INSECURE:-false},\"deployment_branch_policy\":{\"custom_branch_policies\":true}}" ;;
      */actions/permissions/workflow) answer='{"default_workflow_permissions":"read","can_approve_pull_request_reviews":false}' ;;
      */actions/secrets)
        answer='{"secrets":[]}'
        if [[ "${SIGNING_TEST_ATTACK:-}" == secret-page ]]; then
          answer+=$'\n{"secrets":[{"name":"RELEASE_APP_PRIVATE_KEY"}]}'
        fi ;;
      */rulesets/1)
        answer="$(cat .github/security-ruleset.json)"
        if [[ "${SIGNING_TEST_ATTACK:-}" == missing-checks ]]; then
          answer="$(jq '(.rules[] | select(.type == "required_status_checks").parameters.required_status_checks) = []' <<< "$answer")"
        fi ;;
      */rulesets/2)
        answer="$(cat .github/release-tag-ruleset.json)"
        if [[ "${SIGNING_TEST_ATTACK:-}" == tag-bypass ]]; then
          answer="$(jq '.bypass_actors += [{"actor_id":1,"actor_type":"Team","bypass_mode":"always"}]' <<< "$answer")"
        fi ;;
      */rulesets) answer='[{"name":"main: pull requests with green CI","id":1},{"name":"release tags: release App only","id":2}]' ;;
      repos/vyrwu/atelier) answer='{"owner":{"type":"User"}}' ;;
      *) echo "Unexpected gh API request: $2" >&2; exit 1 ;;
    esac
    if [[ "${3:-}" == --jq ]]; then jq -r "$4" <<< "$answer"; else echo "$answer"; fi ;;
  *) exit 1 ;;
esac
GH
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH"
# Absolute invocation must inspect the script's checkout, not the caller's cwd.
mkdir "$work/elsewhere"
(cd "$work/elsewhere" && "$work/repo/packaging/signing/check-protections.sh")
# Provisioning must fail before creating/storing keys when protections are weak.
if SIGNING_TEST_INSECURE=true packaging/signing/new-keys.sh --rotate > "$work/rejected.log" 2>&1; then
  echo 'Key provisioning accepted an environment with admin bypass.' >&2
  exit 1
fi
for attack in secret-page missing-checks tag-bypass; do
  if SIGNING_TEST_ATTACK="$attack" packaging/signing/new-keys.sh --rotate > "$work/rejected.log" 2>&1; then
    echo "Key provisioning accepted $attack." >&2; exit 1
  fi
done
test -z "$(ls -A "$work/secrets")"
packaging/signing/new-keys.sh --rotate
packaging/signing/fingerprints.sh --check
ATELIER_SIGNING_GPG_KEY="$(cat "$work/secrets/ATELIER_SIGNING_GPG_KEY")"
ATELIER_SIGNING_GPG_PASSPHRASE="$(cat "$work/secrets/ATELIER_SIGNING_GPG_PASSPHRASE")"
ATELIER_SIGNING_APK_KEY="$(cat "$work/secrets/ATELIER_SIGNING_APK_KEY")"
export ATELIER_SIGNING_GPG_KEY ATELIER_SIGNING_GPG_PASSPHRASE ATELIER_SIGNING_APK_KEY
rm -rf "$work/secrets"
if env -u ATELIER_SIGNING_GPG_KEY packaging/signing/with-keys.sh --check > "$work/rejected.log" 2>&1; then
  echo 'Signing accepted a missing private key.' >&2; exit 1
fi
if ATELIER_SIGNING_GPG_PASSPHRASE=wrong packaging/signing/with-keys.sh --check > "$work/rejected.log" 2>&1; then
  echo 'Signing accepted a wrong passphrase.' >&2; exit 1
fi
cp packaging/keys/atelier.asc "$work/public.asc"
printf 'not a public key\n' > packaging/keys/atelier.asc
if packaging/signing/with-keys.sh --check > "$work/rejected.log" 2>&1; then
  echo 'Signing accepted a mismatched public key.' >&2; exit 1
fi
cp "$work/public.asc" packaging/keys/atelier.asc
packaging/signing/with-keys.sh --check
packaging/signing/with-keys.sh goreleaser release --config .goreleaser.yaml --snapshot --clean
unset ATELIER_SIGNING_GPG_KEY ATELIER_SIGNING_GPG_PASSPHRASE ATELIER_SIGNING_APK_KEY
# Exercise archive checks with public keys present, as on provisioned checkouts.
# Then remove the copied keys to test the signed verifier independently.
scripts/test-release.sh
rm dist/atelier.asc dist/atelier.rsa.pub
packaging/signing/verify-release.sh
manifest="dist/atelier_$(jq -r .version dist/metadata.json)_checksums.txt"
printf '\ntampered\n' >> "$manifest"
if packaging/signing/verify-release.sh > "$work/rejected.log" 2>&1; then
  echo 'Verification accepted a tampered checksum manifest.' >&2; exit 1
fi
# Restore the signed manifest, then exercise checksum rejection for an artifact.
sed '$d' "$manifest" | sed '$d' > "$work/manifest"
mv "$work/manifest" "$manifest"
archive="$(find dist -name '*.tar.gz' | head -n 1)"
cp "$archive" "$work/archive"
printf 'tampered\n' >> "$archive"
if packaging/signing/verify-release.sh > "$work/rejected.log" 2>&1; then
  echo 'Verification accepted a tampered archive.' >&2; exit 1
fi
cp "$work/archive" "$archive"
packaging/signing/verify-release.sh
rm -rf "$root/dist"
cp -R dist "$root/dist"
echo 'Signed snapshot verified; missing/mismatched keys, wrong passphrase and tampering rejected.'
