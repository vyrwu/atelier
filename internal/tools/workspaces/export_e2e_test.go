//go:build e2e

package workspaces

import "github.com/vyrwu/atelier/internal/tmuxhost"

// DeleteRow re-exports the in-process delete used by the bubbletea M-s
// picker so external-package e2e tests can drive it directly (the old
// `_delete-row` shell subcommand is gone).
func DeleteRow(h *tmuxhost.Client, session, window string) error {
	return deleteRow(h, session, window)
}
