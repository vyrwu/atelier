package workspaces

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
)

// Manifest is workspaces' registry descriptor.
var Manifest = &manifest.Manifest{
	Tool:          true,
	Name:          "workspaces",
	Description:   "Workspace picker, session switcher, clone-from-URL (fzf-driven, bash-exact)",
	PrimaryInvoke: "sessions",
	Binding: &manifest.Binding{
		Key:         "M-n",
		Title:       "New workspace",
		Style:       manifest.StylePicker,
		Invoke:      "pick",
		AlsoInPopup: true,
	},
	Bindings: []manifest.Binding{
		{Key: "M-s", Title: "Select workspace", Style: manifest.StylePicker, Invoke: "sessions", AlsoInPopup: true},
		{Key: "M-r", Title: "Recover Workspace", Style: manifest.StylePicker, Invoke: "recover", AlsoInPopup: true},
	},
	UI: &manifest.UI{
		Icon:        "栽",
		AccentColor: "168",
		PopupTitle:  "Select Workspace",
	},
	Popup:    manifest.KindNone,
	Requires: []string{"git", "fzf"},
	PickerBindings: []manifest.PickerBinding{
		// creator (repo picker)
		{Picker: "creator", Key: "Enter", Action: "Accept repo (or submit prompt in auto-mode)"},
		{Picker: "creator", Key: "M-a", Action: "Toggle repo / auto-mode prompt"},
		{Picker: "creator", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "creator", Key: "M-r", Action: "Jump to recover workspace picker"},
		{Picker: "creator", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// name (manual branch-name picker)
		{Picker: "name", Key: "Enter", Action: "Accept branch name (empty → default branch)"},
		{Picker: "name", Key: "M-a", Action: "Switch to auto-mode prompt"},
		{Picker: "name", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "name", Key: "M-r", Action: "Jump to recover workspace picker"},
		{Picker: "name", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// prompt (auto-mode Claude-named)
		{Picker: "prompt", Key: "Enter", Action: "Submit prompt → Claude generates branch name"},
		{Picker: "prompt", Key: "M-a", Action: "Switch to manual-name mode"},
		{Picker: "prompt", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "prompt", Key: "M-r", Action: "Jump to recover workspace picker"},
		{Picker: "prompt", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// sessions (Select Workspace)
		{Picker: "sessions", Key: "Enter", Action: "Switch to workspace / confirm action"},
		{Picker: "sessions", Key: "M-x", Action: "Delete workspace (with confirm)"},
		{Picker: "sessions", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "sessions", Key: "M-r", Action: "Jump to recover workspace picker"},
		{Picker: "sessions", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// recover (Recover Workspace — worktrees on disk)
		{Picker: "recover", Key: "Enter", Action: "Open the worktree / confirm action"},
		{Picker: "recover", Key: "M-x", Action: "Delete worktree (with confirm)"},
		{Picker: "recover", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "recover", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "recover", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// clone (URL picker)
		{Picker: "clone", Key: "Enter", Action: "Validate URL + clone"},
		{Picker: "clone", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "clone", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "clone", Key: "M-r", Action: "Jump to recover workspace picker"},
	},
}

// AddCommands wires workspaces' subcommands (including the internal
// fzf-transform helpers) onto the dispatch root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(PickCommand())
	root.AddCommand(SessionsCommand())
	root.AddCommand(CreateCommand())
	root.AddCommand(DeleteCommand())
	root.AddCommand(RecoverCommand())
	root.AddCommand(CloneCommand())
	// Internal subcommands wired up by the fzf transforms in pickers.
	root.AddCommand(DeletePromptCommand())
	root.AddCommand(DeleteRowCommand())
	root.AddCommand(DeleteActionCommand())
	root.AddCommand(SessionListCommand())
	root.AddCommand(RecoverRowsCommand())
	root.AddCommand(RecoverDeletePromptCommand())
	root.AddCommand(RecoverDeleteRowCommand())
	root.AddCommand(RecoverDeleteActionCommand())
	root.AddCommand(AutoSessionCommand())
	root.AddCommand(PromptCommand())
	root.AddCommand(BuildCommand())
	root.AddCommand(NameCommand())
	root.AddCommand(BgPullCommand())
	// Kernel forge-badge slot commands (fed by the active ForgeIntegration).
	root.AddCommand(ForgeRefreshCommand())
	root.AddCommand(OpenForgeCommand())
}
