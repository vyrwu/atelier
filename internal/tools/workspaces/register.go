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
		{Key: "M-s", Title: "Active workspaces", Style: manifest.StylePicker, Invoke: "sessions", AlsoInPopup: true},
		{Key: "M-r", Title: "Workspace history", Style: manifest.StylePicker, Invoke: "recover", AlsoInPopup: true},
	},
	UI: &manifest.UI{
		Icon:        "栽",
		AccentColor: "168",
		PopupTitle:  "Select Workspace",
	},
	Popup:    manifest.KindNone,
	Requires: []string{"git", "fzf"},
	PickerBindings: []manifest.PickerBinding{
		// creator (repo picker) — M-m toggles a multi-repo (AI-named) session.
		{Picker: "creator", Key: "Enter", Action: "Accept repo (or submit prompt in multi-repo mode)"},
		{Picker: "creator", Key: "M-m", Action: "Toggle multi-repo (AI-named) session mode"},
		{Picker: "creator", Key: "M-s", Action: "Jump to active workspaces"},
		{Picker: "creator", Key: "M-r", Action: "Jump to workspace history"},
		{Picker: "creator", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// name (forced manual picker 製! — reached via M-m from prompt)
		{Picker: "name", Key: "Enter", Action: "Accept branch name (empty → default branch)"},
		{Picker: "name", Key: "M-m", Action: "Switch back to AI mode (製? )"},
		{Picker: "name", Key: "M-s", Action: "Jump to active workspaces"},
		{Picker: "name", Key: "M-r", Action: "Jump to workspace history"},
		{Picker: "name", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// prompt (AI branch-naming — default after repo pick)
		{Picker: "prompt", Key: "Enter", Action: "Submit prompt → agent generates branch name"},
		{Picker: "prompt", Key: "M-m", Action: "Switch to manual branch name (製! )"},
		{Picker: "prompt", Key: "M-s", Action: "Jump to active workspaces"},
		{Picker: "prompt", Key: "M-r", Action: "Jump to workspace history"},
		{Picker: "prompt", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// sessions (Active Workspaces — M-s)
		{Picker: "sessions", Key: "Enter", Action: "Switch to workspace / confirm action"},
		{Picker: "sessions", Key: "M-x", Action: "Delete workspace (with confirm)"},
		{Picker: "sessions", Key: "M-t", Action: "Tag workspace (pick/create; empty clears)"},
		{Picker: "sessions", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "sessions", Key: "M-r", Action: "Jump to workspace history"},
		{Picker: "sessions", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// recover (Workspace History — M-r)
		{Picker: "recover", Key: "Enter", Action: "Open the worktree / confirm action"},
		{Picker: "recover", Key: "M-x", Action: "Delete worktree (with confirm)"},
		{Picker: "recover", Key: "M-s", Action: "Jump to active workspaces"},
		{Picker: "recover", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "recover", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// clone (URL picker)
		{Picker: "clone", Key: "Enter", Action: "Validate URL + clone"},
		{Picker: "clone", Key: "M-s", Action: "Jump to active workspaces"},
		{Picker: "clone", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "clone", Key: "M-r", Action: "Jump to workspace history"},
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
	root.AddCommand(SessionListCommand())
	root.AddCommand(TagCommand())
	root.AddCommand(TagPreviewCommand())
	root.AddCommand(SetScopePinCommand())
	root.AddCommand(RecoverRowsCommand())
	root.AddCommand(RecoverDeletePromptCommand())
	root.AddCommand(RecoverDeleteRowCommand())
	root.AddCommand(AutoSessionCommand())
	root.AddCommand(PromptCommand())
	root.AddCommand(BuildCommand())
	root.AddCommand(NameCommand())
	root.AddCommand(BgPullCommand())
	// Kernel forge-badge slot commands (fed by the active ForgeIntegration).
	root.AddCommand(ForgeRefreshCommand())
	root.AddCommand(OpenForgeCommand())
}
