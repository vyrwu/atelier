package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var binaryOnce sync.Once
var binaryPath string

// Binary keeps agent/MCP PATH changes from selecting an incompatible client.
// Older instances have no pin: try the other installed clients only when the
// first client cannot contact an existing server. Never start or restart it.
func Binary() string {
	if path := os.Getenv("ATELIER_TMUX"); path != "" {
		return path
	}
	binaryOnce.Do(func() {
		binaryPath = "tmux"
		seen := map[string]bool{}
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			path, err := exec.LookPath(filepath.Join(dir, "tmux"))
			if err != nil {
				continue
			}
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				path = resolved
			}
			if seen[path] {
				continue
			}
			seen[path] = true
			if binaryPath == "tmux" {
				binaryPath = path
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			// #nosec G204 G702 -- Executable is a tmux client found on the user's PATH; fixed read-only arguments.
			out, err := exec.CommandContext(ctx, path, "-N", "-L", socket(), "display-message", "-p", "#{pid}").CombinedOutput()
			cancel()
			if err == nil || serverAbsent(strings.TrimSpace(string(out))) {
				binaryPath = path
				return
			}
		}
	})
	return binaryPath
}
