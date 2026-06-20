// Command atelier-pg is the singleton pgcli/pgcenter popup tool.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/pg"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
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
	Requires:    []string{"pgcli"},
	Subcommands: []string{"pgcli", "pgcenter", "switch", "contexts"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Connect to the selected (context, endpoint) pair"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(pg.PgcliCommand())
		root.AddCommand(pg.PgcenterCommand())
		root.AddCommand(pg.SwitchCommand())
		root.AddCommand(pg.ContextsCommand())
		root.AddCommand(pg.LaunchCommand())
	})
}
