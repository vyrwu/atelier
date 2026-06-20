#!/usr/bin/env bash
# atelier.tmux — TPM entry point.
#
# When loaded by tpm, sources `atelier init` output into the running tmux
# server. Looks for the atelier binary on PATH and in common install paths.

set -euo pipefail

find_atelier() {
    if command -v atelier >/dev/null 2>&1; then
        command -v atelier
        return
    fi
    for candidate in "$HOME/.local/bin/atelier" "/usr/local/bin/atelier" "/opt/homebrew/bin/atelier"; do
        if [[ -x "$candidate" ]]; then
            echo "$candidate"
            return
        fi
    done
    return 1
}

if ATELIER_BIN="$(find_atelier)"; then
    "$ATELIER_BIN" init | tmux source-file -
else
    tmux display-message "atelier: binary not found. Install via 'make install' or 'nix run github:vyrwu/atelier#install'"
    exit 0
fi
