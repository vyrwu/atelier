// Command atelier-popupshell is the per-window persistent shell popup tool.
// Invoked via the tool selector (M-;), no dedicated top-level shortcut.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/popupshell"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
	Name:          "popupshell",
	Description:   "Persistent shell popup per parent window",
	Popup:         manifest.KindWorkspace,
	PrimaryInvoke: "open",
	Binding: &manifest.Binding{
		Style:    manifest.StyleFull,
		StartCwd: true,
		Invoke:   "open",
	},
	UI: &manifest.UI{
		Icon:        "浮",
		AccentColor: "188",
		PopupTitle:  "Popup",
	},
	Subcommands: []string{"open", "create"},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(popupshell.OpenCommand())
		root.AddCommand(popupshell.CreateCommand())
	})
}
