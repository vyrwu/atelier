// Package dispatch builds the shell-string invocations of the
// `atelier` core binary that get embedded in tmux bindings, fzf
// --bind actions, and run-shell hooks.
//
// Why this exists: the literal "atelier tools <name> <subcmd> ..."
// invocation was hand-rolled at 10+ callsites across workspaces,
// claude, and the init/popup generators. If the dispatch path
// changes (atelier renamed, subcommand restructured, args reordered),
// every callsite breaks silently — the strings are inert until tmux
// fires them, so unit tests don't catch them.
//
// Centralizing also gives us ONE place to handle quoting, escaping,
// and any future flags the dispatch protocol grows.
package dispatch

import (
	"strings"
)

// CoreBinary is the name of the atelier core binary on PATH.
// If we ever rename or relocate it, one change here propagates
// everywhere via the helpers below.
const CoreBinary = "atelier"

// ToolCmd returns the shell string that dispatches a subcommand of
// a discovered atelier-* tool plugin.
//
//	ToolCmd("workspaces", "sessions")
//	    → "atelier tools workspaces sessions"
//
//	ToolCmd("workspaces", "_delete-row", "{}")
//	    → "atelier tools workspaces _delete-row {}"
//
// Arguments are joined with single spaces; callers that need shell
// quoting (e.g. paths with spaces) should pre-quote.
func ToolCmd(tool string, args ...string) string {
	parts := make([]string, 0, len(args)+3)
	parts = append(parts, CoreBinary, "tools", tool)
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

// CoreCmd returns the shell string for a core subcommand
// (no `tools` segment).
//
//	CoreCmd("state", "restore")
//	    → "atelier state restore"
//
//	CoreCmd("internal", "stamp-statusline")
//	    → "atelier internal stamp-statusline"
func CoreCmd(args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, CoreBinary)
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}
