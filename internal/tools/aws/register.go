package aws

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
)

// Manifest is aws's registry descriptor.
var Manifest = &manifest.Manifest{
	Tool:          true,
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
	Requires: []string{"aws-vault", "fzf"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Respawn the outer pane as `aws-vault exec <profile>`"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

// AddCommands wires aws's subcommands onto the dispatch root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(PickCommand())
	root.AddCommand(ListCommand())
}
