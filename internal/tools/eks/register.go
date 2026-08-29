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
	// Primary binding declares the M-; selector style (no dedicated key —
	// mirrors k9s). M-e is the switch chord (below), usable from inside the
	// shell popup too.
	Binding: &manifest.Binding{
		Style:  manifest.StylePicker,
		Invoke: "open",
	},
	Bindings: []manifest.Binding{
		{Key: "M-e", Title: "Switch EKS context", Style: manifest.StylePicker, Invoke: "switch", AlsoInPopup: true},
	},
	UI: &manifest.UI{
		Icon:        "雲",
		AccentColor: "208",
		PopupTitle:  "EKS",
	},
	Popup: manifest.KindGlobal,
	// Only kubectl is a real PATH binary. granted's `assume` is a sourced shell
	// function (not on PATH), so requiring it would make `atelier doctor`
	// false-report it missing — the per-context authCmd is what invokes it.
	Requires: []string{"kubectl"},
	PickerBindings: []manifest.PickerBinding{
		{Key: "Enter", Action: "Assume the role + open a kubectl shell for the context"},
		{Key: "M-e", Action: "Switch context (respawns the shell)"},
		{Key: "Esc", Action: "Dismiss"},
	},
}

// AddCommands wires eks's subcommands onto the dispatch root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(OpenCommand())
	root.AddCommand(SwitchCommand())
	root.AddCommand(ContextsCommand())
	root.AddCommand(AttachCommand())
	root.AddCommand(LaunchCommand())
}
