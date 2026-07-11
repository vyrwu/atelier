package statestore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setupCacheDir redirects XDG_CACHE_HOME to a tempdir and returns the
// expected state file path. Cleans up via t.Cleanup.
func setupCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	return Path()
}

func TestLoad_FileMissing_ReturnsNil(t *testing.T) {
	setupCacheDir(t)
	got, err := Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if got != nil {
		t.Errorf("Load on missing file should return nil State, got %+v", got)
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	setupCacheDir(t)
	in := &State{
		Workspaces: []Workspace{
			{
				SessionName: "vyrwu/atelier",
				RepoPath:    "/Users/u/code/github/vyrwu/atelier",
				Kind:        "worktree",
				Windows: []Window{
					{Name: "main", Cwd: "/Users/u/code/github/vyrwu/atelier", Branch: "main"},
					{
						Name:      "feat/persistence",
						Cwd:       "/Users/u/code/.worktrees/.../feat/persistence",
						Branch:    "feat/persistence",
						Attention: true,
						Recap:     "Designing persistence layer",
						RecapTs:   1729094400,
						Metadata: map[string]string{
							"ai.prompt":            "build statestore",
							"ai.workspace_kind":    "worktree",
							"ai.active_session_id": "abc-123",
						},
					},
				},
			},
		},
		Globals: map[string]string{
			"k8s_active":   "prod-aws",
			"pgcli_active": "prod:read",
		},
	}
	if err := Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out == nil {
		t.Fatal("Load returned nil after Save")
	}
	if out.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion: got %d want %d", out.SchemaVersion, SchemaVersion)
	}
	// Compare cores (Hostname/SchemaVersion set by Save are not user data).
	if got, want := out.Workspaces[0].SessionName, "vyrwu/atelier"; got != want {
		t.Errorf("session_name: got %q want %q", got, want)
	}
	w := out.Workspaces[0].Windows[1]
	if w.Recap != "Designing persistence layer" || w.RecapTs != 1729094400 {
		t.Errorf("recap/ts not preserved: %+v", w)
	}
	if got := w.Metadata["ai.active_session_id"]; got != "abc-123" {
		t.Errorf("ai.active_session_id metadata not preserved: %q", got)
	}
	if got := w.Metadata["ai.prompt"]; got != "build statestore" {
		t.Errorf("ai.prompt metadata not preserved: %q", got)
	}
	if !w.Attention {
		t.Errorf("attention not preserved")
	}
	if out.Globals["k8s_active"] != "prod-aws" {
		t.Errorf("global not preserved: %+v", out.Globals)
	}
}

// TestLoad_SchemaMismatch_TreatedAsEmpty locks in the "no migrations,
// just ignore" policy. Future schema bumps should NOT crash atelier
// for users on stale caches.
func TestLoad_SchemaMismatch_TreatedAsEmpty(t *testing.T) {
	path := setupCacheDir(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	bogus := map[string]any{
		"schema_version": 999, // future
		"hostname":       "irrelevant",
		"workspaces":     []any{map[string]any{"session_name": "ghost"}},
	}
	data, _ := json.Marshal(bogus)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load on schema mismatch: %v", err)
	}
	if got != nil {
		t.Errorf("schema mismatch should return nil, got %+v", got)
	}
}

// TestLoad_MalformedJSON_ReturnsError differs from schema-mismatch:
// schema-mismatch is a known shape we choose to discard, malformed
// JSON is corruption we surface.
func TestLoad_MalformedJSON_ReturnsError(t *testing.T) {
	path := setupCacheDir(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("expected error on malformed JSON, got nil")
	}
}

// TestSave_AtomicViaTempRename verifies that interrupting a write
// leaves the previous good state intact. Simulated by writing a known
// state, then writing a sentinel tempfile manually, then loading —
// the loaded result should be the original, not the tempfile.
func TestSave_AtomicViaTempRename(t *testing.T) {
	path := setupCacheDir(t)
	first := &State{Workspaces: []Workspace{
		{SessionName: "first", RepoPath: "/r", Kind: "worktree"},
	}}
	if err := Save(first); err != nil {
		t.Fatal(err)
	}
	// Simulate a half-finished write: write a junk tempfile in the
	// same dir. Save uses os.CreateTemp which generates a unique name,
	// so leaving this on disk shouldn't affect anything; loading should
	// still see "first".
	junk := filepath.Join(filepath.Dir(path), ".state-aborted.json.tmp")
	_ = os.WriteFile(junk, []byte("incomplete"), 0o644)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load after junk tempfile: %v", err)
	}
	if got == nil || len(got.Workspaces) != 1 || got.Workspaces[0].SessionName != "first" {
		t.Errorf("partial-write tempfile leaked into Load: %+v", got)
	}
}

