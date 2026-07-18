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
		{Key: "M-a", Title: "Active workspaces", Style: manifest.StylePicker, Invoke: "sessions", AlsoInPopup: true},
		{Key: "M-r", Title: "Workspace history", Style: manifest.StylePicker, Invoke: "recover", AlsoInPopup: true},
	},
	UI: &manifest.UI{
		Icon:        "栽",
		AccentColor: "168",
		PopupTitle:  "Active Workspaces",
	},
	Popup:    manifest.KindNone,
	Requires: []string{"git", "fzf"},
	PickerBindings: []manifest.PickerBinding{
		// creator (repo picker) — AI branch-naming is the default; M-f forces
		// a manual name, M-m switches to a multi-repo (AI-named) session.
		{Picker: "creator", Key: "Enter", Action: "Accept repo (or submit prompt in multi-repo mode)"},
		{Picker: "creator", Key: "M-m", Action: "Toggle multi-repo (AI-named) session mode"},
		{Picker: "creator", Key: "M-a", Action: "Jump to active workspaces"},
		{Picker: "creator", Key: "M-r", Action: "Jump to workspace history"},
		{Picker: "creator", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// name (manual branch-name picker — reached via M-f)
		{Picker: "name", Key: "Enter", Action: "Accept branch name (empty → default branch)"},
		{Picker: "name", Key: "M-f", Action: "Switch back to AI branch naming"},
		{Picker: "name", Key: "M-a", Action: "Jump to active workspaces"},
		{Picker: "name", Key: "M-r", Action: "Jump to workspace history"},
		{Picker: "name", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// prompt (default: AI-named branch)
		{Picker: "prompt", Key: "Enter", Action: "Submit prompt → agent generates branch name"},
		{Picker: "prompt", Key: "M-f", Action: "Force a manual branch name"},
		{Picker: "prompt", Key: "M-a", Action: "Jump to active workspaces"},
		{Picker: "prompt", Key: "M-r", Action: "Jump to workspace history"},
		{Picker: "prompt", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// sessions (Active Workspaces — M-a)
		{Picker: "sessions", Key: "Enter", Action: "Switch to workspace / confirm action"},
		{Picker: "sessions", Key: "Tab", Action: "Cycle sort (attention/age/repo/tag/forge)"},
		{Picker: "sessions", Key: "M-x", Action: "Delete workspace (with confirm)"},
		{Picker: "sessions", Key: "M-t", Action: "Tag workspace (pick/create; empty clears)"},
		{Picker: "sessions", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "sessions", Key: "M-r", Action: "Jump to workspace history"},
		{Picker: "sessions", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// recover (Workspace History — worktrees on disk)
		{Picker: "recover", Key: "Enter", Action: "Open the worktree / confirm action"},
		{Picker: "recover", Key: "M-x", Action: "Delete worktree (with confirm)"},
		{Picker: "recover", Key: "M-a", Action: "Jump to active workspaces"},
		{Picker: "recover", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "recover", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// clone (URL picker)
		{Picker: "clone", Key: "Enter", Action: "Validate URL + clone"},
		{Picker: "clone", Key: "M-a", Action: "Jump to active workspaces"},
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
	root.AddCommand(SortNextCommand())
	root.AddCommand(TagCommand())
	root.AddCommand(TagPreviewCommand())
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
