# Release security

Merging the release-please PR remains Atelier's release operation. Automation
accepts only stable `vX.Y.Z` tags on `main`, reruns CI at the exact tagged commit,
and builds without CI caches. Releases contain four archives, six native Linux
packages, public signing keys and a signed SHA256 manifest. GitHub build
provenance covers every archive, package and public key in that manifest. Only
a successful build, signature verification and attestation can advance the
Homebrew cask through a separate job with a tap-only App token.

## Security controls

These controls protect build inputs, release credentials and published artifacts.

| Control | Implementation |
| --- | --- |
| Full-SHA action pins | All external actions pinned; Dependabot updates pins weekly. |
| Pinned tools and verified downloads | Exact `mise.toml` versions and `mise.lock` download checksums. Every workflow pins mise; the privileged build also pins its binary SHA256. |
| Weekly module, action and tool updates | Dependabot plus `tools.yml`. Candidate tools run without secrets; a clean writer copies only `mise.toml`, `mise.lock`, `go.mod` and `go.sum`, then mints an App token. |
| Local/CI parity and optional pre-push hooks | `mise run ci`; `mise run hooks`. |
| Race tests, gosec, govulncheck, actionlint, zizmor | Linux and macOS; no release-branch exemption. |
| Real installation tests | Archive checks on both platforms, Homebrew cask on macOS, throwaway-key RPM/APK/DEB signature checks and native installs on Linux. |
| Protected main and passing CI | Full CI reruns at the validated tag commit. Required checks and resolved review threads are in `.github/security-ruleset.json`. |
| Protected credentials | Main-only `release-automation` environment for the tag-creating App. Tag-only `release` environment for signing keys and `tap` environment for a separate tap App. No admin bypass. |
| Least-privilege tokens | Read-only workflow defaults, scoped job permissions and App tokens, no saved checkout credentials. Actions cannot approve PRs. |
| Locked, isolated signing build | Only Go and GoReleaser installed from a temporary directory containing the two reviewed mise files. No caches, checkout-local mise discovery, shims, tasks, GoReleaser hooks or execution of project binaries/tests. `GOTOOLCHAIN=local`, explicit GoReleaser config. |
| Reproducible inputs | `-trimpath`, commit timestamps, read-only module resolution; module verification happens in CI before secrets are available. |
| Signed native packages and manifest | RSA-4096 OpenPGP signatures for RPM, DEB (`origin`) and checksums; RSA-4096 APK signatures. Public keys are release assets covered by checksums and provenance. |
| Fail-closed signing and key rotation | Match both private keys to committed public keys and test the passphrase before building. Temporary private files are mode 0600, keyrings removed on exit. Two-year OpenPGP expiry, warning within 60 days. |
| Published release notes | Empty GitHub release body stops attestation and tap publication. |
| Serialization and rollback prevention | One release concurrency group; recheck tag syntax, main ancestry, remote existence, commit identity and newest version before publishing; recheck tap version on every push retry. |
| Vulnerability alerts and security updates | `scripts/configure-security.sh` enables repository settings as well as installing the checked-in rulesets and environment policies. |

Atelier uses release-please and a Homebrew cask. A tag ruleset allows only the
release App to create, update or delete `v*` tags. That App's private
key must be confined to main. This closes the path where a branch workflow
could create its own tag and enter the signing environment. Key provisioning
also rejects existing `v*` tags outside main.

## One-time repository setup

Files alone cannot activate GitHub settings or migrate private credentials.
**Production release automation remains unavailable until this setup and the
public-key commit are complete.** PR CI uses disposable keys and remains usable
before provisioning. Run setup from a reviewed checkout with no release running.

1. As a repository administrator, run `scripts/configure-security.sh`. It enables
   Dependabot alerts/security fixes, applies both checked-in rulesets, disables
   Actions PR approvals, and creates the three protected environments below.
   Main requires an approval after the last push, resolved conversations and
   green checks, with no bypass; arrange another reviewer before enabling it.
   Existing branch protection and secret scanning/push protection stay enabled.
2. Keep repository variable `RELEASE_APP_ID=4281327`. The matching App needs
   Contents and Pull requests read/write on `vyrwu/atelier`. Store its original
   PEM as `RELEASE_APP_PRIVATE_KEY` in **release-automation**, then delete the
   repository-level secret of the same name. GitHub cannot export an existing
   secret: use the original PEM or rotate the App key. Do not put this key in
   another environment, repository secret, or the tap job.
