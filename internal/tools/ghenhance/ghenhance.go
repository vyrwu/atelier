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

// DefaultCmd captures stderr to ~/.cache/atelier/ghenhance.log and,
// on non-zero exit, pipes the tail through `less -R` so the popup
// stays open until the user presses q. `read` doesn't work as a
// "wait for keypress" trap here because gh-enhance (a Bubble Tea
// TUI) leaves the tty in a state where `read` returns EOF
// immediately — `less` opens its own /dev/tty handle and dodges
// the issue.
var Spec = &popup.WorkspaceScoped{
	Tool: "ghenhance",
	DefaultCmd: `mkdir -p $HOME/.cache/atelier && ` +
		`gh-enhance 2>>$HOME/.cache/atelier/ghenhance.log; status=$?; ` +
		`if [ "$status" != 0 ]; then ` +
		`{ echo "gh-enhance exited $status (~/.cache/atelier/ghenhance.log)"; ` +
		`echo "--- last 20 stderr lines (press q to dismiss) ---"; ` +
		`tail -20 $HOME/.cache/atelier/ghenhance.log; } | less -R; ` +
		`fi`,
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
