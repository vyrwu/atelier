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

// TestRestore_WindowNamePersistsAcrossMultipleCycles guards against
// window-name drift across N save → kill → restore cycles. atelier's
// persistent identity is (session_name, window_name); if anything
// silently mutates the window name between save and restore (e.g.
// tmux's `automatic-rename` reflecting the running shell), the
// cache entry becomes stale and the next save round-trip writes a
// different name. After N cycles, the identity has drifted away
// from the user's intent.
//
// Real-world trigger we hit: tmux's `automatic-rename` is ON by
// default. Restore creates window with `-n main`, but as soon as
// the shell runs, tmux renames it to `zsh`. Next cycle the cache
// gets `name: "zsh"` instead of `name: "main"`. ThemeBlock now
// sets `automatic-rename off` to prevent this; the test below
// confirms the invariant.
func TestRestore_WindowNamePersistsAcrossMultipleCycles(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Seed the cache with a workspace whose window is named "main".
	repoDir := t.TempDir()
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: "fake/repo",
			RepoPath:    repoDir,
			Kind:        "default-branch",
			Windows: []statestore.Window{
				{Name: "main", Cwd: repoDir, Branch: "main"},
			},
		}},
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// Three cycles: restore against a fresh tmux server, verify
	// window name is still "main" in both live tmux state AND
	// in the cache after a write-through update.
	for i := 0; i < 3; i++ {
		srv := testtmux.New(t)
		srv.NewSession("seed") // boot the server

		if err := workspace.Restore(srv.Client); err != nil {
			t.Fatalf("cycle %d: workspace.Restore: %v", i, err)
		}

		// Assert the restored session has a window named "main".
		out, err := srv.Client.Run("list-windows",
			"-t", "=fake/repo", "-F", "#{window_name}")
		if err != nil {
			t.Fatalf("cycle %d: list-windows: %v", i, err)
		}
		got := strings.TrimSpace(string(out))
		if got != "main" {
			t.Fatalf("cycle %d: window name = %q, want %q (auto-rename leak?)",
				i, got, "main")
		}

		// Trigger a write-through (simulating any of the
		// SetRecap / SetAttention / RegisterCreatedWorkspace
		// paths the user's actions trigger). The window name
		// in the cache should remain "main", not whatever tmux
		// might have renamed the window to.
		if err := workspace.SetRecap(srv.Client, getWindowID(t, srv, "fake/repo"),
			"cycle test"); err != nil {
			t.Fatalf("cycle %d: SetRecap: %v", i, err)
		}

		// Reload cache and confirm.
		state, err := statestore.Load()
		if err != nil {
			t.Fatalf("cycle %d: statestore.Load: %v", i, err)
		}
		if state == nil || len(state.Workspaces) == 0 {
			t.Fatalf("cycle %d: cache empty after write-through", i)
		}
		var ws *statestore.Workspace
		for j := range state.Workspaces {
			if state.Workspaces[j].SessionName == "fake/repo" {
				ws = &state.Workspaces[j]
				break
			}
		}
		if ws == nil {
			t.Fatalf("cycle %d: fake/repo not in cache", i)
		}
		if len(ws.Windows) == 0 {
			t.Fatalf("cycle %d: workspace has no windows in cache", i)
		}
		if ws.Windows[0].Name != "main" {
			t.Fatalf("cycle %d: cache window name = %q, want %q (drift!)",
				i, ws.Windows[0].Name, "main")
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

// TestThemeBlock_DisablesAutomaticRename locks in the theme-level
// invariant directly so a future "let's enable automatic-rename for
// fancy reasons" change fails CI rather than silently breaking the
// persistence layer.
func TestThemeBlock_DisablesAutomaticRename(t *testing.T) {
	// Read the theme block source to confirm the directive is present.
	// (The full e2e test above proves it's effective; this is the
	// cheap unit-level grep that catches accidental removal.)
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
