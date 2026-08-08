package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// ReconcileCommand is `atelier reconcile [--fix]`: the one authorized recovery
// command. It captures the live topology, validates the invariants, and — with
// --fix — repairs every fixable violation (kill orphan popups, drop a bad
// outer pointer, unset a detached outer-client hint, disarm a leaked
// client-detached hook). Default is a dry run that only reports.
//
// It operates purely on live tmux state; the persisted statestore cache is
// reconciled separately by `atelier state sync`.
func ReconcileCommand() *cobra.Command {
	var socket string
	var fix bool
	c := &cobra.Command{
		Use:   "reconcile",
		Short: "Report (and with --fix, repair) tmux state invariant violations",
		Long: `Report atelier's tmux-state invariant violations; with --fix, repair the
fixable ones. Without --fix this is a dry run — it only reports.

Violations split in two:

  Fixable (repaired by --fix): orphan popups; an outer pointer stranded on the
    launcher, a popup, or a dead target; a detached outer-client hint; a
    client-moving hook left armed at rest; attention stranded on a popup window.
  Report-only (never auto-fixed): attention on a non-listable workspace window,
    a window whose working directory is gone, more than one workspace client on
    a tty. The fix is a human action; --fix leaves these untouched.

reconcile does NOT clear the attention badge. Attention on a real workspace
(the status-line dot) is a signal — an agent finished and you haven't looked —
not a fault; visit the workspace to clear it. A healthy server with pending
attention correctly reports "no violations".

Operates on live tmux only. The persisted state cache is reconciled separately
by 'atelier state sync'.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			results, err := state.Reconcile(tmuxhost.New(socket), fix)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(results) == 0 {
				fmt.Fprintln(out, "no violations")
				return nil
			}
			for _, r := range results {
				tag := "report-only"
				switch {
				case r.Repaired:
					tag = "repaired"
				case r.Fixable && !fix:
					tag = "would-fix"
				case r.Fixable && fix:
					tag = "FIX-FAILED"
				}
				fmt.Fprintf(out, "%-5s %-24s [%s] %s: %s\n",
					r.Severity, r.Code, tag, r.Subject, r.Detail)
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	c.Flags().BoolVar(&fix, "fix", false, "repair fixable violations (default: report only)")
	return c
}
