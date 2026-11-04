package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
)

// usage is the asynchronous tmux status job, an agent's refresh after a turn
// (see hook), or Claude's status-line capture. Rendering never waits on the
// network: tmux sees the cache immediately, and fresh readings replace it when
// the metadata fetches finish.
func usage(args []string) error {
	if arg(args, 0) == "refresh" { // atelier usage refresh <agent>
		return agent.RefreshUsage(agent.Of(arg(args, 1)))
	}
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
	status := func() string {
		return agent.UsageStatus(agent.LoadUsage(agent.Claude), agent.LoadUsage(agent.Codex), time.Now())
	}
	prior := status()
	fmt.Println(prior)
	// Agents refresh their own reading when a turn ends. Fetch here only what
	// has gone stale without one, so idle agents cost no fetches every minute.
	var refresh sync.WaitGroup
	for _, k := range []agent.Kind{agent.Claude, agent.Codex} {
		if time.Since(agent.LoadUsage(k).Observed) > agent.UsageStale {
			refresh.Go(func() { _ = agent.RefreshUsage(k) })
		}
	}
	refresh.Wait()
	if current := status(); current != prior {
		fmt.Println(current)
	}
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
	// A removed override must also disappear from the old server's environment.
	cmd := "env -u XDG_CACHE_HOME -u CLAUDE_CONFIG_DIR -u CODEX_HOME -u PATH ATELIER_SOCKET=" + quote(core.LoadConfig().Socket)
	for _, key := range []string{"XDG_CACHE_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "PATH"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd += " " + key + "=" + quote(value)
		}
	}
	cmd += " " + quote(self()) + " usage"
	// The result is embedded in a double-quoted tmux config value.
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(cmd)
}
