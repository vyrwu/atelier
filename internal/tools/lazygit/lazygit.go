// Package lazygit is atelier's per-window lazygit popup — bash-exact port
// of show_lazygit_popup.
package lazygit

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

var Spec = &popup.WorkspaceScoped{
	Tool:        "lazygit",
	DefaultCmd:  "lazygit",
	Description: "Per-window lazygit popup (bash-exact)",
}

// OpenCommand returns the `open` cobra command. Parent-context
// resolution, popup style application, and attach are all owned by
// internal/popup.OpenWorkspaceScoped.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Open the lazygit popup (bash-exact show_lazygit_popup port)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return popup.OpenWorkspaceScoped(tmuxhost.New(socket), Spec)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
