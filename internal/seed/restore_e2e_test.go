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

// TestSeed_HydrateThenRestore is the load-bearing integration test for the
// sandbox: hydrate the built-in scenario into an isolated root (real git
// repos + worktrees + a real statestore cache), then run atelier's own
// Restore against a fresh tmux server and assert the workspaces come back
// exactly as seeded — sessions recreated, attention/recap re-stamped, the
// forge PR state re-stamped, the picker rendering every recap with no live
// agent, and the soft-closed worktree left on disk for M-r.
func TestSeed_HydrateThenRestore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

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
		want = append(want, ws.Session)
	}
	sort.Strings(want)
	got := atelierSessions(t, srv)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sessions = %v, want %v", got, want)
	}

	// 2. @repo_path stamped per session.
	for _, ws := range sc.Workspaces {
		want := filepath.Join(layout.CodeRoot, ws.RepoSlug) + "\n"
		if v, _ := srv.Client.Run("show-option", "-v", "-t", ws.Session, "@repo_path"); string(v) != want {
			t.Errorf("%s @repo_path = %q, want %q", ws.Session, string(v), want)
		}
	}

	// 3. Attention + recap re-stamped on the helm ingress window.
	wid := windowID(t, srv, "acme-platform/helm-charts", "feat/bump-ingress-nginx")
	if got := opt(t, srv, wid, "@needs_attention"); got != "1" {
		t.Errorf("@needs_attention = %q, want 1", got)
	}
	if got := opt(t, srv, wid, "@attention_recap"); !strings.Contains(got, "ingress-nginx") {
		t.Errorf("@attention_recap = %q, want it to mention ingress-nginx", got)
	}

	// 4. Forge PR state + workspace tag re-stamped from seeded metadata.
	if got := opt(t, srv, wid, "@forge_state"); got != "open" {
		t.Errorf("@forge_state = %q, want open", got)
	}
	if got := opt(t, srv, wid, "@workspace_tag"); got != "platform" {
		t.Errorf("@workspace_tag = %q, want platform", got)
	}

	// 4b. Per-window age: BOTH windows of the multi-worktree helm session
	// get a distinct @workspace_created_ts (regression — the second window
	// used to come back blank, so the picker showed no age on it).
	redisWid := windowID(t, srv, "acme-platform/helm-charts", "feat/redis-pdb")
	firstTs := opt(t, srv, wid, workspace.OptWorkspaceCreatedTs)
	secondTs := opt(t, srv, redisWid, workspace.OptWorkspaceCreatedTs)
	if firstTs == "" || secondTs == "" {
		t.Errorf("both helm windows want @workspace_created_ts; got first=%q second=%q", firstTs, secondTs)
	}
	if firstTs == secondTs {
		t.Errorf("helm windows share @workspace_created_ts %q; want distinct per-window ages", firstTs)
	}

	// 5. Picker renders recaps from persisted state — no live agent popup.
	// The recap is its own SessionRow field (the picker renders it as a
	// second line under the workspace name), not part of Display.
	rows, err := workspaces.BuildSessionList(srv.Client)
	if err != nil {
		t.Fatalf("BuildSessionList: %v", err)
	}
	byWindow := map[string]workspaces.SessionRow{}
	for _, r := range rows {
		byWindow[r.Session+":"+r.Window] = r
	}
	for _, ws := range sc.Workspaces {
		for _, w := range ws.Windows {
			row, ok := byWindow[ws.Session+":"+w.Name]
			if !ok {
				t.Errorf("picker missing row for %s:%s", ws.Session, w.Name)
				continue
			}
			if w.Recap != "" && !strings.Contains(row.Recap, w.Recap) {
				t.Errorf("picker row %s:%s recap = %q, want it to contain %q", ws.Session, w.Name, row.Recap, w.Recap)
			}
		}
	}

	// 6. Soft-closed worktree on disk (for M-r) but not a live window.
	marker := filepath.Join(layout.WorktreeRoot, "acme-platform/platform-scripts/fix/ci-cache-key/.atelier-soft-closed")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("soft-closed marker missing: %v", err)
	}
	res, _ := srv.Client.Run("list-windows", "-t", "=acme-platform/platform-scripts", "-F", "#{window_name}")
	if strings.Contains(string(res), "fix/ci-cache-key") {
		t.Error("soft-closed worktree fix/ci-cache-key should not be a live window")
	}
}

// atelierSessions returns the sorted atelier-managed sessions (those with
// @repo_path set), ignoring any bootstrap session.
func atelierSessions(t *testing.T, srv *testtmux.Server) []string {
	t.Helper()
	out, err := srv.Client.Run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		if v, _ := srv.Client.Run("show-option", "-v", "-t", line, "@repo_path"); strings.TrimSpace(string(v)) != "" {
			names = append(names, line)
		}
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
