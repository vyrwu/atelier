#!/usr/bin/env bash
# Exercise the archive a user downloads, including checksums and version stamp.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
version="$(jq -r .version dist/metadata.json)"
case "$(uname -s)" in
  Darwin) platform=macos ;;
  Linux) platform=linux ;;
  *) exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64) arch=x86_64 ;;
  *) exit 1 ;;
esac
# GoReleaser hashes these external files without putting them in dist. Bootstrap
# snapshots have no production keys; provisioned snapshots must check both.
for key in packaging/keys/atelier.asc packaging/keys/atelier.rsa.pub; do
  if [[ -f "$key" ]]; then cp "$key" dist/; fi
done
(cd dist && shasum -a 256 -c "atelier_${version}_checksums.txt")
install_dir="$(mktemp -d "${TMPDIR:-/tmp}/atelier-release.XXXXXX")"
trap 'rm -rf "$install_dir"' EXIT
tar -xzf "dist/atelier_${version}_${platform}_${arch}.tar.gz" -C "$install_dir"
test -f "$install_dir/LICENSE"
test -f "$install_dir/README.md"
test "$("$install_dir/atelier" version)" = "$version"
echo "Archive installed and reports ${version} on ${platform}/${arch}."
