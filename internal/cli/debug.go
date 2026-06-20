package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
)

// DebugCommand groups `atelier debug *` helpers around the always-on
// trace log written by every atelier-* binary.
func DebugCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "debug",
		Short: "Trace-log inspection helpers",
	}
	c.AddCommand(debugPathCmd())
	c.AddCommand(debugTailCmd())
	c.AddCommand(debugLastCmd())
	c.AddCommand(debugClearCmd())
	return c
}

func debugPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the debug.log path",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), debuglog.Path())
		},
	}
}

// debugTailCmd shells out to `tail -F` so the user gets live-follow.
// Using -F (capital) lets it survive log rotation.
func debugTailCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tail",
		Short: "tail -F the debug log (Ctrl-C to exit)",
		RunE: func(_ *cobra.Command, _ []string) error {
			p := debuglog.Path()
			// Touch the file if absent so tail doesn't error.
			if _, err := os.Stat(p); os.IsNotExist(err) {
				if f, ferr := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644); ferr == nil {
					_ = f.Close()
				}
			}
			cmd := exec.Command("tail", "-F", p)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		},
	}
}

// debugLastCmd prints the last N lines (default 200).
func debugLastCmd() *cobra.Command {
	var n int
	c := &cobra.Command{
		Use:   "last",
		Short: "Print the last N debug-log lines (default 200)",
		RunE: func(_ *cobra.Command, _ []string) error {
			p := debuglog.Path()
			cmd := exec.Command("tail", "-n", fmt.Sprintf("%d", n), p)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		},
	}
	c.Flags().IntVarP(&n, "lines", "n", 200, "number of lines")
	return c
}

func debugClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Truncate the debug log (start fresh for a reproduction)",
		RunE: func(_ *cobra.Command, _ []string) error {
			p := debuglog.Path()
			return os.Truncate(p, 0)
		},
	}
}
