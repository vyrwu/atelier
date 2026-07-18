//go:build e2e

package workspaces_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestSessionList_RendersTagPill proves the end-to-end render path: a
// window stamped with @workspace_tag surfaces a "#tag" pill in the M-s
// picker rows (BuildSessionList → _session-list), and clearing the tag
// removes it. The interactive tag picker (M-t → nested fzf) needs pty
// driving; the SetTag primitive + choice logic are covered by unit and
// workspace-package e2e tests.
func TestSessionList_RendersTagPill(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")
	srv.SourceInit(t)
	_ = srv.Attach(t, "main")
	time.Sleep(200 * time.Millisecond)

	tmp := t.TempDir()
	repoDir := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")
	srv.SetEnv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	srv.SetEnv("HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))

	if _, err := srv.RunAtelier("tools", "workspaces", "_name",
		"vyrwu/demo", repoDir, "main", "feat-tag"); err != nil {
		t.Fatalf("create: %v", err)
	}
	wid, err := srv.Client.DisplayMessageAt("vyrwu/demo:feat-tag", "#{window_id}")
	if err != nil || wid == "" {
		t.Fatalf("window id: %v", err)
	}
	if _, err := srv.Client.Run("set-window-option", "-t", wid, "@workspace_tag", "client-x"); err != nil {
		t.Fatalf("stamp tag: %v", err)
	}

	out, err := srv.RunAtelier("tools", "workspaces", "_session-list")
	if err != nil {
		t.Fatalf("_session-list: %v\n%s", err, out)
	}
	// The colored pill ends with an SGR reset — asserting "#client-x\033[0m"
	// proves both the text and that it rendered as a styled pill.
	if !strings.Contains(string(out), "#client-x\033[0m") {
		t.Errorf("expected colored tag pill #client-x in session list, got:\n%q", out)
	}

	if _, err := srv.Client.Run("set-window-option", "-t", wid, "-u", "@workspace_tag"); err != nil {
		t.Fatalf("clear tag: %v", err)
	}
	out2, err := srv.RunAtelier("tools", "workspaces", "_session-list")
	if err != nil {
		t.Fatalf("_session-list after clear: %v\n%s", err, out2)
	}
	if strings.Contains(string(out2), "#client-x") {
		t.Errorf("pill must be gone after clearing the tag, got:\n%q", out2)
	}
}
