package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
)

// usage is the asynchronous tmux status job, or Claude's status-line capture.
// Rendering never waits on the network: tmux sees the cache immediately, and a
// fresh Codex reading replaces it when the metadata fetch finishes.
func usage(args []string) error {
	if arg(args, 0) == "claude" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 2<<20))
		if err != nil {
			return err
		}
		_ = agent.CaptureClaudeUsage(data, time.Now())
		if original := arg(args, 1); original != "" {
			decoded, err := base64.StdEncoding.DecodeString(original)
			if err != nil {
				return err
			}
			cmd := exec.Command("/bin/sh", "-c", string(decoded))
			cmd.Stdin = bytes.NewReader(data)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			return cmd.Run()
		}
		return nil
	}
	printUsage := func() {
		fmt.Println(agent.UsageStatus(agent.LoadUsage(agent.Claude), agent.LoadUsage(agent.Codex), time.Now()))
	}
	printUsage()
	_ = agent.RefreshCodexUsage()
	printUsage()
	return nil
}

// Existing Claude sessions reload a changed project-local status command.
// Capture is installed for all spaces, including those currently on Codex.
func syncUsageCapture() {
	for _, ws := range core.Load().Workspaces {
		if !ws.Retired {
			_ = agent.InstallClaudeUsage(ws.Root())
		}
	}
}

// usageJob pins the paths onto the tmux job, including the dev instance's cache
// and socket. A long-lived tmux server need not have inherited today's env.
func usageJob() string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	cmd := "env ATELIER_SOCKET=" + quote(core.LoadConfig().Socket)
	for _, key := range []string{"XDG_CACHE_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "PATH"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd += " " + key + "=" + quote(value)
		}
	}
	cmd += " " + quote(self()) + " usage"
	// The result is embedded in a double-quoted tmux config value.
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(cmd)
}
