package workspaces

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// Sticky M-s scope (M-p in the session picker): a search-query prefix the
// user locks so the picker opens pre-filtered to a focused context (one
// repo, one tag) instead of an empty query, and keeps that scope across
// picker invocations for the rest of the tmux session. Stored as a tmux
// global option (session-lived, dropped on `atelier stop`) — see
// workspace.OptScopePin. The picker reads it on open to pre-seed the
// query (with a trailing space, so the pin reads as a prefix you keep
// typing after) and to show the "Pinned" footer badge; M-p toggles it via
// the _set-scope-pin sub-command.

// pinnedBadge is the "Pinned" indicator shown at the bottom-left of the
// M-s footer while a scope is pinned. Rendered in bold red foreground —
// the picker's primary accent — so it stands out from the grey keybind
// hints beside it.
const pinnedBadge = "\033[1;31mPinned\033[0m"

// sessionFooter builds the M-s picker footer: the keybind hints, the M-o
// open-PR hint when a forge is active, prefixed with the Pinned badge
// while a scope is pinned. Shared by the picker's initial render and
// _set-scope-pin's live change-footer so the two never drift.
func sessionFooter(pinned, forge bool) string {
	f := "M-x · delete  |  M-t · tag  |  M-p · pin  |  M-? · help"
	if forge {
		f += "  |  M-o · open PR"
	}
	if pinned {
		f = pinnedBadge + "  " + f
	}
	return f
}

// SetScopePinCommand is the hidden `_set-scope-pin [query]`: bound to M-p
// in the session picker via a `transform` action. It toggles the pin
// based on the CURRENT scope state (not the query), and echoes the fzf
// actions that update the picker live without a reload:
//
//   - already pinned → unpin: clear the scope, clear-query (so fzf
//     restarts from a blank filter), and drop the Pinned badge.
//   - not pinned → pin the current query, put a trailing space (the pin
//     reads as a prefix), and show the Pinned badge. An empty query is a
//     no-op — there is nothing to pin.
func SetScopePinCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_set-scope-pin [query]",
		Short:  "internal: toggle the M-s picker scope pin (M-p)",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) > 0 {
				query = strings.TrimSpace(args[0])
			}
			h := tmuxhost.New(socket)
			forge := forgeActive()
			out := cmd.OutOrStdout()

			if workspace.GetScopePin(h) != "" {
				// Already pinned → unpin and reset the picker.
				if err := workspace.SetScopePin(h, ""); err != nil {
					debuglog.LogErr("workspaces._set-scope-pin: unpin", err)
					return err
				}
				debuglog.Logf("workspaces._set-scope-pin: unpinned")
				fmt.Fprintf(out, "clear-query+change-footer(%s)\n", sessionFooter(false, forge))
				return nil
			}

			if query == "" {
				return nil // nothing to pin
			}
			if err := workspace.SetScopePin(h, query); err != nil {
				debuglog.LogErr("workspaces._set-scope-pin: pin", err)
				return err
			}
			debuglog.Logf("workspaces._set-scope-pin: pinned=%q", query)
			// put( ) appends the trailing space to the live query so the
			// pin reads as a prefix the user keeps typing after.
			fmt.Fprintf(out, "put( )+change-footer(%s)\n", sessionFooter(true, forge))
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
