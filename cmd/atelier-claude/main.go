// Command atelier-claude is the per-window Claude Code popup tool.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/claude"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
	Name:          "claude",
	Description:   "Per-window Claude Code popup",
	Popup:         manifest.KindWorkspace,
	PrimaryInvoke: "open",
	Binding: &manifest.Binding{
		Style:    manifest.StyleFull,
		StartCwd: true,
		Invoke:   "open",
	},
	UI: &manifest.UI{
		Icon:        "知",
		AccentColor: "173",
		PopupTitle:  "Claude Code",
	},
	Requires:    []string{"claude"},
	Subcommands: []string{"open", "set-prompt", "recap", "notify-attention"},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(claude.OpenCommand())
		root.AddCommand(claude.SetPromptCommand())
		root.AddCommand(claude.RecapCommand())
		root.AddCommand(claude.NotifyAttentionCommand())
		root.AddCommand(claude.RecapFromHookCommand())
	})
}
