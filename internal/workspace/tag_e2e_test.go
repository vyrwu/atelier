//go:build e2e

package workspace_test

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestSetTag_OptionAndCacheMirror locks in the workspace-tag primitive: SetTag
// is SESSION-scoped now — it writes @workspace_tag on the session (source of
// truth, resolved at window scope via inheritance), mirrors it to the
// statestore Workspace.Tag (so it survives a tmux restart), replaces on re-tag,
// and clears both on empty.
func TestSetTag_OptionAndCacheMirror(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := testtmux.New(t)
	srv.NewSession("vyrwu/demo")
	time.Sleep(150 * time.Millisecond)

	wid, err := srv.Client.DisplayMessageAt("vyrwu/demo", "#{window_id}")
	if err != nil || wid == "" {
		t.Fatalf("window id: %v", err)
	}
	wname, err := srv.Client.DisplayMessageAt("vyrwu/demo", "#{window_name}")
	if err != nil || wname == "" {
		t.Fatalf("window name: %v", err)
	}

	// Register the workspace first (with identity) so the cache record survives
	// the statestore's atelier-managed Save filter — a bare Tag alone wouldn't.
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session:    "vyrwu/demo",
		Title:      "demo",
		Root:       "/tmp/demo",
		WindowName: wname,
		Cwd:        "/tmp/demo",
	})

	if err := workspace.SetTag(srv.Client, "vyrwu/demo", "client-x"); err != nil {
		t.Fatalf("SetTag: %v", err)
	}
	// The tag is a SESSION option that resolves at window scope via tmux
	// inheritance (the picker reads it per row). Assert via the
	// inheritance-aware read.
	if got := windowScopeOption(t, srv, wid, workspace.OptWorkspaceTag); got != "client-x" {
		t.Errorf("@workspace_tag (window-scope inheritance) = %q, want client-x", got)
	}
	if got := cachedTag(t, "vyrwu/demo"); got != "client-x" {
		t.Errorf("cached workspace Tag = %q, want client-x", got)
	}

	// Re-tag replaces the previous value (one tag per workspace).
	if err := workspace.SetTag(srv.Client, "vyrwu/demo", "infra"); err != nil {
		t.Fatalf("re-tag: %v", err)
	}
	if got := windowScopeOption(t, srv, wid, workspace.OptWorkspaceTag); got != "infra" {
		t.Errorf("re-tag @workspace_tag = %q, want infra", got)
	}
	if got := cachedTag(t, "vyrwu/demo"); got != "infra" {
		t.Errorf("re-tag cached Tag = %q, want infra", got)
	}

	// Empty clears the option and the cache mirror.
	if err := workspace.SetTag(srv.Client, "vyrwu/demo", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := windowScopeOption(t, srv, wid, workspace.OptWorkspaceTag); got != "" {
		t.Errorf("after clear @workspace_tag = %q, want empty", got)
	}
	if got := cachedTag(t, "vyrwu/demo"); got != "" {
		t.Errorf("after clear cached Tag = %q, want empty", got)
	}
}

// cachedTag reads the persisted Workspace.Tag for a session from the on-disk
// statestore.
func cachedTag(t *testing.T, session string) string {
	t.Helper()
	st, err := statestore.Load()
	if err != nil || st == nil {
		t.Fatalf("statestore.Load: %v", err)
	}
	ws := st.FindWorkspace(session)
	if ws == nil {
		return ""
	}
	return ws.Tag
}
