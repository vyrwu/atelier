// Command atelier-workspaces is the workspaces tool.
package main

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
	"github.com/vyrwu/atelier/internal/tools/workspaces"
)

var Manifest = &manifest.Manifest{
	APIVersion:    manifest.APIVersion,
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
		{Key: "M-l", Title: "List worktrees", Style: manifest.StylePicker, Invoke: "list", AlsoInPopup: true},
	},
	UI: &manifest.UI{
		Icon:        "栽",
		AccentColor: "168",
		PopupTitle:  "Select Workspace",
	},
	Popup:       manifest.KindNone,
	Requires:    []string{"git", "fzf"},
	Subcommands: []string{"pick", "sessions", "create", "delete", "list", "clone"},
	PickerBindings: []manifest.PickerBinding{
		// creator (repo picker)
		{Picker: "creator", Key: "Enter", Action: "Accept repo (or submit prompt in auto-mode)"},
		{Picker: "creator", Key: "M-a", Action: "Toggle repo / auto-mode prompt"},
		{Picker: "creator", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "creator", Key: "M-l", Action: "Jump to list-worktrees picker"},
		{Picker: "creator", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// name (manual branch-name picker)
		{Picker: "name", Key: "Enter", Action: "Accept branch name (empty → default branch)"},
		{Picker: "name", Key: "M-a", Action: "Switch to auto-mode prompt"},
		{Picker: "name", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "name", Key: "M-l", Action: "Jump to list-worktrees picker"},
		{Picker: "name", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// prompt (auto-mode Claude-named)
		{Picker: "prompt", Key: "Enter", Action: "Submit prompt → Claude generates branch name"},
		{Picker: "prompt", Key: "M-a", Action: "Switch to manual-name mode"},
		{Picker: "prompt", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "prompt", Key: "M-l", Action: "Jump to list-worktrees picker"},
		{Picker: "prompt", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// sessions (Select Workspace)
		{Picker: "sessions", Key: "Enter", Action: "Switch to workspace / confirm action"},
		{Picker: "sessions", Key: "M-x", Action: "Delete workspace (with confirm)"},
		{Picker: "sessions", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "sessions", Key: "M-l", Action: "Jump to list-worktrees picker"},
		{Picker: "sessions", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// list (List Workspaces — worktrees on disk)
		{Picker: "list", Key: "Enter", Action: "Open the worktree / confirm action"},
		{Picker: "list", Key: "M-x", Action: "Delete worktree (with confirm)"},
		{Picker: "list", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "list", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "list", Key: "M-u", Action: "Jump to clone-from-URL picker"},
		// clone (URL picker)
		{Picker: "clone", Key: "Enter", Action: "Validate URL + clone"},
		{Picker: "clone", Key: "M-s", Action: "Jump to session picker"},
		{Picker: "clone", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "clone", Key: "M-l", Action: "Jump to list-worktrees picker"},
	},
}

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(workspaces.PickCommand())
		root.AddCommand(workspaces.SessionsCommand())
		root.AddCommand(workspaces.CreateCommand())
		root.AddCommand(workspaces.DeleteCommand())
		root.AddCommand(workspaces.ListCommand())
		root.AddCommand(workspaces.CloneCommand())
		// Internal subcommands wired up by the fzf transforms in pickers.
		root.AddCommand(workspaces.DeletePromptCommand())
		root.AddCommand(workspaces.DeleteRowCommand())
		root.AddCommand(workspaces.SessionListCommand())
		root.AddCommand(workspaces.ListRowsCommand())
		root.AddCommand(workspaces.ListDeletePromptCommand())
		root.AddCommand(workspaces.ListDeleteRowCommand())
		root.AddCommand(workspaces.AutoSessionCommand())
		root.AddCommand(workspaces.PromptCommand())
		root.AddCommand(workspaces.NameCommand())
		root.AddCommand(workspaces.BgPullCommand())
	})
}
