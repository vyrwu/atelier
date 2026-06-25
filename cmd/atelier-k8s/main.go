// Command atelier-k8s is the singleton k9s popup tool.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/k8s"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
	Name:          "k8s",
	Description:   "Singleton k9s popup (picker on every open; respawns on context change)",
	PrimaryInvoke: "open",
	// The primary binding dispatches the CONTEXT PICKER, which is a
	// small popup. The picker queues a separate full-size popup for the
	// actual K9s TUI via the internal `_attach` subcommand. Without
	// this split, M-; → K9s with no active context rendered the
	// context list inside a 99%-tall popup.
	Binding: &manifest.Binding{
		Style:  manifest.StylePicker,
		Invoke: "open",
	},
	Bindings: []manifest.Binding{
		// M-c reopens the context picker so the user can switch context
		// from inside K9s (or anywhere — root and popup tables). The
		// switch subcommand respawns the K9s popup-session on a real
		// context change; same-context is a no-op + attach.
		{Key: "M-c", Title: "Switch K9s context", Style: manifest.StylePicker, Invoke: "switch", AlsoInPopup: true},
	},
	UI: &manifest.UI{
		Icon:        "胡",
		AccentColor: "110",
		PopupTitle:  "K9s",
	},
	Popup:       manifest.KindGlobal,
	Requires:    []string{"k9s"},
	Subcommands: []string{"open", "switch", "contexts"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Switch to the selected k8s context (respawns k9s)"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(k8s.OpenCommand())
		root.AddCommand(k8s.SwitchCommand())
		root.AddCommand(k8s.ContextsCommand())
		root.AddCommand(k8s.LaunchCommand())
		root.AddCommand(k8s.AttachCommand())
	})
}
