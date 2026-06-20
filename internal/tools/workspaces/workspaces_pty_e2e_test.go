//go:build e2e

// PTY-driven integration tests for the workspaces tool. Cover the
// M-n / M-s binding wiring (M-n → creator, M-s → session picker) and
// the manual-name creator flow that produces a real worktree + window
// against a real test repo on disk.
package workspaces_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestCreator_MN_FiresBinding asserts M-n triggers the workspaces
// creator binding chain (@atelier_outer_pane set as side effect).
func TestCreator_MN_FiresBinding(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	client := srv.Attach(t, "main")
	time.Sleep(300 * time.Millisecond)

	client.Send("\x1bn")
	testtmux.Eventually(t, 3*time.Second, func() error {
		v, _ := srv.Client.ShowGlobalOption("@atelier_outer_pane")
		if v == "" {
			return fmt.Errorf("@atelier_outer_pane unset; M-n binding did not fire")
		}
		return nil
	})
}

// TestSessionPicker_MS_FiresBinding asserts M-s triggers the workspaces
// session picker binding chain.
func TestSessionPicker_MS_FiresBinding(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	client := srv.Attach(t, "main")
	time.Sleep(300 * time.Millisecond)

	client.Send("\x1bs")
	testtmux.Eventually(t, 3*time.Second, func() error {
		v, _ := srv.Client.ShowGlobalOption("@atelier_outer_session")
		if v == "" {
			return fmt.Errorf("@atelier_outer_session unset; M-s binding did not fire")
		}
		return nil
	})
}

// TestCreator_NameFlow_CreatesSession drives the manual-name flow
// (workspaces _name) end-to-end with a real test repo on disk. Skips
// the picker by passing an explicit name, then verifies a session +
// worktree window were created against the test tmux server.
//
// Catches: the parseOutput query/expect swap (empty Enter producing
// "enter" workspace), ensureDefaultBranchWindow logic, Attach-creates-
// parallel-client bug, switch-client targeting.
func TestCreator_NameFlow_CreatesSession(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	// Attach a PTY-backed client so the creator's switch-client at the
	// end of the flow has a "current client" to switch.
	_ = srv.Attach(t, "main")
	time.Sleep(200 * time.Millisecond)

	tmp := t.TempDir()
	_ = testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	out, err := srv.RunAtelier("tools", "workspaces", "_name",
		"vyrwu/demo",
		testtmux.CodeRoot(tmp)+"/vyrwu/demo",
		"main",
		"feat-foo")
	if err != nil {
		t.Fatalf("workspaces _name: %v\n%s", err, out)
	}
	srv.MustHaveSession("vyrwu/demo")
	if !contains(srv.WindowsIn("vyrwu/demo"), "feat-foo") {
		t.Fatalf("expected window 'feat-foo' in vyrwu/demo, got %v",
			srv.WindowsIn("vyrwu/demo"))
	}
}

// TestCreator_PreExistingSession_AddsWindow verifies that running the
// creator against an existing session adds a new window (rather than
// erroring or creating a duplicate session).
func TestCreator_PreExistingSession_AddsWindow(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	_ = srv.Attach(t, "main")
	time.Sleep(200 * time.Millisecond)

	tmp := t.TempDir()
	_ = testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	repoPath := testtmux.CodeRoot(tmp) + "/vyrwu/demo"

	// Create the first workspace.
	if _, err := srv.RunAtelier("tools", "workspaces", "_name",
		"vyrwu/demo", repoPath, "main", "feat-one"); err != nil {
		t.Fatalf("first _name: %v", err)
	}
	// Create a second one — same session, new window.
	if _, err := srv.RunAtelier("tools", "workspaces", "_name",
		"vyrwu/demo", repoPath, "main", "feat-two"); err != nil {
		t.Fatalf("second _name: %v", err)
	}
	wins := srv.WindowsIn("vyrwu/demo")
	if !contains(wins, "feat-one") || !contains(wins, "feat-two") {
		t.Fatalf("expected both windows present, got %v", wins)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
