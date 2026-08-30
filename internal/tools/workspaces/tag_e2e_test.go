//go:build e2e

package workspaces_test

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/tools/workspaces"
)

// TestSessionList_RendersTagPill proves the end-to-end tag path: a window
// stamped with @workspace_tag surfaces on the M-s picker row's Tag field
// (BuildSessionList), and clearing the tag empties it. The interactive tag
// picker (M-t) is covered by TUI-package tests; the SetTag primitive +
// choice logic are covered by unit and workspace-package e2e tests.
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

	tagFor := func(session, window string) (string, bool) {
		rows, err := workspaces.BuildSessionList(srv.Client)
		if err != nil {
			t.Fatalf("BuildSessionList: %v", err)
		}
		for _, r := range rows {
			if r.Session == session && r.Window == window {
				return r.Tag, true
			}
		}
		return "", false
	}

	if tag, ok := tagFor("vyrwu/demo", "feat-tag"); !ok || tag != "client-x" {
		t.Errorf("expected tag client-x on feat-tag row, got tag=%q found=%v", tag, ok)
	}

	if _, err := srv.Client.Run("set-window-option", "-t", wid, "-u", "@workspace_tag"); err != nil {
		t.Fatalf("clear tag: %v", err)
	}
	if tag, ok := tagFor("vyrwu/demo", "feat-tag"); !ok || tag != "" {
		t.Errorf("tag must be empty after clearing, got tag=%q found=%v", tag, ok)
	}
}
