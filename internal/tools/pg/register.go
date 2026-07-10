package pg

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
)

// Manifest is pg's registry descriptor.
var Manifest = &manifest.Manifest{
	Tool:          true,
	Name:          "pg",
	Description:   "Postgres tools (pgcli + pgcenter, singleton popups)",
	Popup:         manifest.KindGlobal,
	PrimaryInvoke: "pgcli",
	Binding: &manifest.Binding{
		Style:  manifest.StyleFull,
		Invoke: "pgcli",
	},
	UI: &manifest.UI{
		Icon:        "索",
		AccentColor: "67",
		PopupTitle:  "Pgcli",
	},
	Requires: []string{"pgcli"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Connect to the selected (context, endpoint) pair"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

// AddCommands wires pg's subcommands onto the dispatch root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(PgcliCommand())
	root.AddCommand(PgcenterCommand())
	root.AddCommand(SwitchCommand())
	root.AddCommand(ContextsCommand())
	root.AddCommand(LaunchCommand())
}
