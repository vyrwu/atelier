//go:build e2e

package workspace_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRestore_RecreatesWorkspaceDriverAndOptions is the load-bearing
// integration test for the intent-workspace persistence story: write a cache,
// start a fresh tmux server, call Restore — and the workspace comes back as a
// SESSION with a single driver window at its root, its identity options
// stamped, its per-agent state (attention/recap/metadata) re-applied, and its
// worktrees re-linked into the root as symlinks (NOT as one window per branch).
func TestRestore_RecreatesWorkspaceDriverAndOptions(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ATELIER_WORKSPACE_ROOT", t.TempDir())

	root := filepath.Join(t.TempDir(), "ws-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real worktree dir on disk so ReconcileLayout links it (rather than
	// reporting it dangling).
	wt := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	const createdTs int64 = 1729094400
	cached := &statestore.State{
		Workspaces: []statestore.Workspace{
			{
				SessionName: "vyrwu/atelier",
				Title:       "Ship persistence",
				Intent:      "make workspaces survive a restart",
				Root:        root,
				Tag:         "infra",
				CreatedAt:   createdTs,
				Worktrees: []statestore.Worktree{
					{Repo: "vyrwu/atelier", Branch: "feat/persistence", Path: wt},
				},
				Windows: []statestore.Window{
					{
						Name:      "atelier",
						Cwd:       root,
						Attention: true,
						Recap:     "Wrote persistence layer",
						RecapTs:   createdTs,
						Metadata: map[string]string{
							"ai.active_session_id": "abc-123-def-456",
						},
					},
				},
			},
		},
		Globals: map[string]string{
			"@atelier_k8s_active": "prod-aws",
		},
	}
	if err := statestore.Save(cached); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	srv := testtmux.New(t)
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("workspace.Restore: %v", err)
	}

	// Session recreated.
	if has, err := srv.Client.HasSession("vyrwu/atelier"); err != nil || !has {
		t.Fatalf("session not recreated: has=%v err=%v", has, err)
	}

	// Exactly ONE window (the driver) — worktrees are symlinks, not windows.
	winOut, err := srv.Client.Run("list-windows", "-t", "=vyrwu/atelier",
		"-F", "#{window_name}|#{window_id}")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(string(winOut))
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 (driver) window, got %d: %v", len(lines), lines)
	}
	wid := splitOnce(lines[0], '|')[1]

	// Session-level identity options.
	idChecks := map[string]string{
		workspace.OptWorkspaceID:     "vyrwu/atelier",
		workspace.OptWorkspaceTitle:  "Ship persistence",
		workspace.OptWorkspaceIntent: "make workspaces survive a restart",
		workspace.OptWorkspaceRoot:   root,
		workspace.OptWorkspaceTag:    "infra",
	}
	for opt, want := range idChecks {
		out, _ := srv.Client.Run("show-option", "-t", "vyrwu/atelier", "-qv", opt)
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("session option %s: got %q want %q", opt, got, want)
		}
	}

	// Driver window is marked.
	if drv, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceDriver); drv != "1" {
		t.Errorf("@workspace_driver on driver window = %q, want 1", drv)
	}

	// Per-window options re-stamped from the primary window record. The
	// @ai_* option comes from the generic Metadata bag (metadata key
	// `<plugin>.<field>` → tmux option `@<plugin>_<field>`).
	winChecks := map[string]string{
		workspace.OptAttention:          "1",
		workspace.OptRecap:              "Wrote persistence layer",
		workspace.OptRecapTs:            strconv.FormatInt(createdTs, 10),
		workspace.OptWorkspaceCreatedTs: strconv.FormatInt(createdTs, 10),
		"@ai_active_session_id":         "abc-123-def-456",
	}
	for opt, want := range winChecks {
		got, _ := srv.Client.GetWindowOption(wid, opt)
		if got != want {
			t.Errorf("window option %s: got %q want %q", opt, got, want)
		}
	}

	// Worktree re-linked into the root as a symlink pointing at the real dir.
	link := workspace.WorktreeLinkPath(root, "vyrwu/atelier", "feat/persistence")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("worktree symlink not created at %s: %v", link, err)
	}
	if target != wt {
		t.Errorf("worktree symlink target = %q, want %q", target, wt)
	}

	// Globals.
	if v, _ := srv.Client.ShowGlobalOption("@atelier_k8s_active"); v != "prod-aws" {
		t.Errorf("global @atelier_k8s_active: got %q want %q", v, "prod-aws")
	}
}

// TestRestore_Idempotent verifies running Restore twice in a row produces no
// errors and doesn't create duplicate sessions. This is the property that lets
// us put restore in `atelier init` and not worry about sourcing the config
// twice.
func TestRestore_Idempotent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ATELIER_WORKSPACE_ROOT", t.TempDir())

	root := t.TempDir()
	_ = statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{
			{SessionName: "x", Title: "x", Root: root,
				Windows: []statestore.Window{{Name: "x", Cwd: root}}},
		},
	})

	srv := testtmux.New(t)
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("Restore 1: %v", err)
	}
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("Restore 2: %v", err)
	}

	out, _ := srv.Client.Run("list-windows", "-t", "=x", "-F", "#{window_name}")
	if got := len(nonEmptyLines(string(out))); got != 1 {
		t.Errorf("expected exactly 1 window after two restores, got %d. list-windows:\n%s",
			got, out)
	}
}

// TestRestore_MaterializesRootWhenMissing verifies restore creates the
// workspace's dedicated root when it doesn't exist on disk (a migrated
// workspace, or a machine where ~/ateliers was cleared). The session must
// still come back — the whole point of restore.
func TestRestore_MaterializesRootWhenMissing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ATELIER_WORKSPACE_ROOT", t.TempDir())

	root := filepath.Join(t.TempDir(), "not-yet-there")
	_ = statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{
			{SessionName: "ghost", Title: "ghost", Root: root,
				Windows: []statestore.Window{{Name: "ghost", Cwd: root}}},
		},
	})

	srv := testtmux.New(t)
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if has, _ := srv.Client.HasSession("ghost"); !has {
		t.Error("Restore should recreate the session and materialize its root")
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Errorf("root not materialized: %v", err)
	}
}

// TestSyncCache_RemovesOrphans seeds the cache with a workspace that's not in
// tmux, calls SyncCache, asserts the orphan is gone. This is the property the
// session-closed tmux hook depends on for cache hygiene.
func TestSyncCache_RemovesOrphans(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("alive")

	// Read the actual window name testtmux gave us so the cache lines up.
	aliveWinOut, _ := srv.Client.Run("list-windows", "-t", "=alive", "-F", "#{window_name}")
	aliveWinName := nonEmptyLines(string(aliveWinOut))[0]

	_ = statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{
			{SessionName: "alive", Title: "alive", Root: "/r-alive",
				Windows: []statestore.Window{{Name: aliveWinName}}},
			{SessionName: "ghost", Title: "ghost", Root: "/r-ghost",
				Windows: []statestore.Window{{Name: "ghost"}}},
		},
	})

	if err := workspace.SyncCache(srv.Client); err != nil {
		t.Fatalf("SyncCache: %v", err)
	}

	s, _ := statestore.Load()
	if s == nil {
		t.Fatal("cache should still exist after sync")
	}
	if len(s.Workspaces) != 1 || s.Workspaces[0].SessionName != "alive" {
		t.Errorf("ghost not removed (or alive lost): %+v", s.Workspaces)
	}
}

// helpers — pulled inline to keep test-file dependency self-contained.
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range splitForTest(s) {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitForTest(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func splitOnce(s string, sep byte) [2]string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{s, ""}
}
