//go:build e2e

package ghpr

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// windowID returns the single window id of a session (test helper).
func windowID(t *testing.T, srv *testtmux.Server, session string) string {
	t.Helper()
	out, _ := srv.Client.Run("list-windows", "-t", "="+session, "-F", "#{window_id}")
	return strings.TrimSpace(string(out))
}

// windowName returns the single window name of a session (test helper).
func windowName(t *testing.T, srv *testtmux.Server, session string) string {
	t.Helper()
	out, _ := srv.Client.Run("list-windows", "-t", "="+session, "-F", "#{window_name}")
	return strings.TrimSpace(string(out))
}

// TestRunRefresh_StampsGitWindow locks in the enumeration + stamping
// contract: a repo window gets @ghpr_ts stamped. The fixture repo has a
// local (non-GitHub) origin and no PR, so `gh pr view` fails fast and the
// badge is cleared — exercising the "no PR" path deterministically without
// network or auth.
func TestRunRefresh_StampsGitWindow(t *testing.T) {
	srv := testtmux.New(t)
	tmp := t.TempDir()
	repo := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")

	if _, err := srv.Client.Run("new-session", "-d", "-s", "vyrwu/demo", "-c", repo); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	wid := windowID(t, srv, "vyrwu/demo")
	if err := srv.Client.SetWindowOption(wid, "@repo_path", repo); err != nil {
		t.Fatalf("set @repo_path: %v", err)
	}
	// Seed a stale badge so we can verify the no-PR path clears it.
	_ = srv.Client.SetWindowOption(wid, OptBadge, "STALE")

	if err := runRefresh(srv.Client, time.Now()); err != nil {
		t.Fatalf("runRefresh: %v", err)
	}

	if ts, _ := srv.Client.GetWindowOption(wid, OptTs); ts == "" || ts == "0" {
		t.Errorf("%s should be a non-zero unix epoch, got %q", OptTs, ts)
	}
	if b, _ := srv.Client.GetWindowOption(wid, OptBadge); b != "" {
		t.Errorf("%s should be cleared when there is no PR, got %q", OptBadge, b)
	}
	if s, _ := srv.Client.GetWindowOption(wid, OptState); s != "" {
		t.Errorf("%s should be cleared when there is no PR, got %q", OptState, s)
	}
}

// TestRunRefresh_SkipsNonGitWindow: a window without @repo_path is not a
// workspace and must be left untouched.
func TestRunRefresh_SkipsNonGitWindow(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("scratch")
	time.Sleep(150 * time.Millisecond)
	wid := windowID(t, srv, "scratch")

	if err := runRefresh(srv.Client, time.Now()); err != nil {
		t.Fatalf("runRefresh: %v", err)
	}
	if ts, _ := srv.Client.GetWindowOption(wid, OptTs); ts != "" {
		t.Errorf("%s should be unset on a non-git window, got %q", OptTs, ts)
	}
}

// TestRunRefresh_ThrottlesFreshWindow: a window whose @ghpr_ts is within
// refreshTTL is skipped entirely, so its existing badge is preserved and
// no gh call is made.
func TestRunRefresh_ThrottlesFreshWindow(t *testing.T) {
	srv := testtmux.New(t)
	tmp := t.TempDir()
	repo := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")

	if _, err := srv.Client.Run("new-session", "-d", "-s", "vyrwu/demo", "-c", repo); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	wid := windowID(t, srv, "vyrwu/demo")
	_ = srv.Client.SetWindowOption(wid, "@repo_path", repo)

	now := time.Now()
	// Fresh timestamp + a sentinel badge; a throttled window must keep both.
	_ = srv.Client.SetWindowOption(wid, OptTs, strconv.FormatInt(now.Unix(), 10))
	_ = srv.Client.SetWindowOption(wid, OptBadge, "KEEP")

	if err := runRefresh(srv.Client, now); err != nil {
		t.Fatalf("runRefresh: %v", err)
	}
	if b, _ := srv.Client.GetWindowOption(wid, OptBadge); b != "KEEP" {
		t.Errorf("fresh window badge should be preserved, got %q want KEEP", b)
	}
}

// TestRunRefresh_PersistsToStatestore locks in OOTB restore: refresh
// mirrors PR state into the statestore under ghpr.* metadata keys, so
// workspace restore re-stamps the badge without waiting for a refresh.
// @repo_path is set at SESSION scope so the session is atelier-managed
// (the persistence gate). XDG_CACHE_HOME is isolated so the test writes
// to a temp cache, never the user's real state.
func TestRunRefresh_PersistsToStatestore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := testtmux.New(t)
	tmp := t.TempDir()
	repo := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")

	if _, err := srv.Client.Run("new-session", "-d", "-s", "vyrwu/demo", "-c", repo); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	// Session-scoped @repo_path → session is atelier-managed (gate for
	// persistence) and the window inherits it for enumeration.
	if _, err := srv.Client.Run("set-option", "-t", "vyrwu/demo", "@repo_path", repo); err != nil {
		t.Fatalf("set session @repo_path: %v", err)
	}
	winName := windowName(t, srv, "vyrwu/demo")
	// Register the workspace like the real creation flow, so the store has
	// a workspace entry (with RepoPath) that survives Save's managed-only
	// filter and that refresh's per-window metadata attaches to.
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session: "vyrwu/demo", RepoPath: repo, Kind: "worktree",
		WindowName: winName, Cwd: repo, Branch: "main",
	})

	if err := runRefresh(srv.Client, time.Now()); err != nil {
		t.Fatalf("runRefresh: %v", err)
	}

	st, err := statestore.Load()
	if err != nil {
		t.Fatalf("statestore.Load: %v", err)
	}
	if st == nil {
		t.Fatal("statestore is nil — refresh did not persist")
	}
	var found bool
	for _, ws := range st.Workspaces {
		if ws.SessionName != "vyrwu/demo" {
			continue
		}
		for _, w := range ws.Windows {
			if w.Metadata["ghpr.ts"] != "" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected ghpr.ts persisted in statestore for vyrwu/demo, state=%+v", st.Workspaces)
	}
}
