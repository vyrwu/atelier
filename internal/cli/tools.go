package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/plugin"
)

// ToolsCommand is the dispatcher: `atelier tools <name> [args...]` looks up
// the tool in the registry and runs it in-process — a built-in via its
// registered command tree, a config launcher via its declared popup.
//
// Bare `atelier tools` and `atelier tools list` print the registered tools.
//
// This is intentionally tiny: the core resolves the tool from the static
// registry (built-ins) plus config launchers. There is no PATH scan and
// no subprocess manifest protocol.
func ToolsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:                "tools <name> [args...]",
		Short:              "Dispatch to a registered atelier tool",
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
				return fmt.Errorf("unknown tool %q (run `atelier tools list`)", toolName)
			}
			return p.Dispatch(rest)
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
		kind := "built-in"
		if !p.IsBuiltin() {
			kind = "launcher"
		}
		fmt.Fprintf(out, "%-16s  %-9s  %s\n", p.Name, kind, p.Manifest.Description)
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr())
		fmt.Fprintln(cmd.ErrOrStderr(), "Skipped (config errors):")
		for src, err := range res.Skipped {
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s: %v\n", src, err)
		}
	}
	return nil
}
