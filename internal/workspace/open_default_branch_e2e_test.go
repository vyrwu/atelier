//go:build e2e

package workspace_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestOpenDefaultBranch_AtomicCreation locks in the lifecycle
// primitive's contract: ONE call performs the whole "open default
// branch" sequence atomically (EnsureSession → ensure window →
// LandOuter → SpawnBgPull → RegisterCreatedWorkspace).
//
// Before this primitive existed, callers in tools/workspaces.go
// inlined the same 5-step dance at multiple callsites with subtle
// drift: clone path forgot RegisterCreatedWorkspace for a while,
// the prompt-flow default-branch path duplicated 25 lines verbatim
// from the auto-flow path. This test guards the consolidation —
// any future regression (skipping the register, forgetting the
// session creation, etc.) fails here.
func TestOpenDefaultBranch_AtomicCreation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := testtmux.New(t)
	srv.NewSession("seed") // boot the server

	// Bare git repo to point @repo_path at. Doesn't need to be a
	// real worktree — OpenDefaultBranch's bg-pull would fail and
	// log, but the synchronous parts (session/window/register)
	// proceed regardless.
	repoDir := filepath.Join(t.TempDir(), "fakerepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repoDir, "init", "--initial-branch=main")
	mustGit(t, repoDir, "commit", "--allow-empty", "-m", "init")

	client := tmuxhost.New(srv.Socket)

	ensureNoop := func(h *tmuxhost.Client, session, repoPath, branch string) error {
		// EnsureSession already created session with window-1 named
		// `branch`; no need to do anything else for the test.
		return nil
	}

	const sessionName = "fake/repo"
	if err := workspace.OpenDefaultBranch(
		client, sessionName, repoDir, "main", ensureNoop); err != nil {
		t.Fatalf("OpenDefaultBranch: %v", err)
	}

	// Assertion 1: tmux session was created.
	if has, _ := client.HasSession(sessionName); !has {
		t.Fatalf("session %q not created", sessionName)
	}

	// Assertion 2: @repo_path option stamped on the session.
	out, err := client.Run("show-option", "-t", sessionName, "-v", workspace.OptRepoPath)
	if err != nil {
		t.Fatalf("show-option @repo_path: %v", err)
	}
	if got := string(out); !contains(got, repoDir) {
		t.Errorf("@repo_path = %q, want substring %q", got, repoDir)
	}

	// Assertion 3: workspace registered in the on-disk statestore.
	state, err := statestore.Load()
	if err != nil {
		t.Fatalf("statestore.Load: %v", err)
	}
	if state == nil {
		t.Fatal("statestore is empty — RegisterCreatedWorkspace was skipped")
	}
	found := false
	for _, ws := range state.Workspaces {
		if ws.SessionName == sessionName {
			found = true
			if ws.Kind != "default-branch" {
				t.Errorf("workspace kind = %q, want %q", ws.Kind, "default-branch")
			}
			if ws.RepoPath != repoDir {
				t.Errorf("workspace repo_path = %q, want %q", ws.RepoPath, repoDir)
			}
		}
	}
	if !found {
		t.Errorf("session %q not in statestore. cached: %+v", sessionName, state.Workspaces)
	}
}

// Idempotency: calling OpenDefaultBranch twice on the same session
// should not double-register or error. EnsureSession early-returns
// when the session exists; we want the whole primitive to compose
// correctly on the second call.
func TestOpenDefaultBranch_Idempotent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := testtmux.New(t)
	srv.NewSession("seed")

	repoDir := filepath.Join(t.TempDir(), "fakerepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repoDir, "init", "--initial-branch=main")
	mustGit(t, repoDir, "commit", "--allow-empty", "-m", "init")

	client := tmuxhost.New(srv.Socket)
	ensureNoop := func(h *tmuxhost.Client, session, repoPath, branch string) error {
		return nil
	}
	for i := 0; i < 2; i++ {
		if err := workspace.OpenDefaultBranch(
			client, "fake/repo", repoDir, "main", ensureNoop); err != nil {
			t.Fatalf("call %d: OpenDefaultBranch: %v", i, err)
		}
	}
	// Only one session named fake/repo should exist.
	sessions, _ := client.ListSessions()
	count := 0
	for _, s := range sessions {
		if s == "fake/repo" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 session named fake/repo, got %d. all: %v", count, sessions)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