3. Install a **separate App** with Contents read/write on `vyrwu/homebrew-tap`
   only. Put its ID in `HOMEBREW_TAP_APP_ID` and PEM in
   `HOMEBREW_TAP_APP_PRIVATE_KEY`, both in the **tap** environment. Remove the old
   `HOMEBREW_TAP_GITHUB_TOKEN` PAT after confirming the new installation. The tap
   App must have no access to Atelier and no release-tag bypass.
4. Run `packaging/signing/check-protections.sh`, then
   `packaging/signing/new-keys.sh`. The latter rechecks protections, generates
   Atelier's own keys, streams three secrets into **release**, and writes only
   public keys under `packaging/keys` and fingerprints into this guide and the
   README. Requires `gh`, `git`, GnuPG and OpenSSL.
5. Commit the generated public keys and both fingerprint tables to this PR (or
   a reviewed follow-up before releasing). Merge only after their CI is green.
   The private keys and passphrase must never enter Git, logs or repository-wide
   secrets. Subsequent releases refuse missing or mismatched keys.

| Environment | Allowed deployment ref | Credentials |
| --- | --- | --- |
| `release-automation` | branch `main` only | `RELEASE_APP_PRIVATE_KEY` |
| `release` | tag `v*` only | `ATELIER_SIGNING_GPG_KEY`, `ATELIER_SIGNING_GPG_PASSPHRASE`, `ATELIER_SIGNING_APK_KEY` |
| `tap` | tag `v*` only | separate `HOMEBREW_TAP_APP_PRIVATE_KEY` and `HOMEBREW_TAP_APP_ID` variable |

All environments disallow admin bypass. The provisioning guard checks live
settings and inherited-secret assumptions for this personal repository. Re-audit
those assumptions before transferring it to an organization. Keep the App ID
in the tag ruleset synchronized with `RELEASE_APP_ID` if the App is replaced.

## Release signing keys

This table is updated by `packaging/signing/new-keys.sh`; production signing is
not ready while these fingerprints are absent. Compare downloaded keys against
the reviewed table before trusting them.

| Key | Signs | Fingerprint |
| --- | --- | --- |
| `atelier.asc` | `.rpm`, `.deb`, `checksums.txt` | `not generated yet` |
| `atelier.rsa.pub` | `.apk` | SHA-256 `not generated yet` |

Rotate before the OpenPGP key's two-year expiry with
`packaging/signing/new-keys.sh --rotate`, then commit the new public keys and
fingerprints. Rotation replaces the environment secrets immediately, so pause
releases until that commit lands. Existing releases retain their old signatures;
users need the old public keys to verify them. For a compromised key, remove it
from RPM's key database, `/etc/apk/keys`, and Debian's policy/keyring directories;
there is no automatic revocation service. Publish the rotation and new
fingerprints for users to review.

## Verify and install downloads

Use an empty directory, select an explicit release tag, and stop if any check
fails. Older releases predating this workflow have no signatures/attestations.
Each artifact's provenance must name this workflow, the selected tag, and a
GitHub-hosted runner:

```sh
tag=vX.Y.Z # replace with the release you intend to install
verify() {
  gh attestation verify "$1" --repo vyrwu/atelier \
    --signer-workflow vyrwu/atelier/.github/workflows/release.yml \
    --source-ref "refs/tags/$tag" --deny-self-hosted-runners
}
```

For a standalone archive, download it and run `verify <archive.tar.gz>` before
extracting. To verify its signed checksums too:

```sh
gh release download "$tag" -R vyrwu/atelier -p atelier.asc \
  -p "atelier_${tag#v}_checksums.txt" -p "atelier_${tag#v}_checksums.txt.sig"
verify atelier.asc
gpg --show-keys atelier.asc # compare the full fingerprint with the table above
gpg --dearmor --output atelier.gpg atelier.asc
gpgv --keyring "$PWD/atelier.gpg" "atelier_${tag#v}_checksums.txt.sig" "atelier_${tag#v}_checksums.txt"
shasum -a 256 --check --ignore-missing "atelier_${tag#v}_checksums.txt"
```

The manifest covers all archives, native packages and public keys. Downloaded
keys require provenance and fingerprint checks too; a signature alone does not
establish whose key you received. Install `tmux`, `git`, `gh` and your chosen
agent separately on Linux.

### Fedora/RHEL (RPM)

