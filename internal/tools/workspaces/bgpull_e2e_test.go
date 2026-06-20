//go:build e2e

package workspaces

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestBgPull_StampsFreshnessOnSuccess locks in FR-7.3's option-stamping
// contract: after a successful fetch (no rebase needed on the bare
// repo), the four `@workspace_*` options must land on the target
// window.
func TestBgPull_StampsFreshnessOnSuccess(t *testing.T) {
	srv := testtmux.New(t)
	tmp := t.TempDir()
	repo := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")

	srv.NewSession("vyrwu/demo")
	time.Sleep(150 * time.Millisecond)
	out, _ := srv.Client.Run("list-windows", "-t", "=vyrwu/demo", "-F", "#{window_id}")
	wid := string(out)
	if n := len(wid); n > 0 && wid[n-1] == '\n' {
		wid = wid[:n-1]
	}

	if err := runBgPull(srv.Client, repo, "main", wid); err != nil {
		t.Fatalf("runBgPull: %v", err)
	}

	// Behind/ahead default to "0" against an isolated test repo with
	// no real remote — TestRepo wires up origin so fetch succeeds.
	behind, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceBehind)
	ahead, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceAhead)
	ts, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceFreshnessTs)
	pullErr, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspacePullError)

	if behind != "0" {
		t.Errorf("@workspace_behind: got %q want 0", behind)
	}
	if ahead != "0" {
		t.Errorf("@workspace_ahead: got %q want 0", ahead)
	}
	if ts == "" || ts == "0" {
		t.Errorf("@workspace_freshness_ts should be a non-zero unix epoch, got %q", ts)
	}
	if pullErr != "" {
		t.Errorf("@workspace_pull_error should be empty on success, got %q", pullErr)
	}
}

// TestBgPull_StampsErrorOnFailure: when the repoPath isn't a git
// repo (fetch fails), pull-error gets stamped + behind/ahead cleared.
// Lets the status-line ⚠ icon surface the failure passively.
func TestBgPull_StampsErrorOnFailure(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("vyrwu/demo")
	time.Sleep(150 * time.Millisecond)
	out, _ := srv.Client.Run("list-windows", "-t", "=vyrwu/demo", "-F", "#{window_id}")
	wid := string(out)
	if n := len(wid); n > 0 && wid[n-1] == '\n' {
		wid = wid[:n-1]
	}

	// Seed prior success so we can verify clearing.
	_ = srv.Client.SetWindowOption(wid, workspace.OptWorkspaceBehind, "5")
	_ = srv.Client.SetWindowOption(wid, workspace.OptWorkspaceAhead, "1")

	if err := runBgPull(srv.Client, "/nonexistent/repo", "main", wid); err == nil {
		t.Fatal("runBgPull on missing repo should error")
	}

	pullErr, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspacePullError)
	if pullErr == "" {
		t.Errorf("@workspace_pull_error should be set, got empty")
	}
	if v, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceBehind); v != "" {
		t.Errorf("@workspace_behind should be cleared on error, got %q", v)
	}
	if v, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceAhead); v != "" {
		t.Errorf("@workspace_ahead should be cleared on error, got %q", v)
	}
}
