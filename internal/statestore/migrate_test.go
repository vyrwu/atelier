package statestore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoad_MigratesV2ToV3 is the end-to-end migration lock: a v2 cache
// (repo-session workspaces with a `kind`, an `ai.prompt`-carrying driver
// window, and per-branch worktree windows) loads as a v3 intent-workspace.
// Title comes from the repo slug, Intent from the first window's ai.prompt,
// and Worktrees are reconstructed from the branch-checkout windows. Root is
// left empty (materialized on first use, per WS-9). Goes through Load() (not
// migrateFromV2 directly) so the filter/canonicalize pass is exercised too.
func TestLoad_MigratesV2ToV3(t *testing.T) {
	path := setupCacheDir(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	v2 := `{
  "schema_version": 2,
  "hostname": "laptop",
  "last_active_session": "vyrwu/atelier",
  "workspaces": [
    {
      "session_name": "vyrwu/atelier",
      "repo_path": "/home/u/code/github/vyrwu/atelier",
      "kind": "worktree",
      "created_at": 1700000000,
      "windows": [
        {"name": "main", "cwd": "/home/u/code/github/vyrwu/atelier", "branch": "main"},
        {
          "name": "feat/persistence",
          "cwd": "/home/u/code/.worktrees/vyrwu/atelier/feat/persistence",
          "branch": "feat/persistence",
          "metadata": {
            "ai.prompt": "build the statestore",
            "ai.active_session_id": "abc-123"
          }
        }
      ]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s == nil {
		t.Fatal("Load returned nil for a v2 cache — migration should not drop it")
	}
	if s.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion after migration: got %d want %d", s.SchemaVersion, SchemaVersion)
	}
	if s.LastActiveSession != "vyrwu/atelier" {
		t.Errorf("last_active not carried: %q", s.LastActiveSession)
	}
	if len(s.Workspaces) != 1 {
		t.Fatalf("expected 1 migrated workspace, got %d: %+v", len(s.Workspaces), s.Workspaces)
	}
	ws := s.Workspaces[0]

	if ws.Title != "vyrwu/atelier" {
		t.Errorf("Title not derived from repo slug: got %q want %q", ws.Title, "vyrwu/atelier")
	}
	if ws.Intent != "build the statestore" {
		t.Errorf("Intent not recovered from ai.prompt: got %q", ws.Intent)
	}
	// Root materializes on first use — v2 migration leaves it empty.
	if ws.Root != "" {
		t.Errorf("Root should be empty after migration (materialized on first use), got %q", ws.Root)
	}
	if ws.RepoPath != "/home/u/code/github/vyrwu/atelier" {
		t.Errorf("RepoPath not carried: %q", ws.RepoPath)
	}
	if ws.CreatedAt != 1700000000 {
		t.Errorf("CreatedAt not carried: %d", ws.CreatedAt)
	}

	// The branch-checkout window becomes a worktree; the repo-root window
	// (cwd == repo_path) does not.
	if len(ws.Worktrees) != 1 {
		t.Fatalf("expected 1 reconstructed worktree, got %d: %+v", len(ws.Worktrees), ws.Worktrees)
	}
	wt := ws.Worktrees[0]
	if wt.Repo != "vyrwu/atelier" {
		t.Errorf("worktree Repo slug: got %q want %q", wt.Repo, "vyrwu/atelier")
	}
	if wt.Branch != "feat/persistence" {
		t.Errorf("worktree Branch: got %q want %q", wt.Branch, "feat/persistence")
	}
	if wt.Path != "/home/u/code/.worktrees/vyrwu/atelier/feat/persistence" {
		t.Errorf("worktree Path: got %q", wt.Path)
	}
	if wt.Link != "" {
		t.Errorf("worktree Link should be empty until layout repair, got %q", wt.Link)
	}

	// Windows carry over verbatim, preserving per-window metadata.
	if len(ws.Windows) != 2 {
		t.Fatalf("windows not carried over: %+v", ws.Windows)
	}
	drv := s.FindWindow("vyrwu/atelier", "feat/persistence")
	if drv == nil {
		t.Fatalf("driver window lost in migration: %+v", ws.Windows)
	}
	if drv.Metadata["ai.active_session_id"] != "abc-123" {
		t.Errorf("window metadata not preserved: %+v", drv.Metadata)
	}
}

// TestMigratedTitle covers the pure title-derivation helper directly: a
// single-repo session gets the "owner/repo" slug; a repo-less `auto/…`
// session falls back to the de-prefixed, de-hyphenated session name.
func TestMigratedTitle(t *testing.T) {
	cases := []struct {
		name     string
		session  string
		repoPath string
		want     string
	}{
		{"repo slug", "vyrwu/atelier", "/home/u/code/github/vyrwu/atelier", "vyrwu/atelier"},
		{"auto session no repo", "auto/fix-the-bug", "", "fix the bug"},
		{"plain session no repo", "scratch", "", "scratch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := migratedTitle(c.session, c.repoPath); got != c.want {
				t.Errorf("migratedTitle(%q, %q) = %q, want %q", c.session, c.repoPath, got, c.want)
			}
		})
	}
}

// TestMigratedIntent picks the first window carrying an ai.prompt; empty
// when none do.
func TestMigratedIntent(t *testing.T) {
	got := migratedIntent([]Window{
		{Name: "main"},
		{Name: "feat/x", Metadata: map[string]string{"ai.prompt": "first"}},
		{Name: "feat/y", Metadata: map[string]string{"ai.prompt": "second"}},
	})
	if got != "first" {
		t.Errorf("migratedIntent = %q, want %q", got, "first")
	}
	if got := migratedIntent([]Window{{Name: "main"}}); got != "" {
		t.Errorf("migratedIntent with no ai.prompt = %q, want empty", got)
	}
}

// TestMigratedWorktrees reconstructs a worktree for every window whose Cwd
// is a branch checkout distinct from the repo root; the repo-root window is
// skipped, and a missing Branch falls back to the window Name.
func TestMigratedWorktrees(t *testing.T) {
	repoPath := "/home/u/code/github/vyrwu/atelier"
	wts := migratedWorktrees(repoPath, []Window{
		{Name: "main", Cwd: repoPath, Branch: "main"}, // repo root → skipped
		{Name: "feat/a", Cwd: "/wt/a", Branch: "feat/a"},
		{Name: "feat/b", Cwd: "/wt/b"}, // no Branch → falls back to Name
		{Name: "noCwd"},                // no Cwd → skipped
	})
	if len(wts) != 2 {
		t.Fatalf("expected 2 worktrees, got %d: %+v", len(wts), wts)
	}
	if wts[0].Branch != "feat/a" || wts[0].Path != "/wt/a" || wts[0].Repo != "vyrwu/atelier" {
		t.Errorf("worktree[0] wrong: %+v", wts[0])
	}
	if wts[1].Branch != "feat/b" {
		t.Errorf("worktree[1] branch should fall back to Name: %+v", wts[1])
	}
}

// TestLoad_V1SchemaTreatedAsEmpty: the never-shipped v1 has no migration
// path, so a v1 cache loads as empty (nil) rather than crashing or being
// force-migrated through the v2 mapper.
func TestLoad_V1SchemaTreatedAsEmpty(t *testing.T) {
	path := setupCacheDir(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `{
  "schema_version": 1,
  "workspaces": [
    {"session_name": "vyrwu/atelier", "repo_path": "/r", "kind": "worktree"}
  ]
}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err != nil {
		t.Fatalf("Load on v1 cache: %v", err)
	}
	if s != nil {
		t.Errorf("v1 cache should load as empty (nil), got %+v", s)
	}
}
