package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// AICommand is the kernel-side entry to the configured AI integration. Each
// subcommand resolves the active adapter (integration.Active().AI) and
// delegates through the port. When no AI integration is configured, every
// subcommand errors clearly rather than doing nothing — predictable.
//
// These are invoked by the workspace views (agent open on land) and by the
// agent's own stop-hook (`atelier ai on-stop`, installed via EnsureHooks).
func AICommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "ai <subcommand>",
		Short: "Drive the configured AI integration (agent, naming, summary, attention)",
		Long: `Kernel entry to the AI integration selected by ` + "`[integrations] ai`" + ` in
config.toml (claude | mock | …). Subcommands delegate to the active
adapter through the kernel's AIIntegration port; with none configured they
error rather than silently no-op.`,
	}
	c.AddCommand(aiOpenCmd(), aiSetPromptCmd(), aiOnStopCmd(), aiRecapCmd())
	return c
}

func activeAI() (integration.AIIntegration, error) {
	ai := integration.Active().AI
	if ai == nil {
		return nil, fmt.Errorf("no AI integration configured (set `[integrations] ai = \"claude\"` in config.toml)")
	}
	return ai, nil
}

func aiOpenCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Open the AI agent in the current workspace popup",
		RunE: func(_ *cobra.Command, _ []string) error {
			ai, err := activeAI()
			if err != nil {
				return err
			}
			return ai.OpenAgent(tmuxhost.New(socket))
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func aiSetPromptCmd() *cobra.Command {
	var window, prompt, kind, socket string
	c := &cobra.Command{
		Use:   "set-prompt",
		Short: "Queue the initial task prompt for the next agent open on a window",
		RunE: func(_ *cobra.Command, _ []string) error {
			ai, err := activeAI()
			if err != nil {
				return err
			}
			h := tmuxhost.New(socket)
			if window == "" {
				s, err := state.Capture(h)
				if err != nil {
					return err
				}
				window = s.OuterWindow
			}
			if window == "" {
				return fmt.Errorf("--window required (no outer window in state)")
			}
			return ai.SetPrompt(h, window, prompt, kind)
		},
	}
	c.Flags().StringVar(&window, "window", "", "tmux window id (default: outer window from state)")
	c.Flags().StringVar(&prompt, "prompt", "", "initial prompt (empty clears it)")
	c.Flags().StringVar(&kind, "kind", "", "workspace kind hint (worktree | multi-repo)")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func aiOnStopCmd() *cobra.Command {
	var window, socket string
	c := &cobra.Command{
		Use:   "on-stop",
		Short: "Agent stop-hook entry: flag attention + refresh summary (reads payload on stdin)",
		RunE: func(_ *cobra.Command, _ []string) error {
			ai, err := activeAI()
			if err != nil {
				return err
			}
			payload, _ := io.ReadAll(os.Stdin)
			return ai.OnStop(tmuxhost.New(socket), window, payload)
		},
	}
	c.Flags().StringVar(&window, "window", "", "target tmux window id (default: resolved from popup context)")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func aiRecapCmd() *cobra.Command {
	var window, project, socket string
	c := &cobra.Command{
		Use:   "recap",
		Short: "Refresh the workspace summary from the agent's latest transcript",
		RunE: func(_ *cobra.Command, _ []string) error {
			ai, err := activeAI()
			if err != nil {
				return err
			}
			h := tmuxhost.New(socket)
			if window == "" {
				s, err := state.Capture(h)
				if err != nil {
					return err
				}
				window = s.OuterWindow
			}
			if window == "" {
				return fmt.Errorf("--window required")
			}
			return ai.Summarize(h, window, project)
		},
	}
	c.Flags().StringVar(&window, "window", "", "tmux window id (default: outer window)")
	c.Flags().StringVar(&project, "project", "", "agent project root (default: workspace cwd)")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}
