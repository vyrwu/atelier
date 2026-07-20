//go:build e2e

package workspace_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestCreateWorktreeWindow_DottedSessionName is the regression guard for the
// "creating a workspace for cloudnativedenmark.dk breaks" bug. With the
// SessionName-normalized identity, EnsureSession + CreateWorktreeWindow
// resolve cleanly end-to-end. Before the fix the raw "…dk" target had tmux
// parsing ".dk" as a window/pane, so every -t operation failed.
func TestCreateWorktreeWindow_DottedSessionName(t *testing.T) {
	srv := testtmux.New(t)

	session := workspace.SessionName("cloudnativedenmark/cloudnativedenmark.dk")
	if strings.ContainsAny(session, ".:") {
		t.Fatalf("SessionName left a tmux delimiter in %q", session)
	}

	repoPath := t.TempDir()
	created, err := workspace.EnsureSession(srv.Client, session, repoPath, "main")
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if !created {
		t.Fatalf("EnsureSession created=false, want true for a fresh session")
	}
	// The session must be resolvable by the SAME name we used to create it —
	// this is exactly what broke when the name still carried the dot.
	if has, _ := srv.Client.HasSession(session); !has {
		t.Fatalf("session %q not resolvable after EnsureSession", session)
	}

	wid, err := workspace.CreateWorktreeWindow(srv.Client, workspace.WorktreeWindowSpec{
		Session:    session,
		WtPath:     t.TempDir(),
		WindowName: "feat/add-sponsor-logos",
		Kind:       "worktree",
	})
	if err != nil {
		t.Fatalf("CreateWorktreeWindow: %v", err)
	}
	if !strings.HasPrefix(wid, "@") {
		t.Fatalf("window id is not a tmux @ID (poisoned target?): %q", wid)
	}
}

// TestCreateWorktreeWindow_MissingSessionReturnsError guards Bug B: on a failed
// new-window, tmux returns its stderr AS the command output. The old code
// captured that "can't find session: …" text as the window @ID and fed it into
// every downstream -t target. A failed creation must surface an error and an
// empty id instead.
func TestCreateWorktreeWindow_MissingSessionReturnsError(t *testing.T) {
	srv := testtmux.New(t)

	wid, err := workspace.CreateWorktreeWindow(srv.Client, workspace.WorktreeWindowSpec{
		Session:    "vyrwu/does-not-exist",
		WtPath:     t.TempDir(),
		WindowName: "feat/x",
		Kind:       "worktree",
	})
	if err == nil {
		t.Fatalf("expected error for missing session, got wid=%q", wid)
	}
	if wid != "" {
		t.Fatalf("window id must be empty on failure, got %q (error text used as id?)", wid)
	}
}
