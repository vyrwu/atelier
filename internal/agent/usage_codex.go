package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

// RefreshUsage fetches account metadata at most once per minute per agent,
// however many turns end or status lines ask. Neither provider needs a model call or a live
// interactive session, so both balances stay current regardless of focus.
func RefreshUsage(k Kind) error {
	path := usagePath(k)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, ok, err := core.TryLockFile(path + ".lock")
	if err != nil || !ok {
		return err
	}
	defer unlock()
	attempt := path + ".attempt"
	if fi, err := os.Stat(attempt); err == nil && time.Since(fi.ModTime()) < time.Minute {
		return nil
	}
	if err := os.WriteFile(attempt, nil, 0o600); err != nil {
		return err
	}
	var u Usage
	if k == Claude {
		u, err = readClaudeUsage()
	} else {
		u, err = readCodexUsage()
	}
	if err != nil {
		return err // retain the previous observation, visibly marked stale
	}
	// Serialize with status-line capture, which must re-check whether a fresh
	// native reading won the race before writing session headers over it.
	unlockCache, err := lockConfig(path)
	if err != nil {
		return err
	}
	defer unlockCache()
	return saveUsage(k, u)
}

func readCodexUsage() (Usage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://",
		"--disable", "hooks", "-c", "analytics.enabled=false")
	cmd.Env = withoutSpace(os.Environ())
	// Avoid picking up a workspace's .codex config or guide.
	cmd.Dir = filepath.Dir(usagePath(Codex))
	input, err := cmd.StdinPipe()
	if err != nil {
		return Usage{}, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return Usage{}, err
	}
	if err := cmd.Start(); err != nil {
		return Usage{}, err
	}
	defer func() {
		_ = input.Close()
		// EOF lets app-server clean up its own child processes. Bound that
		// shutdown too, so a wedged helper cannot hold the status job open.
		kill := time.AfterFunc(time.Second, func() { _ = cmd.Process.Kill() })
		_ = cmd.Wait()
		kill.Stop()
	}()
	send := json.NewEncoder(input)
	if err := send.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"clientInfo": map[string]string{"name": "atelier", "version": "1.0.0"},
	}}); err != nil {
		return Usage{}, err
	}
	scan := bufio.NewScanner(output)
	scan.Buffer(make([]byte, 4096), 2<<20)
	for scan.Scan() {
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(scan.Bytes(), &msg) != nil || (msg.ID != 1 && msg.ID != 2) {
			continue
		}
		if msg.Error != nil {
			return Usage{}, fmt.Errorf("codex usage: %s", msg.Error.Message)
		}
		if msg.ID == 2 {
			return decodeCodexUsage(msg.Result, time.Now())
		}
		if err := send.Encode(map[string]any{"method": "initialized"}); err != nil {
			return Usage{}, err
		}
		if err := send.Encode(map[string]any{"id": 2, "method": "account/rateLimits/read"}); err != nil {
			return Usage{}, err
		}
	}
	if err := scan.Err(); err != nil {
		return Usage{}, err
	}
	return Usage{}, fmt.Errorf("codex usage: no response")
}
