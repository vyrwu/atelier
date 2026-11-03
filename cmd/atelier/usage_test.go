package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/core"
)

// A rebuilt binary must backfill capture into pre-existing spaces even when
// tmux already has its current bindings. This is what makes Claude's reading
// appear without restarting old sessions or making Claude the active agent.
func TestRefreshBackfillsClaudeCaptureOnCodexSpace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("ATELIER_ROOT", t.TempDir())
	ws := core.Workspace{Slug: "existing-space", Agent: "codex"}
	if err := os.MkdirAll(ws.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := core.Update(func(s *core.State) { s.Workspaces = []core.Workspace{ws} }); err != nil {
		t.Fatal(err)
	}
	_, version := bindingsFragment()
	original := liveBindings
	liveBindings = func() string { return version }
	t.Cleanup(func() { liveBindings = original })
	refreshBindings()
	data, err := os.ReadFile(filepath.Join(ws.Root(), ".claude", "settings.local.json"))
	if err != nil || !strings.Contains(string(data), "usage claude") {
		t.Fatalf("existing Codex space lacks Claude capture: %s, %v", data, err)
	}
}
