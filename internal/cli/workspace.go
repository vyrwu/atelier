package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// WorkspaceCommand exposes the workspace primitive: list / info / create /
// switch / delete / default-branch / pull-default. These are core commands,
// not tool commands. They operate on absolute paths only — resolving
// repo slugs against a code_root is a plugin concern (workspaces tool's
// `--repo` flag), not a core concern.
func WorkspaceCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "workspace",
		Short: "Workspace primitive (list/info/create/switch/delete + git helpers)",
		Long: `Workspaces are tmux windows with metadata atelier understands directly.
These commands operate on absolute paths and pane/session/window IDs.
The opinionated workflow around workspaces (fzf repo picker, git worktree
creation, recap parsing) lives in the workspaces tool ` + "(`atelier tools workspaces`)" + `, not here.`,
	}
	c.AddCommand(workspaceListCmd(), workspaceInfoCmd(), workspaceCreateCmd(),
		workspaceSwitchCmd(), workspaceDeleteCmd(),
		workspaceDefaultBranchCmd(), workspacePullDefaultCmd())
	return c
}

func workspaceListCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "list",
		Short: "List every workspace (tmux window) across all sessions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			workspaces, err := workspace.List(h)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, w := range workspaces {
				fmt.Fprintf(out, "%s\t%s\t%s\n", w.Target(), w.Session+":"+w.Name, w.Cwd)
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func workspaceInfoCmd() *cobra.Command {
	var (
		pane   string
		socket string
		format string
	)
	c := &cobra.Command{
		Use:   "info",
		Short: "Print info about the workspace containing the given pane",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			w, err := workspace.Info(h, pane)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch format {
			case "json", "":
				data, err := w.AsJSON()
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
			case "name":
				fmt.Fprintln(out, w.Name)
			case "repo":
				fmt.Fprintln(out, w.Repo)
			case "branch":
				fmt.Fprintln(out, w.Branch)
			case "cwd":
				fmt.Fprintln(out, w.Cwd)
			default:
				return fmt.Errorf("unknown --format=%q (want: json, name, repo, branch, cwd)", format)
			}
			return nil
		},
	}
	c.Flags().StringVar(&pane, "pane", "", "tmux pane id (default: current pane)")
	c.Flags().StringVar(&format, "format", "json", "json | name | repo | branch | cwd")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func workspaceCreateCmd() *cobra.Command {
	var (
		dir           string
		name          string
		sessionTarget string
		socket        string
	)
	c := &cobra.Command{
		Use:   "create",
		Short: "Open a new tmux window at dir, named name",
		RunE: func(_ *cobra.Command, _ []string) error {
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			if _, err := os.Stat(dir); err != nil {
				return fmt.Errorf("dir %q does not exist: %w", dir, err)
			}
			if name == "" {
				name = "workspace"
			}
			return workspace.Create(tmuxhost.New(socket), dir, name, sessionTarget)
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "absolute path to the workspace directory")
	c.Flags().StringVar(&name, "name", "", "window name")
	c.Flags().StringVar(&sessionTarget, "session", "", "tmux session to create the window in (default: current)")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func workspaceSwitchCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "switch <target>",
		Short: "Switch client to a target workspace (session:window)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return workspace.Switch(tmuxhost.New(socket), args[0])
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func workspaceDeleteCmd() *cobra.Command {
	var (
		pane   string
		socket string
	)
	c := &cobra.Command{
		Use:   "delete",
		Short: "Kill the window containing the given pane (default: current)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return workspace.Delete(tmuxhost.New(socket), pane)
		},
	}
	c.Flags().StringVar(&pane, "pane", "", "tmux pane id (default: current pane)")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func workspaceDefaultBranchCmd() *cobra.Command {
	var path string
	c := &cobra.Command{
		Use:   "default-branch",
		Short: "Print the default branch (main/master/...) of a repo at --path",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if path == "" {
				return fmt.Errorf("--path is required (absolute repo path)")
			}
			b, err := workspace.DefaultBranch(path)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), b)
			return nil
		},
	}
	c.Flags().StringVar(&path, "path", "", "absolute repo path")
	return c
}

func workspacePullDefaultCmd() *cobra.Command {
	var path string
	c := &cobra.Command{
		Use:   "pull-default",
		Short: "Fetch + fast-forward the default branch of a repo at --path",
		RunE: func(_ *cobra.Command, _ []string) error {
			if path == "" {
				return fmt.Errorf("--path is required (absolute repo path)")
			}
			return workspace.PullDefault(path)
		},
	}
	c.Flags().StringVar(&path, "path", "", "absolute repo path")
	return c
}
