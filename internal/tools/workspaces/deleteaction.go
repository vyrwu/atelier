package workspaces

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vyrwu/atelier/internal/dispatch"
)

// Idle prompt glyphs restored after a delete confirm/cancel. Trailing
// space is significant — it's the visible gap before the query cursor.
const (
	sessionsPromptGlyph = "栽 "
	recoverPromptGlyph  = "復 "
)

// fzfDeleteAction builds the fzf action string emitted by a picker's `y`
// and `enter` binds when the delete flow is armed.
//
// Why this is a Go command and not an inline `if…echo` bind: the previous
// binds embedded fzf's `{}` (the whole picked row) inside a double-quoted
// shell `echo "…{}…"`. fzf single-quotes `{}`, but those single quotes are
// literal inside double quotes, so backticks / `$()` in the row's free-form
// AI recap were interpreted by the shell — an unbalanced backtick made the
// entire compound command unparseable, so BOTH enter (accept) and delete
// silently died. Routing through a `transform:` target that takes `{}` as a
// bare (fzf-single-quoted) argument means the recap reaches Go untouched;
// only the clean session+window pair, shell-quoted here, is ever embedded
// back into an executed action — never the recap.
//
// key is the bind key ("y" or "enter"); it only changes the idle-state
// branch (y types a literal y, enter selects the row). prompt is
// $FZF_PROMPT. line is the picked row (field 1 = session/repo, field 2 =
// window/branch, TAB-delimited; later display/recap fields are dropped).
// deleteRowSub/reloadSub are the tool subcommands run on confirm and
// promptGlyph is the picker's idle prompt to restore. Pure.
func fzfDeleteAction(key, prompt, line, deleteRowSub, reloadSub, promptGlyph string) string {
	switch {
	case strings.HasPrefix(prompt, "Confirm"):
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			// Malformed row — restore the idle prompt rather than
			// running a delete against a half-parsed target.
			return "change-prompt(" + promptGlyph + ")"
		}
		// Rebuild session\twindow only — the recap (and its shell/fzf
		// metacharacters) never makes it into the executed action.
		row := shellSingleQuote(fields[0] + "\t" + fields[1])
		return fmt.Sprintf("execute-silent(%s)+reload(%s)+change-prompt(%s)",
			dispatch.ToolCmd("workspaces", deleteRowSub, row),
			dispatch.ToolCmd("workspaces", reloadSub),
			promptGlyph)
	case strings.HasPrefix(prompt, "Cannot"):
		return "change-prompt(" + promptGlyph + ")"
	default:
		if key == "y" {
			return "put(y)"
		}
		return "accept"
	}
}

// shellSingleQuote wraps s in single quotes for safe embedding in a shell
// command, escaping embedded single quotes via the '\” idiom. Pure.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DeleteActionCommand is the session picker's y/enter transform target.
// Invoked as `_delete-action <key> "$FZF_PROMPT" {}`.
func DeleteActionCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_delete-action",
		Short:  "internal: emit the session picker's y/enter fzf action",
		Hidden: true,
		Args:   cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(),
				fzfDeleteAction(args[0], args[1], args[2], "_delete-row", "_session-list", sessionsPromptGlyph))
			return nil
		},
	}
}

// RecoverDeleteActionCommand is the recover picker's y/enter transform
// target. Invoked as `_recover-delete-action <key> "$FZF_PROMPT" {}`.
func RecoverDeleteActionCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_recover-delete-action",
		Short:  "internal: emit the recover picker's y/enter fzf action",
		Hidden: true,
		Args:   cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(),
				fzfDeleteAction(args[0], args[1], args[2], "_recover-delete-row", "_recover-rows", recoverPromptGlyph))
			return nil
		},
	}
}
