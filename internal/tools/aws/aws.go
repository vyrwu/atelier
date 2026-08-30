// Package aws is atelier's granted profile picker — it assumes an AWS profile
// in the caller pane using granted's `assume`.
//
// Behavior:
//   - fzf prompt `サ ` yellow, label ` AWS Assume `
//   - Profiles come from `~/.aws/config` (or $AWS_CONFIG_FILE) — the same
//     source granted reads.
//   - On selection: tmux respawn-pane -k <CALLER_PANE>
//     `$SHELL -i -c 'assume '<profile>'; exec $SHELL'`
//     `assume` is a sourced shell function, so it must run in an interactive
//     shell; the credentials it exports persist in the pane's shell (unlike
//     aws-vault's subshell), and `exec $SHELL` keeps the pane alive.
//   - CALLER_PANE comes from atelier global state (set when the popup is
//     opened) OR from _CALLER_PANE global env var as a fallback.
package aws

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/awsassume"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/tui"
)

func PickCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "pick",
		Short: "Pick an AWS profile and assume it in the caller pane with granted",
		RunE: func(_ *cobra.Command, _ []string) error {
			if _, err := exec.LookPath("granted"); err != nil {
				return fmt.Errorf("granted not on PATH (install granted and enable its shell integration): %w", err)
			}
			profiles, err := ListProfiles()
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				return fmt.Errorf("no AWS profiles configured in ~/.aws/config")
			}
			items := make([]list.Item, 0, len(profiles))
			for _, p := range profiles {
				items = append(items, tui.SimpleItem{
					IDStr:    p,
					TitleStr: p,
					Filter:   p,
				})
			}
			outcome, err := tui.Run(tui.NewList(tui.NewTheme(tui.ColYellow), " AWS Assume ", items))
			if err != nil {
				if errors.Is(err, tui.ErrCancelled) {
					return nil
				}
				return err
			}
			picked := outcome.Selection
			h := tmuxhost.New(socket)

			target := resolveCallerPane(h)
			if target == "" {
				return fmt.Errorf("aws picker: caller pane not set")
			}

			shellCmd := awsassume.PickerCmd(picked, awsassume.DefaultShell())
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
		// Guard the respawn target: when Capture can't validate the outer
		// pointer it falls back OuterPane = CurrentPane. Inside the aws popup
		// that current pane IS the picker's own pane, so respawning it with
		// -k would kill the picker and run `assume` in the ephemeral popup
		// (creds lost). Only trust OuterPane as a caller pane when it's a real
		// outer, not the popup we're running in.
		if !s.InPopup || s.OuterPane != s.CurrentPane {
			return s.OuterPane
		}
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
		Short: "List AWS profiles from ~/.aws/config",
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

// ListProfiles returns the AWS profile names granted can assume, read from
// $AWS_CONFIG_FILE or ~/.aws/config.
func ListProfiles() ([]string, error) {
	path := os.Getenv("AWS_CONFIG_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".aws", "config")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read AWS config %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return parseAWSProfiles(f), nil
}

// parseAWSProfiles extracts profile names from an ~/.aws/config stream.
// `[default]` yields "default"; `[profile NAME]` yields NAME. Other sections
// (`[sso-session ...]`, `[services ...]`) are ignored. File order is kept.
func parseAWSProfiles(r io.Reader) []string {
	var profiles []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
			continue
		}
		section := strings.TrimSpace(line[1 : len(line)-1])
		switch {
		case section == "default":
			profiles = append(profiles, "default")
		case strings.HasPrefix(section, "profile "):
			if name := strings.TrimSpace(strings.TrimPrefix(section, "profile ")); name != "" {
				profiles = append(profiles, name)
			}
		}
	}
	return profiles
}