// TestUpdateWindow_OnNewWorkspace verifies UpdateWindow's
// auto-create-workspace path. The created workspace lacks
// RepoPath/Kind, so the load-side filter drops it. This documents
// the intended interaction: callers must seed RepoPath/Kind via
// UpdateWorkspace (or RegisterCreatedWorkspace) before UpdateWindow
// payloads will survive a round-trip.
func TestUpdateWindow_OnNewWorkspace_RequiresScopeToPersist(t *testing.T) {
	setupCacheDir(t)
	err := UpdateWindow("repo-a", "feat/x", func(w *Window) {
		w.Recap = "hello"
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := Load()
	// No RepoPath/Kind on the workspace → filter drops it on load.
	if s != nil && len(s.Workspaces) != 0 {
		t.Errorf("workspace without RepoPath/Kind should be filtered, got %+v", s.Workspaces)
	}
}

func TestUpdateWindow_MutatesExistingAtelierWorkspace(t *testing.T) {
	setupCacheDir(t)
	// Seed with a properly-scoped workspace.
	_ = Save(&State{Workspaces: []Workspace{
		{SessionName: "repo-a", RepoPath: "/r", Kind: "worktree",
			Windows: []Window{{Name: "main", Recap: "old"}},
		},
	}})
	_ = UpdateWindow("repo-a", "main", func(w *Window) {
		w.Recap = "new"
	})
	s, _ := Load()
	if s == nil || len(s.Workspaces) != 1 {
		t.Fatalf("workspace lost on round-trip: %+v", s)
	}
	if s.Workspaces[0].Windows[0].Recap != "new" {
		t.Errorf("recap not updated: %+v", s.Workspaces[0].Windows[0])
	}
	if len(s.Workspaces[0].Windows) != 1 {
		t.Errorf("window duplicated on update: %+v", s.Workspaces[0].Windows)
	}
}

func TestUpdateGlobal_SetAndDelete(t *testing.T) {
	setupCacheDir(t)
	_ = UpdateGlobal("k8s_active", "prod")
	s, _ := Load()
	if s.Globals["k8s_active"] != "prod" {
		t.Errorf("global not set: %+v", s.Globals)
	}
	_ = UpdateGlobal("k8s_active", "")
	s, _ = Load()
	if _, ok := s.Globals["k8s_active"]; ok {
		t.Errorf("global not deleted: %+v", s.Globals)
	}
}

func TestRemoveSession_DropsWorkspace(t *testing.T) {
	setupCacheDir(t)
	_ = Save(&State{Workspaces: []Workspace{
		{SessionName: "a", RepoPath: "/r-a", Kind: "worktree"},
		{SessionName: "b", RepoPath: "/r-b", Kind: "worktree"},
	}})
	_ = RemoveSession("a")
	s, _ := Load()
	if len(s.Workspaces) != 1 || s.Workspaces[0].SessionName != "b" {
		t.Errorf("RemoveSession left wrong state: %+v", s.Workspaces)
	}
}

func TestRemoveWindow_DropsWindow_AndRemovesEmptyWorkspace(t *testing.T) {
	setupCacheDir(t)
	_ = Save(&State{Workspaces: []Workspace{
		{SessionName: "a", RepoPath: "/r-a", Kind: "worktree", Windows: []Window{
			{Name: "main"},
			{Name: "feat/x"},
		}},
		{SessionName: "b", RepoPath: "/r-b", Kind: "worktree", Windows: []Window{{Name: "only"}}},
	}})
	// Drop one window from `a`; workspace `a` remains with one window.
	_ = RemoveWindow("a", "main")
	s, _ := Load()
	if len(s.Workspaces) != 2 {
		t.Fatalf("workspace count changed: %+v", s.Workspaces)
	}
	// Drop the LAST window from `b`; workspace `b` should disappear.
	_ = RemoveWindow("b", "only")
	s, _ = Load()
	if len(s.Workspaces) != 1 || s.Workspaces[0].SessionName != "a" {
		t.Errorf("emptied workspace not removed: %+v", s.Workspaces)
	}
}

func TestRenameWindow_UpdatesName(t *testing.T) {
	setupCacheDir(t)
	_ = Save(&State{Workspaces: []Workspace{
		{SessionName: "a", RepoPath: "/r", Kind: "worktree",
			Windows: []Window{{Name: "old", Recap: "carry over"}},
		},
	}})
	_ = RenameWindow("a", "old", "new")
	s, _ := Load()
	w := s.Workspaces[0].Windows[0]
	if w.Name != "new" {
		t.Errorf("rename failed: %+v", w)
	}
	if w.Recap != "carry over" {
		t.Errorf("rename clobbered other fields: %+v", w)
	}
}

func TestFindWindow_ReturnsPointerOrNil(t *testing.T) {
	s := &State{Workspaces: []Workspace{
		{SessionName: "a", Windows: []Window{{Name: "x"}}},
	}}
	if got := s.FindWindow("a", "x"); got == nil || got.Name != "x" {
		t.Errorf("FindWindow hit: got %+v", got)
	}
	if got := s.FindWindow("a", "missing"); got != nil {
		t.Errorf("FindWindow miss should return nil, got %+v", got)
	}
	if got := s.FindWindow("missing", "x"); got != nil {
		t.Errorf("FindWindow miss-session should return nil, got %+v", got)
	}
}

// TestLoad_FiltersNonAtelierWorkspaces verifies the load-side scope
// filter: legacy / leaked entries without RepoPath or Kind get
// silently dropped. This is the migration path for caches that
// accumulated random tmux sessions before the write-through scope
// check landed.
func TestLoad_FiltersNonAtelierWorkspaces(t *testing.T) {
	path := setupCacheDir(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	bogus := `{
  "schema_version": 2,
  "workspaces": [
    {"session_name":"random-shell", "windows":[{"name":"zsh"}]},
    {"session_name":"real-ws", "repo_path":"/repo", "kind":"worktree", "windows":[{"name":"main"}]},
    {"session_name":"multi-repo-ws", "kind":"multi-repo", "windows":[{"name":"1"}]},
    {"session_name":"another-shell", "windows":[{"name":"x","attention":true}]}
  ]
}`
	if err := os.WriteFile(path, []byte(bogus), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s == nil || len(s.Workspaces) != 2 {
		t.Fatalf("expected 2 atelier-managed workspaces, got %d: %+v",
			len(s.Workspaces), s)
	}
	want := map[string]bool{"real-ws": true, "multi-repo-ws": true}
	for _, ws := range s.Workspaces {
		if !want[ws.SessionName] {
			t.Errorf("unexpected workspace kept: %q", ws.SessionName)
		}
	}
}

// TestSave_FiltersNonAtelierWorkspaces locks in the save-side
// counterpart: even if memory state has random entries (e.g. a buggy
// caller wrote them), they don't make it to disk.
func TestSave_FiltersNonAtelierWorkspaces(t *testing.T) {
	setupCacheDir(t)
	in := &State{Workspaces: []Workspace{
		{SessionName: "ghost"},                                         // dropped
		{SessionName: "real", RepoPath: "/r"},                          // kept
		{SessionName: "multi", Kind: "multi-repo"},                     // kept
		{SessionName: "another-ghost", Windows: []Window{{Name: "x"}}}, // dropped
	}}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	out, _ := Load()
	if out == nil || len(out.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces persisted, got %+v", out)
	}
}

// TestPath_FixedAndEnvIndependent locks in the determinism the cache
// depends on: the filename is a FIXED `state.json`, regardless of hostname
// or ATELIER_TMUX_SOCKET. This is what makes a seed and a later read (in
// the same process, a subprocess, or a fresh server on a different socket)
// always resolve to the same file — the property hostname- and
// socket-keying both failed to guarantee.
func TestPath_FixedAndEnvIndependent(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	want := filepath.Join(cache, "atelier", "state.json")

	for _, sock := range []string{"", "atelier", "atelier-test-abc123", "/tmp/tmux-501/x"} {
		t.Setenv("ATELIER_TMUX_SOCKET", sock)
		if got := Path(); got != want {
			t.Errorf("Path() with ATELIER_TMUX_SOCKET=%q = %q, want %q (must be env-independent)", sock, got, want)
		}
	}
}
