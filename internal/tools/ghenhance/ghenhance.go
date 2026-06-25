// Package ghenhance is atelier's per-workspace gh-enhance popup.
// gh-enhance is dlvhdr's TUI for GitHub Actions workflow runs (sibling
// of gh-dash). Launches in the worktree cwd so PR / workflow lookups
// scope to the current repo.
package ghenhance

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

var Spec = &popup.WorkspaceScoped{
	Tool:        "ghenhance",
	DefaultCmd:  "gh-enhance",
	Description: "Per-workspace gh-enhance popup (GitHub Actions)",
}

func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Open the gh-enhance popup (per-workspace)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return popup.OpenWorkspaceScoped(tmuxhost.New(socket), Spec)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
