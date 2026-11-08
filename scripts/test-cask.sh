#!/usr/bin/env bash
# Run on a disposable macOS CI runner after `mise run snapshot`. Keep the
# generated URLs' filenames and SHA256s, but serve this build from disk.
set -euo pipefail
[[ "${CI:-}" == true && "$(uname -s)" == Darwin ]]
cd "$(git rev-parse --show-toplevel)"
brew tap-new --no-git atelier/ci
tap="$(brew --repository atelier/ci)"
mkdir -p "$tap/Casks"
python3 - "$tap/Casks/atelier-ci.rb" <<'PY'
from pathlib import Path
import re
import sys
cask = Path('dist/homebrew/Casks/atelier.rb').read_text()
cask = cask.replace('cask "atelier"', 'cask "atelier-ci"')
cask = cask.replace('binary "atelier"', 'binary "atelier", target: "atelier-ci"')
cask = re.sub(r'https://github.com/vyrwu/atelier/releases/download/[^/]+/',
              Path('dist').resolve().as_uri() + '/', cask)
Path(sys.argv[1]).write_text(cask)
PY
trap 'brew uninstall --cask atelier/ci/atelier-ci; brew untap atelier/ci' EXIT
brew install --cask atelier/ci/atelier-ci
test "$("$(brew --prefix)/bin/atelier-ci" version)" = "$(jq -r .version dist/metadata.json)"
echo 'Generated Homebrew cask installed the snapshot successfully.'
