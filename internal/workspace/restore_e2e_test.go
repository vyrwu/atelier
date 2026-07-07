//go:build e2e

package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestRestore_RecreatesSessionsWindowsAndOptions is the load-bearing
// integration test for the persistence story: write a cache, kill the
// tmux server, start fresh, call Restore — and the user's workspaces +
// per-window options come back exactly as they were.
func TestRestore_RecreatesSessionsWindowsAndOptions(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Stand up a temp worktree dir so restore's path-existence check
	// passes. Use the test tempdir so it's auto-cleaned.
	wt := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a cache as if a prior atelier session had populated it.
	cached := &statestore.State{
		Workspaces: []statestore.Workspace{
			{
				SessionName: "vyrwu/atelier",
				RepoPath:    wt,
				Kind:        "worktree",
				Windows: []statestore.Window{
					{
						Name:      "feat/persistence",
						Cwd:       wt,
						Branch:    "feat/persistence",
						Attention: true,
						Recap:     "Wrote persistence layer",
						RecapTs:   1729094400,
						Metadata: map[string]string{
							"ai.prompt":            "build the cache",
							"ai.workspace_kind":    "worktree",
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

	// Start a fresh tmux server (no sessions yet besides whatever
	// testtmux creates by default).
	srv := testtmux.New(t)

	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("workspace.Restore: %v", err)
	}

	// Session recreated.
	has, err := srv.Client.HasSession("vyrwu/atelier")
	if err != nil || !has {
		t.Fatalf("session not recreated: has=%v err=%v", has, err)
	}

	// Session-level option.
	if v, _ := srv.Client.Run("show-option", "-v", "-t", "vyrwu/atelier", "@repo_path"); string(v) != wt+"\n" {
		t.Errorf("@repo_path: got %q want %q", string(v), wt+"\n")
	}

	// Find the recreated window's @ID so we can query its options.
	out, err := srv.Client.Run("list-windows", "-t", "=vyrwu/atelier",
		"-F", "#{window_name}|#{window_id}")
	if err != nil {
		t.Fatal(err)
	}
	var wid string
	for _, line := range splitForTest(string(out)) {
		if line == "" {
			continue
		}
		parts := splitOnce(line, '|')
		if parts[0] == "feat/persistence" {
			wid = parts[1]
			break
		}
	}
	if wid == "" {
		t.Fatalf("restored window not found in tmux. list-windows:\n%s", out)
	}

	// Per-window options re-stamped. The `@ai_*` options come from
	// the generic Metadata bag — restore translates each metadata
	// key `<plugin>.<field>` to its tmux option `@<plugin>_<field>`
	// (statestore.MetadataKeyToOptionName).
	checks := map[string]string{
		"@needs_attention":      "1",
		"@attention_recap":      "Wrote persistence layer",
		"@attention_recap_ts":   "1729094400",
		"@ai_prompt":            "build the cache",
		"@ai_workspace_kind":    "worktree",
		"@ai_active_session_id": "abc-123-def-456",
	}
	for opt, want := range checks {
		got, _ := srv.Client.GetWindowOption(wid, opt)
		if got != want {
			t.Errorf("window option %s: got %q want %q", opt, got, want)
		}
	}

	// Globals.
	if v, _ := srv.Client.ShowGlobalOption("@atelier_k8s_active"); v != "prod-aws" {
		t.Errorf("global @atelier_k8s_active: got %q want %q", v, "prod-aws")
	}
}

// TestRestore_Idempotent verifies running Restore twice in a row
// produces no errors and doesn't create duplicate sessions. This is
// the property that lets us put restore in `atelier init` and not
// worry about source-file-the-config-twice scenarios.
func TestRestore_Idempotent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	wt := filepath.Join(t.TempDir(), "wt")
	_ = os.MkdirAll(wt, 0o755)
	_ = statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{
			{SessionName: "x", RepoPath: wt, Kind: "worktree",
				Windows: []statestore.Window{{Name: "main", Cwd: wt}}},
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
	count := 0
	for _, line := range splitForTest(string(out)) {
		if line == "main" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 window named 'main', got %d. list-windows:\n%s",
			count, out)
	}
}

// TestRestore_SkipsMissingWorktree verifies the "user `git worktree
// remove`d the worktree behind atelier's back" scenario: cache says
// workspace exists at /tmp/gone, restore sees the path is gone and
// SKIPS that workspace rather than failing or creating a session
// pointing at a non-existent directory.
func TestRestore_SkipsMissingWorktree(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{
			{SessionName: "ghost", RepoPath: "/nonexistent",
				Kind: "worktree", Windows: []statestore.Window{
					{Name: "main", Cwd: "/nonexistent/worktree"},
				}},
		},
	})

	srv := testtmux.New(t)
	if err := workspace.Restore(srv.Client); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	has, _ := srv.Client.HasSession("ghost")
	if has {
		t.Error("Restore should NOT create a session whose worktree is missing")
	}
}

// TestSyncCache_RemovesOrphans seeds the cache with a workspace that's
// not in tmux, calls SyncCache, asserts the orphan is gone. This is
// the property the session-closed / window-unlinked tmux hooks
// depend on for cache hygiene.
//
// Critical: the "alive" entry's window NAME must match what tmux
// actually has (testtmux's NewSession picks whatever shell default is).
// Otherwise SyncCache would correctly remove the window-name-mismatch
// as orphaned, which would in turn remove the empty workspace.
func TestSyncCache_RemovesOrphans(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("alive")

	// Read the actual window name testtmux gave us so the cache lines up.
	aliveWinOut, _ := srv.Client.Run("list-windows", "-t", "=alive", "-F", "#{window_name}")
	aliveWinName := splitForTest(string(aliveWinOut))[0]

	_ = statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{
			{SessionName: "alive", RepoPath: "/r-alive", Kind: "worktree",
				Windows: []statestore.Window{{Name: aliveWinName}}},
			{SessionName: "ghost", RepoPath: "/r-ghost", Kind: "worktree",
				Windows: []statestore.Window{{Name: "main"}}},
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
