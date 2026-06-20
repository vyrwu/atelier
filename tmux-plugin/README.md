# atelier tmux plugin

This directory packages atelier as a TPM-installable tmux plugin.

## Install via TPM

In your `~/.config/tmux/tmux.conf`:

```tmux
set -g @plugin 'vyrwu/atelier'
run '~/.tmux/plugins/tpm/tpm'
```

Then `prefix + I` to install.

The plugin script (`atelier.tmux`) only sources `atelier init` into tmux —
it expects the `atelier` binary to be on PATH (or in `~/.local/bin`,
`/usr/local/bin`, or `/opt/homebrew/bin`). Install the binary separately:

```bash
# from a clone of vyrwu/atelier:
make install         # to $HOME/.local/bin

# or via nix:
nix run github:vyrwu/atelier#install
```

## Install without TPM

Skip the plugin layer entirely. Add to `~/.config/tmux/tmux.conf`:

```tmux
run-shell 'atelier init | tmux source-file -'
```

That's it. The plugin script does nothing more than this.