After downloading and verifying `atelier.asc` as above:

```sh
sudo rpm --import atelier.asc
gh release download "$tag" -R vyrwu/atelier -p "atelier-*.$(uname -m).rpm"
verify atelier-*.rpm
rpm -K atelier-*.rpm # must report signatures OK
sudo dnf install --setopt=localpkg_gpgcheck=1 ./atelier-*.rpm
```

`localpkg_gpgcheck=1` makes DNF enforce signatures for local packages.

### Alpine (APK)

```sh
gh release download "$tag" -R vyrwu/atelier -p atelier.rsa.pub
verify atelier.rsa.pub
sha256sum atelier.rsa.pub # compare with the table above
sudo cp atelier.rsa.pub /etc/apk/keys/
gh release download "$tag" -R vyrwu/atelier -p "atelier_*_$(apk --print-arch).apk"
verify atelier_*.apk
sudo apk add ./atelier_*.apk
```

APK refuses packages that none of its trusted keys signed. Do not use
`--allow-untrusted`.

### Debian/Ubuntu (DEB)

APT does not verify a local DEB's embedded signature. Download and verify
`atelier.asc` and the signed manifest as above, then install a debsig policy:

```sh
sudo apt install debsig-verify
id="$(gpg --show-keys --with-colons atelier.asc | awk -F: '$1 == "pub" { print $5 }')"
sudo install -D -m 644 atelier.gpg "/usr/share/debsig/keyrings/$id/atelier.gpg"
sudo install -d "/etc/debsig/policies/$id"
sudo tee "/etc/debsig/policies/$id/atelier.pol" > /dev/null <<POLICY
<?xml version="1.0"?>
<!DOCTYPE Policy SYSTEM "https://www.debian.org/debsig/1.0/policy.dtd">
<Policy xmlns="https://www.debian.org/debsig/1.0/">
  <Origin Name="atelier" id="$id" Description="Atelier releases"/>
  <Selection><Required Type="origin" File="atelier.gpg" id="$id"/></Selection>
  <Verification MinOptional="0"><Required Type="origin" File="atelier.gpg" id="$id"/></Verification>
</Policy>
POLICY
gh release download "$tag" -R vyrwu/atelier -p "atelier_*_$(dpkg --print-architecture).deb"
verify atelier_*.deb
sha256sum --check --ignore-missing "atelier_${tag#v}_checksums.txt"
debsig-verify atelier_*.deb
sudo apt install ./atelier_*.deb
```

## Retrying a release

Use the existing run's **Re-run failed jobs** for a tap-only failure within the
cask artifact's seven-day retention. After that, or for a full retry, dispatch
at the same release tag so provenance identifies the commit built:

```sh
gh workflow run release.yml --ref vX.Y.Z -f tag=vX.Y.Z
```

Dispatching from `main`, a branch, an older release, or a tag outside main is
rejected. Tags changed/deleted since the event or CI are also rejected.
GoReleaser is explicitly bound to the validated tag even when other tags point
at that commit. Releases predating this workflow require a new release.

GoReleaser uploads before attestation. If attestation fails, assets may be visible
but the tap does not advance; reject downloads that fail provenance verification.
A retry replaces assets from the same guarded tag and attempts attestation again.

## Maintaining the gates

Do not rename `lint`, `goreleaser-check`, or either `build-and-test` check without
updating the ruleset and provisioning guard. `goreleaser-check` requires both
archive/cask jobs and the signed-package job, including native installation.
`mise run snapshot` is unsigned; `mise run signing-test` generates throwaway
keys and tests real signing, missing/mismatched keys, wrong passphrases and
tampered downloads. Neither uses production secrets. Public-key globs allow
unsigned bootstrap snapshots; the production wrapper always requires both keys.
Once either public key is committed, CI requires both fingerprints to match.

Update all mise manager version pins together and the privileged Linux binary
checksum alongside them. Weekly tool PRs update versions and `mise.lock` but
cannot change workflows. Review lockfile/backend changes as executable build
inputs. Never add `go run`, tests, built binaries, mise tasks or GoReleaser hooks
to the job holding signing keys. Prepare any future generated assets in a
separate job without secrets.

Keep security exceptions beside the operation with the rule and reason.
Test-only gosec exceptions cover fixture permissions, paths and fixed commands;
tests still receive overflow and crypto checks. Intentional local command
execution remains an explicit trust boundary.
