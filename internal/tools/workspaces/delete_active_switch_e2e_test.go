//go:build e2e

package workspaces_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// outerClientOn asserts (via Eventually) that the outer client
// `clientName` is still attached AND lands on session `wantSession`.
// Before the delete-active-workspace fix, killing the attached session
// detached the client outright (its name vanished from list-clients) —
// this helper distinguishes "switched" from "detached".
func outerClientOn(t *testing.T, srv *testtmux.Server, clientName, wantSession string) {
	t.Helper()
	testtmux.Eventually(t, 3*time.Second, func() error {
		out, err := srv.Client.Run("list-clients", "-F", "#{client_name}|#{client_session}")
		if err != nil {
			return err
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(line, "|", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == clientName {
				if strings.TrimSpace(parts[1]) == wantSession {
					return nil
				}
				return fmt.Errorf("outer client on %q, want %q", strings.TrimSpace(parts[1]), wantSession)
			}
		}
		return fmt.Errorf("outer client %q detached (gone from list-clients)", clientName)
	})
}

// seedWorkspaceSession creates a detached workspace session `name` with a
// single window `window`, stamped with @repo_path so BuildSessionList
// treats it as a real workspace (not an internal popup session).
func seedWorkspaceSession(t *testing.T, srv *testtmux.Server, name, window, repoDir string) {
	t.Helper()
	if _, err := srv.Client.Run("new-session", "-d", "-s", name, "-c", repoDir, "-n", window); err != nil {
		t.Fatalf("seed session %s: %v", name, err)
	}
	if _, err := srv.Client.Run("set-option", "-t", name, "@repo_path", repoDir); err != nil {
		t.Fatalf("seed @repo_path %s: %v", name, err)
	}
}

func registerOuterClient(t *testing.T, srv *testtmux.Server, session string) string {
	t.Helper()
	out, _ := srv.Client.Run("list-clients", "-t", "="+session, "-F", "#{client_name}")
	clientName := strings.TrimSpace(string(out))
	if clientName == "" {
		t.Fatalf("no attached client on %q", session)
	}
	if err := srv.Client.SetGlobalOption("@atelier_outer_client", clientName); err != nil {
		t.Fatalf("set @atelier_outer_client: %v", err)
	}
	return clientName
}

// TestDeleteRow_ActiveDefaultBranch_SwitchesInsteadOfDetaching locks in
// the fix: deleting the CURRENTLY ACTIVE workspace via the default-branch
// (kill-session) path must land the outer client on another workspace,
// not detach tmux. The M-s popup rides on the outer client, so keeping
// the client attached is what keeps the popup alive.
func TestDeleteRow_ActiveDefaultBranch_SwitchesInsteadOfDetaching(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("bootstrap") // keep the server alive independent of workspaces
	time.Sleep(150 * time.Millisecond)

	// Build the atelier-* binaries before overriding HOME below, so the
	// go build's module cache isn't written under the read-only test
	// tmpdir (which then fails t.TempDir cleanup).
	_ = srv.BinDir()

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	// Victim (active) workspace + a sibling to land on.
	seedWorkspaceSession(t, srv, "vyrwu/demo", "main", repoDir)
	seedWorkspaceSession(t, srv, "vyrwu/other", "main", repoDir)

	_ = srv.Attach(t, "vyrwu/demo")
	clientName := registerOuterClient(t, srv, "vyrwu/demo")

	// Delete the active workspace's default-branch row → kill-session path.
	if _, err := srv.RunAtelier("tools", "workspaces", "_delete-row",
		"vyrwu/demo\tmain\t<display>"); err != nil {
		t.Fatalf("_delete-row: %v", err)
	}

	testtmux.Eventually(t, 3*time.Second, func() error {
		if has, _ := srv.Client.HasSession("vyrwu/demo"); has {
			return fmt.Errorf("victim session vyrwu/demo still present")
		}
		return nil
	})
	outerClientOn(t, srv, clientName, "vyrwu/other")
}

// TestDeleteRow_ActiveSoleWindow_SwitchesInsteadOfDetaching covers the
// other empties-the-session path: a workspace whose SOLE window is a
// non-default branch. kill-window on the only window destroys the
// session too, so the outer must hop to another workspace first.
func TestDeleteRow_ActiveSoleWindow_SwitchesInsteadOfDetaching(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("bootstrap")
	time.Sleep(150 * time.Millisecond)

	// Build the atelier-* binaries before overriding HOME below, so the
	// go build's module cache isn't written under the read-only test
	// tmpdir (which then fails t.TempDir cleanup).
	_ = srv.BinDir()

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	// Victim's sole window is "feat" (repo default branch is "main") →
	// non-default sole window → kill-window empties the session.
	seedWorkspaceSession(t, srv, "vyrwu/demo", "feat", repoDir)
	seedWorkspaceSession(t, srv, "vyrwu/other", "main", repoDir)

	_ = srv.Attach(t, "vyrwu/demo")
	clientName := registerOuterClient(t, srv, "vyrwu/demo")

	if _, err := srv.RunAtelier("tools", "workspaces", "_delete-row",
		"vyrwu/demo\tfeat\t<display>"); err != nil {
		t.Fatalf("_delete-row: %v", err)
	}

	testtmux.Eventually(t, 3*time.Second, func() error {
		if has, _ := srv.Client.HasSession("vyrwu/demo"); has {
			return fmt.Errorf("victim session vyrwu/demo still present")
		}
		return nil
	})
	outerClientOn(t, srv, clientName, "vyrwu/other")
}

// TestDeleteRow_InactiveWorkspace_DoesNotMoveOuter guards the no-op
// guard: deleting a workspace the outer is NOT on must leave the outer
// exactly where it is (on its own workspace), not yank it elsewhere.
func TestDeleteRow_InactiveWorkspace_DoesNotMoveOuter(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := testtmux.New(t)
	srv.NewSession("bootstrap")
	time.Sleep(150 * time.Millisecond)

	// Build the atelier-* binaries before overriding HOME below, so the
	// go build's module cache isn't written under the read-only test
	// tmpdir (which then fails t.TempDir cleanup).
	_ = srv.BinDir()

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	seedWorkspaceSession(t, srv, "vyrwu/keep", "main", repoDir)   // outer stays here
	seedWorkspaceSession(t, srv, "vyrwu/doomed", "main", repoDir) // deleted while inactive

	_ = srv.Attach(t, "vyrwu/keep")
	clientName := registerOuterClient(t, srv, "vyrwu/keep")

	if _, err := srv.RunAtelier("tools", "workspaces", "_delete-row",
		"vyrwu/doomed\tmain\t<display>"); err != nil {
		t.Fatalf("_delete-row: %v", err)
	}

	testtmux.Eventually(t, 3*time.Second, func() error {
		if has, _ := srv.Client.HasSession("vyrwu/doomed"); has {
			return fmt.Errorf("doomed session still present")
		}
		return nil
	})
	// Outer must remain on vyrwu/keep — the delete of an inactive
	// workspace must not relocate it.
	outerClientOn(t, srv, clientName, "vyrwu/keep")
}
