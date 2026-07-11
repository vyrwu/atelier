package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// StateCommand is the `atelier state` subcommand group. Houses both
// the runtime-state debug printout (`atelier state debug`) and the
// on-disk persistence operations (restore, save, hook callbacks).
//
// Atelier's persistent state cache lives at
// $XDG_CACHE_HOME/atelier/state.json and survives tmux
// server restarts — restore reads it; the tmux hooks emitted by
// `atelier init` invoke the remove/rename subcommands to keep it
// honest when the user kills or renames sessions outside atelier.
func StateCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "state",
		Short: "Atelier runtime + persisted state operations",
	}
	c.AddCommand(runtimeStateDebugCmd())
	c.AddCommand(stateRestoreCmd())
	c.AddCommand(stateSaveCmd())
	c.AddCommand(stateSyncCmd())
	c.AddCommand(stateRemoveSessionCmd())
	c.AddCommand(stateRemoveWindowCmd())
	c.AddCommand(stateRenameWindowCmd())
	return c
}

func stateSyncCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "sync",
		Short: "Reconcile cache against current tmux state (removes orphans)",
		Long: `Diffs the persisted state cache against current tmux state and
removes any cache entries for sessions/windows that no longer exist.
Idempotent. Wired into the session-closed and window-unlinked tmux
hooks emitted by 'atelier init', so the cache stops accumulating
ghosts when the user kills sessions or windows outside atelier.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return workspace.SyncCache(tmuxhost.New(socket))
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func stateRestoreCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "restore",
		Short: "Recreate missing sessions+windows from the persisted cache",
		Long: `Read the on-disk state cache and reproduce any workspaces / windows
/ window-options it lists. Idempotent — sessions that already exist
are left alone. Workspaces whose worktree directory is gone get
skipped (logged). Globals (@atelier_k8s_active, @atelier_pgcli_active)
are restored too.

Invoked automatically from the tmux config block emitted by
'atelier init' so every tmux server start restores prior state.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return workspace.Restore(tmuxhost.New(socket))
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func stateSaveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "path",
		Short: "Print the persisted state cache file path",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), statestore.Path())
			return nil
		},
	}
	return c
}

func stateRemoveSessionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:    "remove-session <session-name>",
		Short:  "Drop a session from the cache (tmux session-closed hook entry)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return statestore.RemoveSession(args[0])
		},
	}
	return c
}

func stateRemoveWindowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:    "remove-window <session> <window>",
		Short:  "Drop a window from the cache (tmux window-unlinked hook entry)",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return statestore.RemoveWindow(args[0], args[1])
		},
	}
	return c
}

func stateRenameWindowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:    "rename-window <session> <old-name> <new-name>",
		Short:  "Update a window name in the cache (tmux window-renamed hook entry)",
		Hidden: true,
		Args:   cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			return statestore.RenameWindow(args[0], args[1], args[2])
		},
	}
	return c
}
