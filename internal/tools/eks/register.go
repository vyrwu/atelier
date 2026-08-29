package eks

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
)

// Manifest is the eks tool's registry descriptor. Like k9s (singleton popup,
// picker on open, respawns on context change) but its popup is an interactive
// kubectl shell rather than the k9s TUI.
var Manifest = &manifest.Manifest{
	Tool:          true,
	Name:          "eks",
	Description:   "EKS assume-role kubectl shell (pick a context → granted assume → authed shell pointed at the cluster)",
	PrimaryInvoke: "open",
	Binding: &manifest.Binding{
		Key:    "M-e",
		Title:  "EKS shell",
		Style:  manifest.StylePicker,
		Invoke: "open",
	},
	UI: &manifest.UI{
		Icon:        "雲",
		AccentColor: "208",
		PopupTitle:  "EKS",
	},
	Popup:    manifest.KindGlobal,
	Requires: []string{"kubectl", "granted"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Assume the role + open a kubectl shell for the context"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

// AddCommands wires eks's subcommands onto the dispatch root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(OpenCommand())
	root.AddCommand(ContextsCommand())
	root.AddCommand(AttachCommand())
	root.AddCommand(LaunchCommand())
}
