// Command atelier-ghdash is the per-workspace gh-dash popup tool.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/ghdash"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
	Name:          "ghdash",
	Description:   "Per-workspace gh-dash popup (GitHub PRs/issues)",
	Popup:         manifest.KindWorkspace,
	PrimaryInvoke: "open",
	// No Key — gh-dash is reachable only via M-; tool selector. Avoids
	// keymap pressure; gh-dash is not frequent enough to warrant a top-
	// level chord.
	Binding: &manifest.Binding{
		Style:    manifest.StyleFull,
		StartCwd: true,
		Invoke:   "open",
	},
	UI: &manifest.UI{
		Icon:        "表",
		AccentColor: "87",
		PopupTitle:  "GH Dash",
	},
	Requires:    []string{"gh-dash"},
	Subcommands: []string{"open"},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(ghdash.OpenCommand())
	})
}
