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
			// #nosec G204 G702 -- Intentionally runs the owner's existing statusLine command, passed by the installed hook.
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
	// has gone stale without one, by each agent's own measure of stale.
	var refresh sync.WaitGroup
	for _, k := range []agent.Kind{agent.Claude, agent.Codex} {
		if time.Since(agent.LoadUsage(k).Observed) > agent.UsageStale(k) {
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

// usageJob is the quota status job.
func usageJob() string { return statusJob("usage") }

// pollJob is the PR status job, for the session tmux is drawing.
func pollJob() string { return statusJob("poll '#{session_name}'") }

// statusJob pins the paths onto a tmux status job running atelier with args,
// including the dev instance's cache and socket. A long-lived tmux server need
// not have inherited today's env.
func statusJob(args string) string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	// A removed override must also disappear from the old server's environment.
	keys := []string{"XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "ATELIER_ROOT", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "PATH"}
	cmd := "env"
	for _, key := range keys {
		cmd += " -u " + key
	}
	cmd += " ATELIER_SOCKET=" + quote(core.LoadConfig().Socket)
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			cmd += " " + key + "=" + quote(value)
		}
	}
	cmd += " " + quote(self()) + " " + args
	// The result is embedded in a double-quoted tmux config value.
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(cmd)
}
