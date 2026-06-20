//go:build e2e

package workspace_test

import (
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestFindWarmupCandidates_FiltersCorrectly locks in the warmup
// discovery contract: only windows whose session has @repo_path set
// AND whose own @workspace_freshness_ts is empty (and pull_error
// empty) should be selected. Sessions without @repo_path and
// already-warm windows must be skipped.
//
// Regression target: the "vyrwu/atelier:main has @repo_path but no
// freshness icon" report — the cache was empty so Restore did
// nothing, but the live session was a perfectly good warmup target
// that the old code never looked at.
func TestFindWarmupCandidates_FiltersCorrectly(t *testing.T) {
	srv := testtmux.New(t)

	srv.NewSession("vyrwu/atelier")
	srv.NewSession("plain-shell")
	srv.NewSession("already-warm")

	// vyrwu/atelier: @repo_path set, no freshness → SHOULD warm up.
	if _, err := srv.Client.Run("set-option", "-t", "vyrwu/atelier",
		workspace.OptRepoPath, "/fake/repo"); err != nil {
		t.Fatal(err)
	}
	// plain-shell: no @repo_path → MUST skip (non-git workspace).
	// already-warm: @repo_path set + freshness_ts → MUST skip (idempotent).
	if _, err := srv.Client.Run("set-option", "-t", "already-warm",
		workspace.OptRepoPath, "/fake/repo2"); err != nil {
		t.Fatal(err)
	}
	wid, _ := srv.Client.Run("list-windows", "-t", "=already-warm", "-F", "#{window_id}")
	awid := string(wid)
	if n := len(awid); n > 0 && awid[n-1] == '\n' {
		awid = awid[:n-1]
	}
	if _, err := srv.Client.Run("set-option", "-w", "-t", awid,
		workspace.OptWorkspaceFreshnessTs, "9999"); err != nil {
		t.Fatal(err)
	}

	got := workspace.FindWarmupCandidates(srv.Client)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 candidate, got %d: %+v", len(got), got)
	}
	if got[0].SessionName != "vyrwu/atelier" {
		t.Errorf("wrong session selected: %q (want vyrwu/atelier). all: %+v",
			got[0].SessionName, got)
	}
	if got[0].RepoPath != "/fake/repo" {
		t.Errorf("repo path: got %q want /fake/repo", got[0].RepoPath)
	}
}
