// Package ccusage is atelier's singleton ccusage popup — a quick
// snapshot of Claude API token spend. Backed by a single session
// `_atelier_ccusage` shared across all workspaces (account-wide data,
// not per-repo). Each open kills the previous session so the snapshot
// is always fresh; `less -R` keeps the popup alive until the user
// dismisses with `q`.
package ccusage

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// Spec backs the popup as a SessionGlobal. The launch command runs
// ccusage's daily snapshot, pipes through less so the user can read
// the numbers without the popup auto-closing.
//
// Why `npx ccusage`: ccusage is published as an npm package and many
// users run it via npx rather than installing globally. npx caches
// after the first run, so subsequent opens are fast.
var Spec = &popup.SessionGlobal{
	Tool:        "ccusage",
	DefaultCmd:  "npx ccusage daily | less -R",
	Description: "Claude API token usage snapshot (singleton)",
}

// OpenCommand wires `atelier tools ccusage open`. Kills any existing
// session first so each invocation produces fresh numbers — ccusage's
// daily totals shift in real time, and a cached stale popup would
// undermine the tool's whole point.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Open the ccusage popup (singleton; fresh on every open)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			if has, _ := h.HasSession(Spec.SessionName()); has {
				_ = h.KillSession(Spec.SessionName())
			}
			return Spec.EnsureAndAttach(h)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
