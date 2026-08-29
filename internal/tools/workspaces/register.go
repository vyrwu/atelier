package workspaces

import (
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
)

// Manifest is workspaces' registry descriptor. In the intent-workspace model
// the primary action is M-n (create from an intent), M-s picks among live
// workspaces, and M-c is the cross-repo Changes (PR) view.
var Manifest = &manifest.Manifest{
	Tool:          true,
	Name:          "workspaces",
	Description:   "Intent-first workspaces: create (M-n), switch (M-s), review changes (M-c)",
	PrimaryInvoke: "sessions",
	Binding: &manifest.Binding{
		Key:         "M-n",
		Title:       "New workspace",
		Style:       manifest.StylePicker,
		Invoke:      "new",
		AlsoInPopup: true,
	},
	Bindings: []manifest.Binding{
		{Key: "M-s", Title: "Active workspaces", Style: manifest.StylePicker, Invoke: "sessions", AlsoInPopup: true},
		{Key: "M-c", Title: "List changes", Style: manifest.StylePicker, Invoke: "changes", AlsoInPopup: true},
	},
	UI: &manifest.UI{
		Icon:        "栽",
		AccentColor: "168",
		PopupTitle:  "Select Workspace",
	},
	Popup:    manifest.KindNone,
	Requires: []string{"git", "fzf"},
	PickerBindings: []manifest.PickerBinding{
		// new (intent prompt — M-n)
		{Picker: "new", Key: "Enter", Action: "Create workspace from the intent (empty → cancel)"},
		{Picker: "new", Key: "M-s", Action: "Jump to active workspaces"},
		{Picker: "new", Key: "M-c", Action: "Jump to changes"},
		// sessions (Active Workspaces — M-s)
		{Picker: "sessions", Key: "Enter", Action: "Switch to workspace / confirm action"},
		{Picker: "sessions", Key: "M-x", Action: "Delete workspace (confirm enumerates worktrees + PRs)"},
		{Picker: "sessions", Key: "M-r", Action: "Rename workspace"},
		{Picker: "sessions", Key: "M-t", Action: "Tag workspace (pick/create; empty clears)"},
		{Picker: "sessions", Key: "M-p", Action: "Pin/unpin the search scope"},
		{Picker: "sessions", Key: "M-n", Action: "Jump to new-workspace creator"},
		{Picker: "sessions", Key: "M-c", Action: "Jump to changes"},
		// changes (List Changes — M-c)
		{Picker: "changes", Key: "M-o", Action: "Open the PR in a browser"},
		{Picker: "changes", Key: "M-c", Action: "Close the PR (confirm)"},
		{Picker: "changes", Key: "Enter", Action: "Open the PR in a browser"},
		{Picker: "changes", Key: "M-s", Action: "Jump to active workspaces"},
		{Picker: "changes", Key: "M-n", Action: "Jump to new-workspace creator"},
	},
}

// AddCommands wires workspaces' subcommands (including internal fzf-transform
// helpers) onto the dispatch root.
func AddCommands(root *cobra.Command) {
	// User-facing pickers.
	root.AddCommand(NewCommand())
	root.AddCommand(SessionsCommand())
	root.AddCommand(ChangesCommand())
	// Intent-creation internals.
	root.AddCommand(BuildCommand())
	root.AddCommand(NameCommand())
	// M-s picker internals.
	root.AddCommand(SessionListCommand())
	root.AddCommand(DeletePromptCommand())
	root.AddCommand(DeleteRowCommand())
	root.AddCommand(RenameCommand())
	root.AddCommand(TagCommand())
	root.AddCommand(TagPreviewCommand())
	root.AddCommand(SetScopePinCommand())
	root.AddCommand(MSListenCommand())
	// M-c Changes internals + forge slot.
	root.AddCommand(ChangesListCommand())
	root.AddCommand(OpenForgeCommand())
	root.AddCommand(PRClosePromptCommand())
	root.AddCommand(CloseForgeCommand())
	root.AddCommand(ForgeRefreshCommand())
	// Background refresh daemon.
	root.AddCommand(RefreshLoopCommand())
}
