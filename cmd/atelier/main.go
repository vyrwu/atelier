// Command atelier is the core binary. It knows nothing about any specific
// tool — tools are external `atelier-<name>` binaries discovered on PATH.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/cli"
)

// version is set at build time via -ldflags by goreleaser.
// "dev" is the default for unstamped local builds.
var version = "dev"

func main() {
	// The root command IS the launch command. Running `atelier` with
	// no subcommand spawns the bundled tmux runtime on its dedicated
	// socket — the "primary product" path. Subcommands (init, doctor,
	// tools, workspace, …) are the engine surface for power users
	// and tooling integrations.
	runCmd := cli.RunCommand()
	root := &cobra.Command{
		Use:           "atelier",
		Short:         "Terminal-native agentic development runtime",
		Long: `atelier is a runtime for agentic development that uses tmux as
its display server. Running ` + "`atelier`" + ` with no subcommand boots the
bundled experience on a dedicated tmux socket — themed, wired, and
ready to use.

Subcommands expose the engine for power users:
  atelier init      emit the tmux config (for embedding in an existing tmux)
  atelier doctor    check tmux + every discovered tool's requirements
  atelier tools     discover and dispatch tool plugins
  atelier workspace inspect / list / switch workspaces
  atelier state     inspect runtime + persisted state
  atelier debug     tail / inspect the debug log`,
		SilenceUsage:  true,
		SilenceErrors: false,
		// When invoked without a subcommand, run the bundled launcher.
		// Cobra runs Args validation before this — we set no required
		// args so it's always reachable.
		RunE: runCmd.RunE,
	}

	root.AddCommand(runCmd)
	root.AddCommand(cli.DoctorCommand())
	root.AddCommand(cli.InitCommand())
	root.AddCommand(versionCmd())
	root.AddCommand(cli.ToolsCommand())
	root.AddCommand(cli.WorkspaceCommand())
	root.AddCommand(cli.PopupCommand())
	root.AddCommand(cli.StatusCommand())
	root.AddCommand(cli.StateCommand())
	root.AddCommand(cli.InternalCommand())
	root.AddCommand(cli.DebugCommand())
	root.AddCommand(cli.CheatsheetCommand())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version)
		},
	}
}
