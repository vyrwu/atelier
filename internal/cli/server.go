package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// ServerCommand is the `atelier server` subcommand group: lifecycle
// operations on the atelier tmux server itself. The default user gesture
// is detach-by-default — `atelier server quit` (or M-q) leaves the server
// alive so background agents (Claude popups, watch loops) keep running.
// `atelier server kill` is the explicit "force quit" for cleanup.
//
// Why a group, not three top-level commands: these all act on the
// server-as-a-whole and benefit from being adjacent in help output and
// shell completion. They're also the natural surface to grow GC /
// status / lock commands into.
func ServerCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "server",
		Short: "Lifecycle operations on the atelier tmux server",
	}
	c.AddCommand(serverQuitCmd())
	c.AddCommand(serverKillCmd())
	c.AddCommand(serverGCCmd())
	return c
}

// serverQuitCmd implements the detach-by-default exit. When invoked from
// a popup (where `client_name` is the popup pty, not the user's outer
// terminal), it reads @atelier_outer_client and detaches THAT client by
// name — so M-q from inside a Claude popup still exits atelier instead
// of just dismissing the popup.
//
// When invoked from a regular pane (root M-q binding), @atelier_outer_client
// may be empty / stale; we fall back to bare `detach-client` which
// detaches whatever client called us — i.e. the user's outer terminal.
//
// tmux's detach-client uses `-t <target-client>` to select the client by
// name (-c is for switch-client / display-popup origin-client — different
// flag for a different purpose).
func serverQuitCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "quit",
		Short: "Detach the outer client — keeps the tmux server alive (background agents survive)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			outer, _ := h.ShowGlobalOption("@atelier_outer_client")
			outer = strings.TrimSpace(outer)
			args := []string{"detach-client"}
			if outer != "" {
				args = append(args, "-t", outer)
			}
			_, err := h.Run(args...)
			return err
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// serverKillCmd is the explicit force-quit. Kills the whole tmux server
// (and everything in it — background agents included), then removes the
// stale socket file. Use when the user wants a true reset, not just a
// detach.
func serverKillCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "kill",
		Short: "Force-kill the atelier tmux server (terminates background agents)",
		RunE: func(_ *cobra.Command, _ []string) error {
			tmuxBin, err := exec.LookPath("tmux")
			if err != nil {
				return fmt.Errorf("tmux not found on PATH: %w", err)
			}
			sock := socket
			if sock == "" {
				sock = "atelier"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			kill := exec.CommandContext(ctx, tmuxBin, "-L", sock, "kill-server")
			_ = kill.Run() // best-effort; server may already be gone

			sockPath := tmuxSocketPath(sock)
			if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale socket %s: %w", sockPath, err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket name (default: atelier)")
	return c
}

// serverGCCmd surfaces hostpopup.CleanupOrphanedPopups as a user-facing
// command. The hook-driven cleanup + boot sweep cover the typical cases;
// this is the safety valve for when popup sessions accumulate across a
// long-lived server (FR-5.3 + FR-5.5).
func serverGCCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "gc",
		Short: "Sweep orphan popup-backing sessions (_atelier_*, _claudepop_*, …)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			before, _ := h.ListSessions()
			beforeOrphans := countPopupSessions(before)
			if err := hostpopup.CleanupOrphanedPopups(h); err != nil {
				return err
			}
			after, _ := h.ListSessions()
			afterOrphans := countPopupSessions(after)
			fmt.Fprintf(cmd.OutOrStdout(), "swept %d orphan(s)\n", beforeOrphans-afterOrphans)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func countPopupSessions(sessions []string) int {
	n := 0
	for _, s := range sessions {
		if strings.HasPrefix(s, "_atelier_") || strings.HasPrefix(s, "_claudepop_") ||
			strings.HasPrefix(s, "_popup_") || strings.HasPrefix(s, "_k8spop_") ||
			strings.HasPrefix(s, "_awspop_") || strings.HasPrefix(s, "_lazygitpop_") {
			n++
		}
	}
	return n
}
