// Command atelier-ghpr is the GitHub-PR-status badge provider: it enriches
// the M-s workspace picker with a per-workspace PR-state symbol and binds
// M-o to open the workspace's PR in the browser.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/ghpr"
)

var Manifest = &manifest.Manifest{
	APIVersion:  manifest.APIVersion,
	Name:        "ghpr",
	Description: "Per-workspace GitHub PR status badge + open-in-browser (M-o)",
	// No popup and no top-level binding: this tool contributes to the
	// workspace picker rather than opening its own surface.
	Popup: manifest.KindNone,
	Badge: &manifest.Badge{
		Option:     ghpr.OptBadge,
		Refresh:    "_refresh",
		SortOption: ghpr.OptState,
		SortOrder:  ghpr.StateOrder,
		Action: &manifest.BadgeAction{
			Key:    "M-o",
			Invoke: "open",
			Label:  "open PR",
		},
	},
	Requires:    []string{"gh"},
	Subcommands: []string{"open"},
	PickerBindings: []manifest.PickerBinding{
		{Picker: "sessions", Key: "M-o", Action: "Open workspace PR in browser"},
	},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(ghpr.OpenCommand())
		root.AddCommand(ghpr.RefreshCommand())
	})
}
