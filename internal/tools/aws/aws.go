// Package aws is atelier's aws-vault profile picker — the bash-exact port
// of tmux_aws_picker.
//
// Behavior:
//   - fzf prompt `サ ` yellow, label ` AWS Profile `
//   - On selection: tmux respawn-pane -k <CALLER_PANE>
//     "aws-vault exec '<profile>' -- $SHELL; exec $SHELL"
//   - CALLER_PANE comes from atelier global state (set when the popup is
//     opened) OR from _CALLER_PANE global env var as a fallback.
package aws

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/fzfstyle"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

func PickCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "pick",
		Short: "Pick an aws-vault profile and respawn the caller pane under aws-vault exec (bash-exact)",
		RunE: func(_ *cobra.Command, _ []string) error {
			profiles, err := ListProfiles()
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				return fmt.Errorf("no aws-vault profiles configured")
			}
			args := fzfstyle.Args("サ ", "AWS Profile", "yellow",
				fzfstyle.WithCustomColor("prompt:yellow:bold,pointer:yellow,query:yellow,hl:yellow,hl+:yellow:bold,label:103,border:103,footer:103"),
			)
			picked, err := fzf.Pick(profiles, args...)
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					return nil
				}
				return err
			}
			h := tmuxhost.New(socket)

			target := resolveCallerPane(h)
			if target == "" {
				return fmt.Errorf("aws picker: caller pane not set")
			}

			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = "/bin/zsh"
			}
			// `exec $SHELL` after aws-vault keeps the pane alive when the
			// user exits the sub-shell (matches bash).
			shellCmd := fmt.Sprintf(`aws-vault exec '%s' -- %s; exec %s`,
				strings.ReplaceAll(picked, `'`, `'\''`), shell, shell)
			_, err = h.Run("respawn-pane", "-k", "-t", target, shellCmd)
			return err
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// resolveCallerPane prefers the atelier state OuterPane, then falls back to
// the bash-style global env var `_CALLER_PANE` (used by the standalone
// `tmux_aws_picker` bash script).
func resolveCallerPane(h *tmuxhost.Client) string {
	if s, err := state.Capture(h); err == nil && s.OuterPane != "" {
		return s.OuterPane
	}
	out, err := h.Run("show-environment", "-g", "_CALLER_PANE")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "_CALLER_PANE=") {
			return strings.TrimPrefix(line, "_CALLER_PANE=")
		}
	}
	return ""
}

func ListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List aws-vault profiles",
		RunE: func(cmd *cobra.Command, _ []string) error {
			profiles, err := ListProfiles()
			if err != nil {
				return err
			}
			for _, p := range profiles {
				fmt.Fprintln(cmd.OutOrStdout(), p)
			}
			return nil
		},
	}
}

func ListProfiles() ([]string, error) {
	if _, err := exec.LookPath("aws-vault"); err != nil {
		return nil, fmt.Errorf("aws-vault not on PATH: %w", err)
	}
	cmd := exec.Command("aws-vault", "list", "--profiles")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("aws-vault list: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	var profiles []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		profiles = append(profiles, line)
	}
	return profiles, nil
}
