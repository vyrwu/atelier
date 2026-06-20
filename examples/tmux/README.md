# examples/tmux/

Reference tmux configurations showing how atelier embeds into
different setups. Each `.conf` is runnable standalone.

| File | What it shows |
|---|---|
| `minimal.conf` | atelier in `--bare` mode on top of vanilla tmux. No theme, no plugins. The simplest possible embedding. |
| `powerline.conf` | atelier in a powerline-styled setup with arrow-segment statusline. Demonstrates that `stamp-statusline` correctly handles the `` glyph anchor. |
| `vyrwu.conf` | The author's actual daily-driver tmux config, with dracula + TPM plugins + atelier underneath. The "reference setup" for the engine-vs-distribution framing. |

## Trying them out

Each file can be sourced via `tmux -f`:

```bash
tmux -L atelier-minimal -f examples/tmux/minimal.conf new-session
```

Use a separate `-L <socket>` per example so they don't collide with
your daily tmux server.

## Adapting

The only load-bearing line for atelier integration is:

```tmux
run-shell 'atelier init --bare | tmux source-file -'
```

Everything else is taste. Replace the theme, the keymap, the
plugin set with whatever you actually want. As long as atelier's
init runs, the engine wiring works.

For the statusline emitter contract, see
[../docs/EMBEDDING.md](../../docs/EMBEDDING.md).
