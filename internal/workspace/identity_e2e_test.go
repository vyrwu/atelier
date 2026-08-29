//go:build e2e

package workspace_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestStampWorkspaceIdentity_OptionsAndCacheMirror locks the identity primitive:
// StampWorkspaceIdentity writes @workspace_id/title/intent/root on the SESSION
// (resolved at window scope via tmux inheritance) and mirrors title/intent/root
// into the statestore so a rename or restore survives a tmux server restart.
func TestStampWorkspaceIdentity_OptionsAndCacheMirror(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := testtmux.New(t)
	srv.NewSession("vyrwu/atelier")
	time.Sleep(100 * time.Millisecond)

	const (
		id     = "vyrwu/atelier"
		title  = "Ship the redesign"
		intent = "rework the workspace model"
		root   = "/tmp/ateliers/atelier"
	)
	if err := workspace.StampWorkspaceIdentity(srv.Client, "vyrwu/atelier", id, title, intent, root); err != nil {
		t.Fatalf("StampWorkspaceIdentity: %v", err)
	}

	// Session options are stamped on the session and resolve at window scope
	// via tmux option inheritance (the documented contract the picker relies
	// on). Read them via `display-message #{@opt}` at the window target — the
	// inheritance-aware read. (Note: tmuxhost.GetWindowOption uses
	// show-window-options, which does NOT walk to session scope — see the
	// report's bug note; we assert the real inheritance path here.)
	wid, err := srv.Client.DisplayMessageAt("vyrwu/atelier", "#{window_id}")
	if err != nil || wid == "" {
		t.Fatalf("window id: %v", err)
	}
	optChecks := map[string]string{
		workspace.OptWorkspaceID:     id,
		workspace.OptWorkspaceTitle:  title,
		workspace.OptWorkspaceIntent: intent,
		workspace.OptWorkspaceRoot:   root,
	}
	for opt, want := range optChecks {
		got := windowScopeOption(t, srv, wid, opt)
		if got != want {
			t.Errorf("%s (window-scope inheritance) = %q, want %q", opt, got, want)
		}
	}

	// Cache mirror: title/intent/root persisted on the Workspace record.
	ws := loadWorkspace(t, "vyrwu/atelier")
	if ws == nil {
		t.Fatal("workspace not in cache after StampWorkspaceIdentity")
	}
	if ws.Title != title || ws.Intent != intent || ws.Root != root {
		t.Errorf("cache mirror = {title:%q intent:%q root:%q}, want {%q %q %q}",
			ws.Title, ws.Intent, ws.Root, title, intent, root)
	}
}

// TestStampWorkspaceIdentity_RequiresID rejects an empty id (the Listable
// marker must never be blank).
func TestStampWorkspaceIdentity_RequiresID(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("s")
	if err := workspace.StampWorkspaceIdentity(srv.Client, "s", "", "t", "i", "r"); err == nil {
		t.Errorf("expected error for empty id, got nil")
	}
}

// TestSetTitle_RenamesLabelNotSession locks the M-r rename: SetTitle moves
// @workspace_title and the cached Title, but never the session NAME (the tmux
// target every switch/kill depends on). Empty title clears the option.
func TestSetTitle_RenamesLabelNotSession(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := testtmux.New(t)
	srv.NewSession("vyrwu/atelier")
	time.Sleep(100 * time.Millisecond)

	// Register so the record survives the atelier-managed Save filter.
	wname, _ := srv.Client.DisplayMessageAt("vyrwu/atelier", "#{window_name}")
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session:    "vyrwu/atelier",
		Title:      "atelier",
		Root:       "/tmp/atelier",
		WindowName: wname,
		Cwd:        "/tmp/atelier",
	})

	if err := workspace.SetTitle(srv.Client, "vyrwu/atelier", "New Name"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	// The session name (tmux target) is untouched.
	if has, _ := srv.Client.HasSession("vyrwu/atelier"); !has {
		t.Fatal("session name changed — SetTitle must not move the tmux target")
	}
	if got, _ := srv.Client.Run("show-option", "-t", "vyrwu/atelier", "-qv", workspace.OptWorkspaceTitle); strings.TrimSpace(string(got)) != "New Name" {
		t.Errorf("@workspace_title = %q, want New Name", strings.TrimSpace(string(got)))
	}
	if ws := loadWorkspace(t, "vyrwu/atelier"); ws == nil || ws.Title != "New Name" {
		t.Errorf("cached Title not updated: %+v", ws)
	}

	// Empty title clears the option.
	if err := workspace.SetTitle(srv.Client, "vyrwu/atelier", ""); err != nil {
		t.Fatalf("SetTitle clear: %v", err)
	}
	if got, _ := srv.Client.Run("show-option", "-t", "vyrwu/atelier", "-qv", workspace.OptWorkspaceTitle); strings.TrimSpace(string(got)) != "" {
		t.Errorf("@workspace_title after clear = %q, want empty", strings.TrimSpace(string(got)))
	}
}

// TestMarkDriver flags a window as the workspace's driver-agent window.
func TestMarkDriver(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("ws")
	time.Sleep(100 * time.Millisecond)

	wid, err := srv.Client.DisplayMessageAt("ws", "#{window_id}")
	if err != nil || wid == "" {
		t.Fatalf("window id: %v", err)
	}
	// Unmarked before.
	if drv, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceDriver); drv == "1" {
		t.Fatalf("precondition: window should not be a driver yet, got %q", drv)
	}

	workspace.MarkDriver(srv.Client, wid)

	if drv, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceDriver); drv != "1" {
		t.Errorf("@workspace_driver = %q, want 1", drv)
	}
}

// windowScopeOption reads a tmux user option at WINDOW scope with inheritance
// (window → session), via `display-message #{@opt}`. Unlike
// tmuxhost.GetWindowOption (show-window-options), this resolves session-scoped
// @-options the way the picker's `#{@workspace_id}` format does.
func windowScopeOption(t *testing.T, srv *testtmux.Server, windowID, opt string) string {
	t.Helper()
	// opt is like "@workspace_id"; the format spec is "#{@workspace_id}".
	out, err := srv.Client.DisplayMessageAt(windowID, "#{"+opt+"}")
	if err != nil {
		t.Fatalf("display-message #{%s}: %v", opt, err)
	}
	return out
}

func loadWorkspace(t *testing.T, session string) *statestore.Workspace {
	t.Helper()
	st, err := statestore.Load()
	if err != nil || st == nil {
		return nil
	}
	return st.FindWorkspace(session)
}
