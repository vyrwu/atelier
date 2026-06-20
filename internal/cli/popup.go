// Package cli holds shared cobra command builders used by cmd/atelier.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/dispatch"
	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/plugin"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

func PopupCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "popup",
		Short: "Popup orchestration utilities",
	}
	c.AddCommand(outerCmd())
	c.AddCommand(gotoToolCmd())
	c.AddCommand(cleanupCmd())
	return c
}

// gotoToolCmd is the popup-table binding entry point. Pressed from inside
// any popup, it opens the named tool on the outer client with that tool's
// preferred popup style (via tmux_outer_popup-equivalent dance).
//
// Used by the init-generated `bind -T popup` lines for M-;, M-n, M-s.
func gotoToolCmd() *cobra.Command {
	var (
		name   string
		invoke string
		socket string
	)
	c := &cobra.Command{
		Use:   "goto-tool",
		Short: "Open the named tool on the outer client (called from popup-table bindings)",
		Long: `Open the named tool on the outer (non-popup) client with the tool's
preferred popup style. Used by popup-table bindings (M-; / M-n / M-s)
to switch contexts without ever issuing a bare detach-client that would
unattach the user's main session.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required (atelier-<name>)")
			}
			res, err := plugin.Discover()
			if err != nil {
				return err
			}
			p, ok := res.FindByName(name)
			if !ok {
				return fmt.Errorf("no atelier-%s on PATH", name)
			}
			if invoke == "" {
				invoke = p.Manifest.Primary()
			}
			h := tmuxhost.New(socket)
			styleArgs := hostpopup.PopupStyleArgs(p.Manifest.Binding)
			fullInvoke := dispatch.ToolCmd(name, invoke)
			// Open the target tool as a NESTED popup overlay on the
			// outer client — do NOT detach the origin tool's popup. The
			// user might just be peeking; if they dismiss, they return
			// to the origin. If they pick another tool from the selector,
			// THAT picked-tool's dispatch (SelectCommand.dispatch) calls
			// hostpopup.OpenOnOuter which handles the detach.
			return hostpopup.OverlayOnOuter(h, styleArgs, fullInvoke)
		},
	}
	c.Flags().StringVar(&name, "name", "", "tool name (e.g. toolselector, workspaces)")
	c.Flags().StringVar(&invoke, "invoke", "", "subcommand (default: tool's PrimaryInvoke)")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func outerCmd() *cobra.Command {
	var (
		socket string
		width  string
		height string
	)
	c := &cobra.Command{
		Use:   "outer <command> [args...]",
		Short: "Run a command as a popup on the outer (non-popup) client",
		Long: `Render <command> as a tmux display-popup on the outermost (non-popup)
workspace client that began the current popup chain. The current popup
stays open (requires tmux 3.4+ for popup nesting).

Replaces bash's tmux_outer_popup with a state-aware mechanism that doesn't
need to detach the inner popup first.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			h := tmuxhost.New(socket)
			s, err := state.Capture(h)
			if err != nil {
				return err
			}
			shellCmd := strings.Join(args, " ")
			return hostpopup.OpenOuter(h, s, shellCmd, width, height)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	c.Flags().StringVar(&width, "width", "70%", "popup width")
	c.Flags().StringVar(&height, "height", "70%", "popup height")
	return c
}

func cleanupCmd() *cobra.Command {
	var (
		socket  string
		startup bool
	)
	c := &cobra.Command{
		Use:   "cleanup",
		Short: "Kill orphaned popup sessions (called from tmux hooks)",
		Long: `Kill any atelier-managed popup session whose parent window or session
no longer exists.

Wire this into tmux.conf via set-hook:

    set-hook -g window-unlinked 'run-shell "atelier popup cleanup"'
    set-hook -g session-closed  'run-shell "atelier popup cleanup"'

The bundled launcher also invokes this with --startup once at fresh-
server boot (belt-and-suspenders sweep for orphans that survived a
crash). The --startup path is a no-op on testtmux sockets (prefix
"atelier-test-") so tests that create orphan-by-construction popup
fixtures don't have them GC'd out from under them.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if startup && strings.HasPrefix(os.Getenv("ATELIER_TMUX_SOCKET"), "atelier-test-") {
				return nil
			}
			return hostpopup.CleanupOrphanedPopups(tmuxhost.New(socket))
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	c.Flags().BoolVar(&startup, "startup", false,
		"called from the bundled launcher's startup sweep (enables test-socket bypass)")
	return c
}

// runtimeStateDebugCmd prints the captured runtime state — formerly the
// top-level `atelier state`, now nested as `atelier state debug` after
// the persistence subcommands joined the group.
func runtimeStateDebugCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "debug",
		Short: "Print atelier runtime state (debugging)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			s, err := state.Capture(h)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "CurrentPane:    %s\n", s.CurrentPane)
			fmt.Fprintf(out, "CurrentSession: %s\n", s.CurrentSession)
			fmt.Fprintf(out, "CurrentWindow:  %s\n", s.CurrentWindow)
			fmt.Fprintf(out, "CurrentName:    %s\n", s.CurrentName)
			fmt.Fprintf(out, "InPopup:        %v\n", s.InPopup)
			if s.InPopup {
				fmt.Fprintf(out, "PopupTool:      %s\n", s.PopupTool)
			}
			fmt.Fprintf(out, "OuterPane:      %s\n", s.OuterPane)
			fmt.Fprintf(out, "OuterSession:   %s\n", s.OuterSession)
			fmt.Fprintf(out, "OuterWindow:    %s\n", s.OuterWindow)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
