package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/adapters/claude"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/plugin"
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
	cachePath := statestore.Path()
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
// TestCheckAgentHooks_SkipsWithoutAI: no AI integration configured → the
// check is a no-op SKIP, not a hardcoded-Claude WARN.
func TestCheckAgentHooks_SkipsWithoutAI(t *testing.T) {
	integration.SetActive(integration.Set{})
	defer integration.SetActive(integration.Set{})
	if r := checkAgentHooks(); r.Status != StatusSkip {
		t.Errorf("no AI configured should SKIP, got %s (%q)", r.Status, r.Detail)
	}
}

// TestCheckAgentHooks_InstallsViaClaudeAdapter: with the claude adapter
// active, EnsureHooks installs the canonical settings (with the Stop hook)
// and the check PASSes.
func TestCheckAgentHooks_InstallsViaClaudeAdapter(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	integration.SetActive(integration.Set{AI: claude.New()})
	defer integration.SetActive(integration.Set{})

	r := checkAgentHooks()
	if r.Status != StatusPass {
		t.Fatalf("claude adapter should install hooks + PASS, got %s (%q)", r.Status, r.Detail)
	}
	path := filepath.Join(cacheRoot, "atelier", "claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings not written at %s: %v", path, err)
	}
	if !strings.Contains(string(data), "atelier ai on-stop") {
		t.Errorf("settings should wire the kernel stop-hook; got %q", string(data))
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

// TestCheckToolsRegistered_ReportsBuiltins: in the single-binary model
// there are no atelier-* binaries to find on PATH. doctor instead reports
// the in-process registry — PASS naming the built-in count once at least
// one tool is registered.
func TestCheckToolsRegistered_ReportsBuiltins(t *testing.T) {
	// Isolate config so a stray real launcher can't flip PASS→WARN.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Register a fake built-in so the registry is non-empty in this test
	// binary (which does not import internal/tools/all). Uniquely named so
	// it can't collide with anything else the package's tests register.
	plugin.RegisterBuiltin(
		&manifest.Manifest{Name: "__doctor_test_tool", Description: "fake"},
		func(*cobra.Command) {})

	r := checkToolsRegistered()
	if r.Status != StatusPass {
		t.Fatalf("with a built-in registered, expected PASS, got %s (%q)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "built-in") {
		t.Errorf("PASS detail must mention built-in count, got %q", r.Detail)
	}
}
