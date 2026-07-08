// Command atelier-lazygit is the per-window lazygit popup tool.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/lazygit"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
	Tool:          true,
	Name:          "lazygit",
	Description:   "Per-window lazygit popup",
	Popup:         manifest.KindWorkspace,
	PrimaryInvoke: "open",
	Binding: &manifest.Binding{
		Style:    manifest.StyleFull,
		StartCwd: true,
		Invoke:   "open",
	},
	UI: &manifest.UI{
		Icon:        "枝",
		AccentColor: "140",
		PopupTitle:  "Lazygit",
	},
	Requires:    []string{"lazygit"},
	Subcommands: []string{"open"},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(lazygit.OpenCommand())
	})
}
