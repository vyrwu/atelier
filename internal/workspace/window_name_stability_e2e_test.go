//go:build e2e

package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRestore_DriverWindowNameStableAcrossCycles guards against driver-window
// name drift across N restore cycles. Restore derives the driver window name
// from the workspace slug ("fake/repo" → "repo"); if tmux's automatic-rename
// (ON by default) renames it to the running shell, the next save round-trip
// would capture the drifted name. ThemeBlock sets `automatic-rename off` to
// prevent this; the test confirms the name is stable cycle to cycle.
func TestRestore_DriverWindowNameStableAcrossCycles(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ATELIER_WORKSPACE_ROOT", t.TempDir())

	root := t.TempDir()
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			Title:       "fake",
			Root:        root,
			Windows: []statestore.Window{
				{Name: "repo", Cwd: root},
			},
		}},
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// "fake/repo" → driver window name "repo".
	const wantName = "repo"

	for i := 0; i < 3; i++ {
		srv := testtmux.New(t)
		srv.NewSession("seed") // boot the server

		if err := workspace.Restore(srv.Client); err != nil {
			t.Fatalf("cycle %d: workspace.Restore: %v", i, err)
		}

		out, err := srv.Client.Run("list-windows",
			"-t", "=fake/repo", "-F", "#{window_name}")
		if err != nil {
			t.Fatalf("cycle %d: list-windows: %v", i, err)
		}
		if got := strings.TrimSpace(string(out)); got != wantName {
			t.Fatalf("cycle %d: window name = %q, want %q (auto-rename leak?)",
				i, got, wantName)
		}

		// Trigger a write-through and confirm the cached window keeps its name.
		if err := workspace.SetRecap(srv.Client, getWindowID(t, srv, "fake/repo"),
			"cycle test"); err != nil {
			t.Fatalf("cycle %d: SetRecap: %v", i, err)
		}

		state, err := statestore.Load()
		if err != nil {
			t.Fatalf("cycle %d: statestore.Load: %v", i, err)
		}
		ws := state.FindWorkspace("fake/repo")
		if ws == nil || len(ws.Windows) == 0 {
			t.Fatalf("cycle %d: fake/repo missing or has no windows in cache", i)
		}
		if ws.Windows[0].Name != wantName {
			t.Fatalf("cycle %d: cache window name = %q, want %q (drift!)",
				i, ws.Windows[0].Name, wantName)
		}
	}
}

func getWindowID(t *testing.T, srv *testtmux.Server, session string) string {
	t.Helper()
	out, err := srv.Client.Run("display-message", "-p",
		"-t", session, "#{window_id}")
	if err != nil {
		t.Fatalf("display-message: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestThemeBlock_DisablesAutomaticRename locks in the theme-level invariant
// directly so a future "let's enable automatic-rename for fancy reasons"
// change fails CI rather than silently breaking the persistence layer.
func TestThemeBlock_DisablesAutomaticRename(t *testing.T) {
	for _, dir := range []string{
		"../initgen",
	} {
		path := filepath.Join(dir, "bindings.go")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !strings.Contains(string(data), "automatic-rename off") {
			t.Errorf("%s: missing `automatic-rename off` directive — "+
				"persistence layer relies on tmux NOT renaming windows", path)
		}
		if !strings.Contains(string(data), "allow-rename off") {
			t.Errorf("%s: missing `allow-rename off` directive — "+
				"persistence layer relies on tmux NOT honoring shell rename escapes", path)
		}
	}
}
