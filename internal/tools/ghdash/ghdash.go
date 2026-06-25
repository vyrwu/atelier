// Package ghdash is atelier's per-workspace gh-dash popup. gh-dash is
// dlvhdr's TUI for browsing GitHub PRs / issues; when launched from
// within a worktree, gh-dash picks up the repo via its in-tree
// `.gh-dash.yml` (or the global config in $XDG_CONFIG_HOME/gh-dash/).
package ghdash

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// Spec is the workspace-scoped popup descriptor. Each parent window
// gets its own `_atelier_ghdash_<sid>_<wid>` session so state (cursor,
// active section) persists per workspace.
var Spec = &popup.WorkspaceScoped{
	Tool:        "ghdash",
	DefaultCmd:  "gh-dash",
	Description: "Per-workspace gh-dash popup (GitHub PRs/issues)",
}

// OpenCommand wires `atelier tools ghdash open`. Parent context, popup
// style, and attach are all owned by `internal/popup.OpenWorkspaceScoped`.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Open the gh-dash popup (per-workspace)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return popup.OpenWorkspaceScoped(tmuxhost.New(socket), Spec)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
