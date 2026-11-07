#!/usr/bin/env bash
# Native verification and install on disposable Linux CI runners only.
set -euo pipefail
[[ "${CI:-}" == true && "$(uname -s)" == Linux ]]
cd "$(git rev-parse --show-toplevel)"
version="$(jq -r .version dist/metadata.json)"
# RPM and APK test images run without network or write access to our artifacts.
docker run --rm --network=none -v "$PWD/dist:/artifacts:ro" -w /artifacts fedora@sha256:53d138f25f6738257c78553d51369b662efc0e0eb1cd1556a2fe0ad5a115be88 bash -euo pipefail -c '
  rpm --import atelier.asc
  rpm -K atelier-*.x86_64.rpm | grep -F "digests signatures OK"
  dnf install -y -q --disablerepo="*" --setopt=localpkg_gpgcheck=1 ./atelier-*.x86_64.rpm
  test "$(/usr/bin/atelier version)" = "$1"
  test "$(stat -c %a /usr/share/doc/atelier/LICENSE)" = 644
' -- "$version"
docker run --rm --network=none -v "$PWD/dist:/artifacts:ro" -w /artifacts alpine@sha256:0f49f331d9212aff11b932863be4eb01cdf962969aa86dab7d1ee21ac6f5f52b sh -euc '
  cp atelier.rsa.pub /etc/apk/keys/
  apk add --no-network --repositories-file=/dev/null ./atelier_*_x86_64.apk
  test "$(/usr/bin/atelier version)" = "$1"
  test "$(stat -c %a /usr/share/doc/atelier/LICENSE)" = 644
' -- "$version"
sudo apt-get update -qq
sudo apt-get install -y -qq debsig-verify
GNUPGHOME="$(mktemp -d "$RUNNER_TEMP/deb-verify.XXXXXX")"
export GNUPGHOME
trap 'rm -rf "$GNUPGHOME"' EXIT
gpg --batch --dearmor --output "$GNUPGHOME/atelier.gpg" dist/atelier.asc
id="$(gpg --show-keys --with-colons dist/atelier.asc | awk -F: '$1 == "pub" { print $5 }')"
sudo install -D -m 644 "$GNUPGHOME/atelier.gpg" "/usr/share/debsig/keyrings/$id/atelier.gpg"
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
gpgv --keyring "$GNUPGHOME/atelier.gpg" "dist/atelier_${version}_checksums.txt.sig" "dist/atelier_${version}_checksums.txt"
debsig-verify dist/atelier_*_amd64.deb
sudo dpkg -i dist/atelier_*_amd64.deb
test "$(/usr/bin/atelier version)" = "$version"
test "$(stat -c %a /usr/share/doc/atelier/LICENSE)" = 644
