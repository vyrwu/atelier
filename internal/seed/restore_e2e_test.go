//go:build e2e

package seed_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/seed"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/tools/workspaces"
	"github.com/vyrwu/atelier/internal/workspace"
)

// driverWindow is the driver window name the seed hydrator uses: the last
// segment of the workspace slug.
func driverWindow(slug string) string { return filepath.Base(slug) }

// TestSeed_HydrateThenRestore is the load-bearing integration test for the
// sandbox: hydrate the built-in scenario into an isolated root (real git
// repos + worktrees + a real statestore cache), then run atelier's own
// Restore against a fresh tmux server and assert the intent-workspaces come
// back exactly as seeded — sessions recreated, the driver window's
// attention/recap re-stamped, the workspace tag re-stamped, the picker
// rendering every recap with no live agent, and the soft-closed worktree
// left on disk for M-r.
func TestSeed_HydrateThenRestore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("ATELIER_WORKSPACE_ROOT", filepath.Join(root, "ateliers"))

	sc, err := seed.Builtin("acme-platform")
	if err != nil {
		t.Fatalf("Builtin: %v", err)
	}
	layout, err := seed.Hydrate(root, sc, seed.Options{AI: "claude"})
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}

	srv := testtmux.New(t) // sets ATELIER_TMUX_SOCKET, so Restore's bg-pull warmup skips (#32)
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("workspace.Restore: %v", err)
	}

	// 1. Every seeded workspace restored, no stray adoption.
	var want []string
	for _, ws := range sc.Workspaces {
		want = append(want, ws.Slug)
	}
	sort.Strings(want)
	got := atelierSessions(t, srv)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sessions = %v, want %v", got, want)
	}

	// 2. Attention + recap re-stamped on the eks upgrade driver window.
	wid := windowID(t, srv, "eks-1-30-upgrade", driverWindow("eks-1-30-upgrade"))
	if got := opt(t, srv, wid, "@needs_attention"); got != "1" {
		t.Errorf("@needs_attention = %q, want 1", got)
	}
	if got := opt(t, srv, wid, "@attention_recap"); !strings.Contains(got, "1.30") {
		t.Errorf("@attention_recap = %q, want it to mention 1.30", got)
	}

	// 3. Workspace tag re-stamped from the seeded record.
	if got := opt(t, srv, wid, "@workspace_tag"); got != "infra" {
		t.Errorf("@workspace_tag = %q, want infra", got)
	}

	// 4. Driver window age stamped.
	if ts := opt(t, srv, wid, workspace.OptWorkspaceCreatedTs); ts == "" {
		t.Error("eks driver window missing @workspace_created_ts")
	}

	// 5. Picker builds one row per restored workspace with its title, from
	// persisted state — no live agent. (The driver recap folds into the
	// workspace summary line; the seeded Summary drives the second row.)
	rows, err := workspaces.BuildWorkspaceList(srv.Client)
	if err != nil {
		t.Fatalf("BuildWorkspaceList: %v", err)
	}
	bySession := map[string]workspaces.WorkspaceRow{}
	for _, r := range rows {
		bySession[r.Session] = r
	}
	for _, ws := range sc.Workspaces {
		row, ok := bySession[ws.Slug]
		if !ok {
			t.Errorf("picker missing row for %s", ws.Slug)
			continue
		}
		if !strings.Contains(row.Display, ws.Title) {
			t.Errorf("picker row %s display = %q, want it to contain title %q", ws.Slug, row.Display, ws.Title)
		}
	}

	// 6. Soft-closed worktree on disk (for M-r) but not a live window.
	marker := filepath.Join(layout.WorktreeRoot, "acme-platform/platform-scripts/fix/ci-cache-key/.atelier-soft-closed")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("soft-closed marker missing: %v", err)
	}
}

// atelierSessions returns the sorted atelier-managed sessions, ignoring any
// bootstrap "default" session (which carries no workspace root).
func atelierSessions(t *testing.T, srv *testtmux.Server) []string {
	t.Helper()
	out, err := srv.Client.Run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || line == "default" {
			continue
		}
		names = append(names, line)
	}
	sort.Strings(names)
	return names
}

func windowID(t *testing.T, srv *testtmux.Server, session, window string) string {
	t.Helper()
	out, err := srv.Client.Run("list-windows", "-t", "="+session, "-F", "#{window_name}|#{window_id}")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, id, _ := strings.Cut(line, "|")
		if name == window {
			return id
		}
	}
	t.Fatalf("window %q not found in %q:\n%s", window, session, out)
	return ""
}

func opt(t *testing.T, srv *testtmux.Server, wid, name string) string {
	t.Helper()
	out, _ := srv.Client.Run("show-options", "-w", "-v", "-t", wid, name)
	return strings.TrimSpace(string(out))
}
