package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
)

// TestCheckStatestoreParseable_NoCache locks the FIRST-RUN case:
// no cache file yet is NOT an error — it's the normal pre-launch
// state. Pre-FR-1.2 there was no check at all; we must not turn
// the empty case into a noisy WARN/FAIL or every fresh install
// will look broken.
func TestCheckStatestoreParseable_NoCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	r := checkStatestoreParseable()
	if r.Status != StatusPass {
		t.Errorf("no-cache should PASS, got %s (detail=%q)", r.Status, r.Detail)
	}
}

// TestCheckStatestoreParseable_Corrupt locks the diagnostic: a
// half-written cache file is the silent-failure mode that
// motivated this check. Doctor must FAIL loudly with a remediation
// hint, not pass quietly.
func TestCheckStatestoreParseable_Corrupt(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	// Write garbage to the path statestore.Load reads.
	cachePath := filepath.Join(cacheRoot, "atelier", "state-"+statestoreHostSuffix()+".json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("not json {{{"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := checkStatestoreParseable()
	if r.Status != StatusFail {
		t.Errorf("corrupt cache should FAIL, got %s (detail=%q)", r.Status, r.Detail)
	}
	if r.Remediation == "" {
		t.Errorf("FAIL result must include remediation")
	}
}

// TestCheckClaudeSettings_AutoCreates locks the FR-1.2 spec point:
// missing settings.json is auto-created (not just warned about).
// The user shouldn't have to manually mkdir + touch to get past
// "claude tool errors with cryptic missing-file message."
func TestCheckClaudeSettings_AutoCreates(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	expected := filepath.Join(cacheRoot, "atelier", "claude", "settings.json")
	if _, err := os.Stat(expected); err == nil {
		t.Fatalf("precondition failed: %s already exists", expected)
	}

	r := checkClaudeSettings()
	if r.Status != StatusWarn {
		t.Errorf("first-time creation should WARN (so user notices), got %s", r.Status)
	}
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("file not created at %s: %v", expected, err)
	}
	if strings.TrimSpace(string(data)) != "{}" {
		t.Errorf("auto-created file should be `{}`, got %q", string(data))
	}
}

// TestCheckClaudeSettings_AlreadyPresent: when the file exists, PASS
// silently. No bookkeeping noise on every doctor run.
func TestCheckClaudeSettings_AlreadyPresent(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	path := filepath.Join(cacheRoot, "atelier", "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"foo":"bar"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r := checkClaudeSettings()
	if r.Status != StatusPass {
		t.Errorf("present-file should PASS, got %s (detail=%q)", r.Status, r.Detail)
	}
}

// TestCheckWorktreeDirsExist_OrphanedEntry locks the "vanished
// worktree" diagnostic. Without this, the cache silently
// accumulates references to dirs the user `git worktree remove`d
// out-of-band — restore skips them but the picker shows them
// forever.
func TestCheckWorktreeDirsExist_OrphanedEntry(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	gone := filepath.Join(t.TempDir(), "deleted-worktree")
	live := t.TempDir()

	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{
			{
				SessionName: "ws/gone",
				RepoPath:    "/tmp/repo-gone",
				Kind:        "default-branch",
				Windows:     []statestore.Window{{Name: "main", Cwd: gone}},
			},
			{
				SessionName: "ws/live",
				RepoPath:    "/tmp/repo-live",
				Kind:        "default-branch",
				Windows:     []statestore.Window{{Name: "main", Cwd: live}},
			},
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := checkWorktreeDirsExist()
	if r.Status != StatusWarn {
		t.Errorf("orphan present should WARN, got %s (detail=%q)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "ws/gone") {
		t.Errorf("WARN must name the orphan; got %q", r.Detail)
	}
	if strings.Contains(r.Detail, "ws/live") {
		t.Errorf("live workspace must not be reported as orphan; got %q", r.Detail)
	}
}

// TestCheckWorktreeDirsExist_AllPresent: clean cache → PASS.
func TestCheckWorktreeDirsExist_AllPresent(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	live := t.TempDir()
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{
			{
				SessionName: "ws/live",
				RepoPath:    "/tmp/repo-live",
				Kind:        "default-branch",
				Windows:     []statestore.Window{{Name: "main", Cwd: live}},
			},
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r := checkWorktreeDirsExist()
	if r.Status != StatusPass {
		t.Errorf("all dirs present should PASS, got %s (%q)", r.Status, r.Detail)
	}
}

// TestCheckAtelierBinaries_MissingAreReported: doctor must NAME
// missing binaries so the user knows which install step failed
// without spelunking `make install` output.
func TestCheckAtelierBinaries_MissingAreReported(t *testing.T) {
	// Empty PATH guarantees every required atelier-* binary is missing.
	t.Setenv("PATH", "")
	r := checkAtelierBinaries()
	if r.Status != StatusWarn {
		t.Errorf("missing binaries should WARN, got %s", r.Status)
	}
	for _, bin := range []string{"atelier-workspaces", "atelier-claude"} {
		if !strings.Contains(r.Detail, bin) {
			t.Errorf("WARN detail must name missing binary %q, got %q", bin, r.Detail)
		}
	}
}

// statestoreHostSuffix mirrors statestore's per-host filename
// derivation just enough for the corrupt-file test to write to
// the actual path Load() reads. Kept local — production code
// doesn't need this exposed.
func statestoreHostSuffix() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	return host
}
