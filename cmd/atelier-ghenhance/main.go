// Command atelier-ghenhance is the per-workspace gh-enhance popup tool.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/ghenhance"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
	Tool:          true,
	Name:          "ghenhance",
	Description:   "Per-workspace gh-enhance popup (GitHub Actions)",
	Popup:         manifest.KindWorkspace,
	PrimaryInvoke: "open",
	Binding: &manifest.Binding{
		Style:    manifest.StyleFull,
		StartCwd: true,
		Invoke:   "open",
	},
	UI: &manifest.UI{
		Icon:        "舞",
		AccentColor: "203",
		PopupTitle:  "GH Enchance",
	},
	Requires:    []string{"gh-enhance"},
	Subcommands: []string{"open"},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(ghenhance.OpenCommand())
	})
}
