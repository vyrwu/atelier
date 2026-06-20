// Package popupshell is atelier's per-window persistent shell popup.
// Bash equivalent: `show_tmux_popup`.
package popupshell

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

var Spec = &popup.WorkspaceScoped{
	Tool:        "popupshell",
	DefaultCmd:  "$SHELL",
	Description: "Persistent shell popup per parent window",
}

// Name builds the canonical backing-session name for the given parent IDs.
func Name(sid, wid string) string { return Spec.SessionName(sid, wid) }

// Create ensures the backing session exists. Idempotent.
func Create(h *tmuxhost.Client, sid, wid string) error {
	return Spec.Ensure(h, sid, wid, "")
}

// OpenCommand returns the `open` cobra command (used from cmd/atelier-popupshell).
// All the parent-context resolution, popup style stamping, and attach
// logic now lives in `internal/popup.OpenWorkspaceScoped` so a single
// place owns the canonical "open a per-window popup tool" recipe.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Open the popup shell (bash-exact show_tmux_popup port)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return popup.OpenWorkspaceScoped(tmuxhost.New(socket), Spec)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// CreateCommand returns the `create` cobra command (used by tests + tools).
func CreateCommand() *cobra.Command {
	var sessionID, windowID, socket string
	c := &cobra.Command{
		Use:   "create",
		Short: "Ensure the backing tmux session exists (does not attach)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if sessionID == "" || windowID == "" {
				return fmt.Errorf("--session and --window are required")
			}
			return Spec.Ensure(tmuxhost.New(socket), sessionID, windowID, "")
		},
	}
	c.Flags().StringVar(&sessionID, "session", "", "parent tmux session id")
	c.Flags().StringVar(&windowID, "window", "", "parent tmux window id")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
