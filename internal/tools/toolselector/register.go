package toolselector

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
)

// Manifest is toolselector's registry descriptor.
var Manifest = &manifest.Manifest{
	Name:        "toolselector",
	Description: "fzf master picker for atelier tools",
	Binding: &manifest.Binding{
		Key:         "M-;",
		Style:       manifest.StylePicker,
		Invoke:      "select",
		AlsoInPopup: true,
	},
	UI: &manifest.UI{
		Icon:        "⌘",
		AccentColor: "172",
		PopupTitle:  "Select Tool",
	},
	Popup:    manifest.KindNone,
	Requires: []string{"fzf"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Open the selected tool"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

// AddCommands wires toolselector's subcommands onto the dispatch root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(SelectCommand())
}
