// Command atelier-aws is the aws-vault profile picker tool.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/aws"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
	Name:          "aws",
	Description:   "aws-vault profile picker (respawns outer pane under aws-vault exec)",
	Popup:         manifest.KindNone,
	PrimaryInvoke: "pick",
	Binding: &manifest.Binding{
		Style:  manifest.StylePicker,
		Invoke: "pick",
	},
	UI: &manifest.UI{
		Icon:        "サ",
		AccentColor: "180",
		PopupTitle:  "AWS Profile",
	},
	Requires:    []string{"aws-vault", "fzf"},
	Subcommands: []string{"pick", "list"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Respawn the outer pane as `aws-vault exec <profile>`"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(aws.PickCommand())
		root.AddCommand(aws.ListCommand())
	})
}
