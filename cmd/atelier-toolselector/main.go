// Command atelier-toolselector is the fzf master tool picker.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/toolselector"
)

var Manifest = &manifest.Manifest{
	APIVersion:  manifest.APIVersion,
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
	Popup:       manifest.KindNone,
	Requires:    []string{"fzf"},
	Subcommands: []string{"select"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Open the selected tool"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(toolselector.SelectCommand())
	})
}
