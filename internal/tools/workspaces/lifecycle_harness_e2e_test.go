//go:build e2e

// Structural lifecycle harness. The workspace bugs (workspaces vanishing on
// restart, bare "zsh" windows, recovered workspaces not surviving a relaunch)
// all live at the SEAMS between operations, across MULTIPLE server lifecycles
// — exactly what unit + single-flow e2e tests never exercise. This file
// models the real user journey (create → restart → recover → restart) and
// asserts INVARIANTS after each step. Add a journey here for any new lifecycle
// behavior; the invariants below are the properties that must always hold.
package workspaces

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type lifecycleEnv struct {
	t    *testing.T
	tmp  string
	repo string // main repo path (vyrwu/demo)
}

func newLifecycleEnv(t *testing.T) *lifecycleEnv {
	t.Helper()
	// NOT t.TempDir: restore fires fire-and-forget `git` (bg-pull) children
	// that can briefly outlive the test and hold the worktree dir open.
	// t.TempDir hard-fails the test on a non-empty dir at cleanup; a
	// best-effort RemoveAll is correct here (the OS reclaims the rest).
	tmp, err := os.MkdirTemp("", "atelier-lifecycle-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	repo := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	t.Setenv("ATELIER_WORKTREE_ROOT", filepath.Join(tmp, "worktrees"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "cache"))
	// Mark this as a test socket so SpawnBgPull no-ops (it skips on
	// `atelier-test-*` sockets) — otherwise restore leaks fire-and-forget
	// `git fetch` children that pile up across the suite until git can't
	// fork. The cache filename itself is fixed (state.json), independent of
	// this value.
	t.Setenv("ATELIER_TMUX_SOCKET", "atelier-test-lifecycle")
	return &lifecycleEnv{t: t, tmp: tmp, repo: repo}
}

// addWorktree materializes a real branch worktree on disk (what a workspace
// window points its cwd at). Returns the worktree path.
func (e *lifecycleEnv) addWorktree(branch string) string {
	e.t.Helper()
	wt := filepath.Join(e.tmp, "worktrees", "vyrwu", "demo", branch)
	if out, err := exec.Command("git", "-C", e.repo, "worktree", "add", "-b", branch, wt).CombinedOutput(); err != nil {
		e.t.Fatalf("git worktree add %s: %v\n%s", branch, err, out)
	}
	return wt
}

// restart is the "quit atelier → relaunch" path: kill the server, spin a fresh
// one, and run restore from the (persistent) cache — exactly what a real
// relaunch does on a fresh server.
func (e *lifecycleEnv) restart(old *testtmux.Server) *testtmux.Server {
	e.t.Helper()
	old.Kill()
	fresh := testtmux.New(e.t)
	if err := workspace.Restore(fresh.Client); err != nil {
		e.t.Fatalf("restore after restart: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	return fresh
}

// ---------------------------------------------------------------------------
// Invariants — the properties that must ALWAYS hold after any operation.
// ---------------------------------------------------------------------------

var bareShellNames = map[string]bool{"zsh": true, "bash": true, "sh": true, "fish": true}

// invNoBareWorkspaceWindow: a workspace session (one carrying @repo_path) must
// never contain a bare shell window — that's the stray "zsh" that shows up in
// M-s and needs a manual exit.
func invNoBareWorkspaceWindow(t *testing.T, srv *testtmux.Server) {
	t.Helper()
	out, _ := srv.Client.Run("list-windows", "-a", "-F", "#{session_name}\t#{window_name}\t#{@repo_path}")
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 3 {
			continue
		}
		sess, name, repo := f[0], f[1], f[2]
		if repo != "" && bareShellNames[name] {
			t.Errorf("INVARIANT VIOLATED: bare shell window %q in workspace session %q (repo=%s)", name, sess, repo)
		}
	}
}

// invLiveHasRepoPath: every session named like a workspace (org/repo) must
// carry @repo_path — otherwise it's a bare launcher-created shell that
// vanishes from M-s.
func invLiveHasRepoPath(t *testing.T, srv *testtmux.Server) {
	t.Helper()
	out, _ := srv.Client.Run("list-sessions", "-F", "#{session_name}")
	for _, s := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.Contains(s, "/") || strings.HasPrefix(s, "_atelier") {
			continue // not a repo workspace session
		}
		v, _ := srv.Client.Run("show-option", "-t", s, "-qv", "@repo_path")
		if strings.TrimSpace(string(v)) == "" {
			t.Errorf("INVARIANT VIOLATED: workspace session %q has no @repo_path (bare)", s)
		}
	}
}

// invInCache: the workspace window must be persisted, so it survives a restart.
func invInCache(t *testing.T, session, branch string) {
	t.Helper()
	st, err := statestore.Load()
	if err != nil || st == nil {
		t.Fatalf("cache load: err=%v st=%v", err, st)
	}
	if st.FindWindow(session, branch) == nil {
		var got []string
		for _, w := range st.Workspaces {
			for _, win := range w.Windows {
				got = append(got, w.SessionName+"/"+win.Name)
			}
		}
		t.Errorf("INVARIANT VIOLATED: %s/%s NOT in cache (won't survive restart); cached: %v", session, branch, got)
	}
}

// invLive: the workspace window exists on the server.
func invLive(t *testing.T, srv *testtmux.Server, session, branch string) {
	t.Helper()
	out, _ := srv.Client.Run("list-windows", "-t", "="+session, "-F", "#{window_name}")
	if !windowListed(string(out), branch) {
		t.Errorf("INVARIANT VIOLATED: %s/%s not live; windows:\n%s", session, branch, out)
	}
}

// invNotLive: the workspace window must NOT be live (e.g. soft-closed).
func invNotLive(t *testing.T, srv *testtmux.Server, session, branch string) {
	t.Helper()
	out, _ := srv.Client.Run("list-windows", "-t", "="+session, "-F", "#{window_name}")
	if windowListed(string(out), branch) {
		t.Errorf("INVARIANT VIOLATED: %s/%s is live but should not be; windows:\n%s", session, branch, out)
	}
}

func windowListed(out, branch string) bool {
	for _, n := range strings.Split(strings.TrimSpace(out), "\n") {
		if n == branch {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Journeys
// ---------------------------------------------------------------------------

// TestLifecycle_RecoverPersistsAndSurvivesRestart is the structural guard for
// the "recovered workspace disappears after restarting atelier" class. Recover
// materializes a live workspace from an on-disk worktree; it MUST persist to
// the cache so a subsequent relaunch (fresh server → restore) brings it back.
func TestLifecycle_RecoverPersistsAndSurvivesRestart(t *testing.T) {
	e := newLifecycleEnv(t)
	srv := testtmux.New(t)
	const branch = "feat-recovered"
	e.addWorktree(branch)
	// Cache deliberately does NOT contain this workspace — recover pulls it
	// from the on-disk worktree, which is the case that broke.

	_ = openWorktreeWorkspace(srv.Client, "vyrwu/demo", branch) // M-r recover
	time.Sleep(100 * time.Millisecond)

	invLive(t, srv, "vyrwu/demo", branch)
	invInCache(t, "vyrwu/demo", branch) // must persist (was the bug)
	invNoBareWorkspaceWindow(t, srv)
	invLiveHasRepoPath(t, srv)

	fresh := e.restart(srv)
	invLive(t, fresh, "vyrwu/demo", branch) // must survive the relaunch
	invNoBareWorkspaceWindow(t, fresh)
}

// TestLifecycle_CreatePersistsAndSurvivesRestart: a created workspace survives
// a relaunch (baseline persistence sanity, via the RegisterCreatedWorkspace
// chokepoint).
func TestLifecycle_CreatePersistsAndSurvivesRestart(t *testing.T) {
	e := newLifecycleEnv(t)
	srv := testtmux.New(t)
	const branch = "feat-created"
	wt := e.addWorktree(branch)

	if _, err := workspace.EnsureSession(srv.Client, "vyrwu/demo", e.repo, "main"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if _, err := workspace.CreateWorktreeWindow(srv.Client, workspace.WorktreeWindowSpec{
		Session: "vyrwu/demo", WtPath: wt, WindowName: branch, Kind: "worktree",
	}); err != nil {
		t.Fatalf("CreateWorktreeWindow: %v", err)
	}
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session: "vyrwu/demo", RepoPath: e.repo, Kind: "worktree",
		WindowName: branch, Cwd: wt, Branch: branch,
	})

	invInCache(t, "vyrwu/demo", branch)
	invNoBareWorkspaceWindow(t, srv)

	fresh := e.restart(srv)
	invLive(t, fresh, "vyrwu/demo", branch)
}

// TestLifecycle_RecoverClearsSoftCloseAndSurvivesRestart is the structural
// guard for "recovered session disappears after restart" AS AN INTERACTION
// with the restore-skip-soft-closed fix. Recovering a SOFT-CLOSED workspace
// must clear the marker, so the next relaunch's restore does NOT skip it. If
// recover forgets to clear (or clears the wrong path), the workspace comes
// back once, then vanishes on the following restart.
func TestLifecycle_RecoverClearsSoftCloseAndSurvivesRestart(t *testing.T) {
	e := newLifecycleEnv(t)
	srv := testtmux.New(t)
	const branch = "feat-reopened"
	wt := e.addWorktree(branch)

	if err := statestore.UpdateWorkspace("vyrwu/demo", func(ws *statestore.Workspace) {
		ws.RepoPath = e.repo
		ws.Kind = "worktree"
	}); err != nil {
		t.Fatal(err)
	}
	if err := statestore.UpdateWindow("vyrwu/demo", branch, func(w *statestore.Window) {
		w.Cwd = wt
		w.Branch = branch
	}); err != nil {
		t.Fatal(err)
	}
	touchSoftClosedMarker(wt) // user closed it earlier

	_ = openWorktreeWorkspace(srv.Client, "vyrwu/demo", branch) // M-r recover → must clear marker
	time.Sleep(100 * time.Millisecond)
	invLive(t, srv, "vyrwu/demo", branch)

	fresh := e.restart(srv)
	invLive(t, fresh, "vyrwu/demo", branch) // must survive — marker must have been cleared
}

// TestLifecycle_RecapShownWithoutLivePopup guards "AI summaries not restored
// on launch". Restore re-stamps @attention_recap, but a fresh launch doesn't
// recreate the agent popup — so the picker must render the persisted summary
// from @attention_recap, NOT gate it on a live popup. Otherwise every
// workspace's last summary vanishes on relaunch.
func TestLifecycle_RecapShownWithoutLivePopup(t *testing.T) {
	e := newLifecycleEnv(t)
	srv := testtmux.New(t)
	const branch = "feat-summary"
	wt := e.addWorktree(branch)

	if _, err := workspace.EnsureSession(srv.Client, "vyrwu/demo", e.repo, "main"); err != nil {
		t.Fatal(err)
	}
	wid, err := workspace.CreateWorktreeWindow(srv.Client, workspace.WorktreeWindowSpec{
		Session: "vyrwu/demo", WtPath: wt, WindowName: branch, Kind: "worktree",
	})
	if err != nil {
		t.Fatal(err)
	}
	const summary = "did the thing pending review"
	if err := workspace.SetRecap(srv.Client, wid, summary); err != nil {
		t.Fatal(err)
	}

	// No live agent popup exists — the summary must show.
	rows, err := BuildSessionList(srv.Client)
	if err != nil {
		t.Fatalf("BuildSessionList: %v", err)
	}
	found := false
	for _, r := range rows {
		// The recap is now a separate field (its own picker line), not part
		// of the styled name line in Display.
		if r.Session == "vyrwu/demo" && r.Window == branch && strings.Contains(r.Recap, summary) {
			found = true
		}
	}
	if !found {
		t.Errorf("persisted recap not shown without a live popup; rows: %+v", rows)
	}
}

// TestLifecycle_SoftClosedDoesNotResurrect: a soft-closed workspace stays in
// the cache for M-r recover but must NOT come back live on restart (the
// accumulation bug that flooded sessions with closed branches).
func TestLifecycle_SoftClosedDoesNotResurrect(t *testing.T) {
	e := newLifecycleEnv(t)
	srv := testtmux.New(t)
	const branch = "feat-closed"
	wt := e.addWorktree(branch)

	// Seed the cache with the workspace, then soft-close its worktree.
	if err := statestore.UpdateWorkspace("vyrwu/demo", func(ws *statestore.Workspace) {
		ws.RepoPath = e.repo
		ws.Kind = "worktree"
	}); err != nil {
		t.Fatal(err)
	}
	if err := statestore.UpdateWindow("vyrwu/demo", branch, func(w *statestore.Window) {
		w.Cwd = wt
		w.Branch = branch
	}); err != nil {
		t.Fatal(err)
	}
	touchSoftClosedMarker(wt)

	fresh := e.restart(srv)
	invNotLive(t, fresh, "vyrwu/demo", branch) // must NOT resurrect
}

// liveBranchWindows returns the set of window names live in a session.
func liveBranchWindows(t *testing.T, srv *testtmux.Server, session string) map[string]bool {
	t.Helper()
	out, _ := srv.Client.Run("list-windows", "-t", "="+session, "-F", "#{window_name}")
	set := map[string]bool{}
	for _, n := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if n != "" {
			set[n] = true
		}
	}
	return set
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestLifecycle_RoundTripIdempotent is the invariant the user actually
// wants: launch → set S → relaunch → S → relaunch → STILL S. Two
// consecutive relaunches must yield an identical live set. A restore that
// prunes then re-persists the pruned set shrinks a little every relaunch
// ("the workspace list keeps shrinking") — this catches that.
func TestLifecycle_RoundTripIdempotent(t *testing.T) {
	e := newLifecycleEnv(t)
	srv := testtmux.New(t)
	branches := []string{"feat-a", "feat-b"}

	if _, err := workspace.EnsureSession(srv.Client, "vyrwu/demo", e.repo, "main"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	for _, b := range branches {
		wt := e.addWorktree(b)
		if _, err := workspace.CreateWorktreeWindow(srv.Client, workspace.WorktreeWindowSpec{
			Session: "vyrwu/demo", WtPath: wt, WindowName: b, Kind: "worktree",
		}); err != nil {
			t.Fatalf("CreateWorktreeWindow %s: %v", b, err)
		}
		workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
			Session: "vyrwu/demo", RepoPath: e.repo, Kind: "worktree",
			WindowName: b, Cwd: wt, Branch: b,
		})
	}

	fresh := e.restart(srv)
	first := liveBranchWindows(t, fresh, "vyrwu/demo")
	fresh2 := e.restart(fresh)
	second := liveBranchWindows(t, fresh2, "vyrwu/demo")

	for _, b := range branches {
		if !first[b] {
			t.Errorf("relaunch 1 dropped %q; live=%v", b, keysOf(first))
		}
		if !second[b] {
			t.Errorf("relaunch 2 dropped %q; live=%v", b, keysOf(second))
		}
	}
	if len(first) != len(second) {
		t.Errorf("INVARIANT VIOLATED: live set shrank across relaunches: %v → %v",
			keysOf(first), keysOf(second))
	}
}

// TestLifecycle_NoNullCwdResurrection reproduces the junk-resurrection the
// user saw (bare "zsh" windows in M-s). A cached window with no cwd is
// launcher-bare-create junk — restore must NOT bring it back as a live
// window (it would land in $HOME with the wrong context), while a real
// worktree window in the same workspace MUST restore.
func TestLifecycle_NoNullCwdResurrection(t *testing.T) {
	e := newLifecycleEnv(t)
	srv := testtmux.New(t)
	realWt := e.addWorktree("feat-real")

	if err := statestore.UpdateWorkspace("vyrwu/demo", func(ws *statestore.Workspace) {
		ws.RepoPath = e.repo
		ws.Kind = "worktree"
	}); err != nil {
		t.Fatal(err)
	}
	if err := statestore.UpdateWindow("vyrwu/demo", "feat-real", func(w *statestore.Window) {
		w.Cwd = realWt
		w.Branch = "feat-real"
	}); err != nil {
		t.Fatal(err)
	}
	if err := statestore.UpdateWindow("vyrwu/demo", "zsh", func(w *statestore.Window) {
		// Deliberately no cwd — the bare-shell junk.
	}); err != nil {
		t.Fatal(err)
	}

	fresh := e.restart(srv)
	live := liveBranchWindows(t, fresh, "vyrwu/demo")
	if !live["feat-real"] {
		t.Errorf("real worktree window not restored; live=%v", keysOf(live))
	}
	if live["zsh"] {
		t.Errorf("INVARIANT VIOLATED: bare/null-cwd junk window resurrected; live=%v", keysOf(live))
	}
}
