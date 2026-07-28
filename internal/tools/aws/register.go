package aws

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
)

// Manifest is aws's registry descriptor.
var Manifest = &manifest.Manifest{
	Tool:          true,
	Name:          "aws",
	Description:   "AWS Assume — granted profile picker (assumes a profile in the outer pane's shell)",
	Popup:         manifest.KindNone,
	PrimaryInvoke: "pick",
	Binding: &manifest.Binding{
		Style:  manifest.StylePicker,
		Invoke: "pick",
	},
	UI: &manifest.UI{
		Icon:        "サ",
		AccentColor: "180",
		PopupTitle:  "AWS Assume",
	},
	Requires: []string{"granted", "fzf"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Assume `<profile>` in the outer pane's shell (granted `assume`)"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

// AddCommands wires aws's subcommands onto the dispatch root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(PickCommand())
	root.AddCommand(ListCommand())
}
