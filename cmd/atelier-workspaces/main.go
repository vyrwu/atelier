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
	Popup:       manifest.KindNone,
	Requires:    []string{"git", "fzf"},
	Subcommands: []string{"pick", "sessions", "create", "delete", "recover", "clone"},
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

func main() {
	toolmain.Run(Manifest, func(root *cobra.Command) {
		root.AddCommand(workspaces.PickCommand())
		root.AddCommand(workspaces.SessionsCommand())
		root.AddCommand(workspaces.CreateCommand())
		root.AddCommand(workspaces.DeleteCommand())
		root.AddCommand(workspaces.RecoverCommand())
		root.AddCommand(workspaces.CloneCommand())
		// Internal subcommands wired up by the fzf transforms in pickers.
		root.AddCommand(workspaces.DeletePromptCommand())
		root.AddCommand(workspaces.DeleteRowCommand())
		root.AddCommand(workspaces.SessionListCommand())
		root.AddCommand(workspaces.RecoverRowsCommand())
		root.AddCommand(workspaces.RecoverDeletePromptCommand())
		root.AddCommand(workspaces.RecoverDeleteRowCommand())
		root.AddCommand(workspaces.AutoSessionCommand())
		root.AddCommand(workspaces.PromptCommand())
		root.AddCommand(workspaces.BuildCommand())
		root.AddCommand(workspaces.NameCommand())
		root.AddCommand(workspaces.BgPullCommand())
	})
}
