//go:build e2e

package workspace_test

import (
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestSetTag_OptionAndCacheMirror locks in the workspace-tag primitive:
// SetTag writes @workspace_tag on the window (source of truth), mirrors
// it to the statestore cache under TagMetadataKey (so it survives a tmux
// restart), replaces on re-tag, and clears both on empty.
func TestSetTag_OptionAndCacheMirror(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := testtmux.New(t)
	srv.NewSession("vyrwu/demo")
	time.Sleep(150 * time.Millisecond)

	// Atelier-managed: stamp @repo_path so the cache mirror isn't skipped
	// (PersistWindowMetadata refuses to pollute non-atelier sessions).
	if _, err := srv.Client.Run("set-option", "-t", "vyrwu/demo", "@repo_path", "/tmp/demo"); err != nil {
		t.Fatalf("stamp repo_path: %v", err)
	}
	wid, err := srv.Client.DisplayMessageAt("vyrwu/demo", "#{window_id}")
	if err != nil || wid == "" {
		t.Fatalf("window id: %v", err)
	}
	wname, err := srv.Client.DisplayMessageAt("vyrwu/demo", "#{window_name}")
	if err != nil || wname == "" {
		t.Fatalf("window name: %v", err)
	}

	// Seed the cache workspace with RepoPath — matches reality (a workspace
	// is registered before it's tagged) and keeps the record past the
	// statestore's atelier-managed Save filter.
	workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
		Session:    "vyrwu/demo",
		RepoPath:   "/tmp/demo",
		Kind:       "worktree",
		WindowName: wname,
		Cwd:        "/tmp/demo",
		Branch:     wname,
	})

	if err := workspace.SetTag(srv.Client, wid, "client-x"); err != nil {
		t.Fatalf("SetTag: %v", err)
	}
	if got, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceTag); got != "client-x" {
		t.Errorf("@workspace_tag = %q, want client-x", got)
	}
	if got := cachedTag(t, "vyrwu/demo", wname); got != "client-x" {
		t.Errorf("cached workspace.tag = %q, want client-x", got)
	}

	// Re-tag replaces the previous value (one tag per window).
	if err := workspace.SetTag(srv.Client, wid, "infra"); err != nil {
		t.Fatalf("re-tag: %v", err)
	}
	if got, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceTag); got != "infra" {
		t.Errorf("re-tag @workspace_tag = %q, want infra", got)
	}

	// Empty clears the option.
	if err := workspace.SetTag(srv.Client, wid, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, _ := srv.Client.GetWindowOption(wid, workspace.OptWorkspaceTag); got != "" {
		t.Errorf("after clear @workspace_tag = %q, want empty", got)
	}
}

// cachedTag reads the persisted workspace.tag metadata for a window from
// the on-disk statestore.
func cachedTag(t *testing.T, session, window string) string {
	t.Helper()
	st, err := statestore.Load()
	if err != nil || st == nil {
		t.Fatalf("statestore.Load: %v", err)
	}
	for _, ws := range st.Workspaces {
		if ws.SessionName != session {
			continue
		}
		for _, w := range ws.Windows {
			if w.Name == window {
				return w.Metadata[workspace.TagMetadataKey]
			}
		}
	}
	return ""
}
