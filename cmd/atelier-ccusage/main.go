// Command atelier-ccusage is the singleton ccusage popup tool.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/ccusage"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
	Name:          "ccusage",
	Description:   "Claude API token usage snapshot (singleton, fresh on each open)",
	Popup:         manifest.KindGlobal,
	PrimaryInvoke: "open",
	Binding: &manifest.Binding{
		Style:  manifest.StyleFull,
		Invoke: "open",
	},
	UI: &manifest.UI{
		Icon:        "金",
		AccentColor: "220",
		PopupTitle:  "Claude Usage",
	},
	Requires:    []string{"npx"},
	Subcommands: []string{"open"},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(ccusage.OpenCommand())
	})
}
