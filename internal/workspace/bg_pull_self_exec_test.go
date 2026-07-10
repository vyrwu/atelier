package workspace

import (
	"os"
	"strings"
	"testing"
)

// TestSpawnBgPull_ReExecsViaToolsDispatch locks the single-binary self-
// re-exec: os.Executable() is `atelier`, so the detached freshness pull
// must be invoked as `atelier tools workspaces _bg-pull`. The old
// basename-patched `atelier-workspaces _bg-pull` form would fail against
// the unified binary. Source-scanned because the exec.Command only fires
// in a detached background process against a live tmux server.
func TestSpawnBgPull_ReExecsViaToolsDispatch(t *testing.T) {
	src, err := os.ReadFile("lifecycle.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if !strings.Contains(string(src), `exec.Command(self, "tools", "workspaces", "_bg-pull"`) {
		t.Errorf("SpawnBgPull must re-invoke via `atelier tools workspaces _bg-pull` " +
			"(exec.Command(self, \"tools\", \"workspaces\", \"_bg-pull\", ...)); not found")
	}
	if strings.Contains(string(src), `atelier-workspaces`) {
		t.Errorf("lifecycle.go still references the removed atelier-workspaces binary name")
	}
}
