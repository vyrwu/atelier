// Package popupshell is the built-in "Popup" tool: a plain shell in a
// workspace-scoped popup, opened at the active workspace dir. It's a
// first-class M-; entry (not a config launcher) so it's always available —
// the terminal-in-a-popup scratch space over the current workspace.
package popupshell

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// Manifest is popupshell's registry descriptor. Workspace-scoped popup, no
// dedicated key (it lives in the M-; menu); the backing session persists per
// workspace window so the scratch shell survives popup close/reopen.
var Manifest = &manifest.Manifest{
	Tool:          true,
	Name:          "popupshell",
	Description:   "Shell popup scoped to the current workspace",
	Popup:         manifest.KindWorkspace,
	PrimaryInvoke: "open",
	Binding: &manifest.Binding{
		Style:    manifest.StyleFull,
		Invoke:   "open",
		StartCwd: true,
	},
	UI: &manifest.UI{
		Icon:        "窓",
		AccentColor: "109",
		PopupTitle:  "Popup",
	},
}

// AddCommands wires popupshell's subcommands onto the dispatch root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(OpenCommand())
}

// OpenCommand opens the workspace-scoped shell popup. Delegates entirely to the
// popup primitive, which resolves the parent workspace, ensures the backing
// session running $SHELL in the workspace dir (@workspace_root), applies the
// popup style, and attaches.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Open a shell popup scoped to the current workspace",
		RunE: func(_ *cobra.Command, _ []string) error {
			return popup.OpenWorkspaceScoped(tmuxhost.New(socket), &popup.WorkspaceScoped{
				Tool:        "popupshell",
				DefaultCmd:  "$SHELL",
				Description: Manifest.Description,
			})
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
