package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/plugin"
)

// ToolsCommand is the dispatcher: `atelier tools <name> [args...]` discovers
// the binary `atelier-<name>` on PATH and exec()s it with the remaining args.
//
// Bare `atelier tools` and `atelier tools list` print the discovered tools.
//
// This is intentionally tiny: the core does no per-tool logic. Every tool
// is an external binary the core doesn't know about at compile time.
func ToolsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:                "tools <name> [args...]",
		Short:              "Dispatch to an atelier-<name> binary",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] == "list" {
				return listTools(cmd)
			}
			if args[0] == "--help" || args[0] == "-h" {
				return cmd.Help()
			}

			toolName := args[0]
			rest := args[1:]
			res, err := plugin.Discover()
			if err != nil {
				return err
			}
			p, ok := res.FindByName(toolName)
			if !ok {
				return fmt.Errorf("no atelier-%s tool found on PATH (run `atelier tools list`)", toolName)
			}
			return execTool(p.Binary, rest)
		},
	}
	return c
}

func listTools(cmd *cobra.Command) error {
	res, err := plugin.Discover()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, p := range res.Plugins {
		fmt.Fprintf(out, "%-20s  %s\n", p.Name, p.Manifest.Description)
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr())
		fmt.Fprintln(cmd.ErrOrStderr(), "Skipped (manifest errors):")
		for path, err := range res.Skipped {
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s: %v\n", path, err)
		}
	}
	return nil
}

func execTool(binary string, args []string) error {
	bin, err := exec.LookPath(binary)
	if err != nil {
		return err
	}
	argv := append([]string{binary}, args...)
	return syscall.Exec(bin, argv, os.Environ())
}
