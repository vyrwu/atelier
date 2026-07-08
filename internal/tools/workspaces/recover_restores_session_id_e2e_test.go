//go:build e2e

package workspaces

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestRecover_RestoresSessionIDForResume locks in the fix for the M-r
// recover path dropping the Claude resume id. Before the fix,
// openWorktreeWorkspace created a bare window with no
// @ai_active_session_id, so spawnClaudeResume found no id and Claude
// started fresh instead of `--resume`ing the prior conversation.
//
// The id lives in the statestore cache under the window's
// Metadata["ai.active_session_id"] (mirrored there when the session was
// first stamped). Recover must read it back and re-stamp it on the new
// window — exactly what workspace.Restore does on server startup.
func TestRecover_RestoresSessionIDForResume(t *testing.T) {
	srv := testtmux.New(t)
	tmp := t.TempDir()
	repo := testtmux.TestRepo(t, tmp, "vyrwu", "demo", "main")

	t.Setenv("ATELIER_CODE_ROOT", testtmux.CodeRoot(tmp))
	worktreeRoot := filepath.Join(tmp, "worktrees")
	t.Setenv("ATELIER_WORKTREE_ROOT", worktreeRoot)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "cache"))

	const branch = "featwork"
	wtPath := filepath.Join(worktreeRoot, "vyrwu", "demo", branch)
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-b", branch, wtPath).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	// Seed the cache as if a prior Claude session had been stamped on
	// this worktree before it was soft-closed.
	const wantID = "uuid-resume-42"
	if err := statestore.UpdateWorkspace("vyrwu/demo", func(ws *statestore.Workspace) {
		ws.RepoPath = repo
		ws.Kind = "worktree"
	}); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := statestore.UpdateWindow("vyrwu/demo", branch, func(w *statestore.Window) {
		w.Cwd = wtPath
		w.Branch = branch
		w.Metadata = map[string]string{"ai.active_session_id": wantID}
	}); err != nil {
		t.Fatalf("seed window: %v", err)
	}

	// Recover it. LandOuter may error in a detached test server (no
	// outer client) — the metadata stamping happens before that, so
	// ignore the return and assert on tmux state.
	_ = openWorktreeWorkspace(srv.Client, "vyrwu/demo", branch)
	time.Sleep(100 * time.Millisecond)

	out, _ := srv.Client.Run("list-windows", "-t", "=vyrwu/demo", "-F", "#{window_id}\t#W")
	var wid string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && parts[1] == branch {
			wid = parts[0]
		}
	}
	if wid == "" {
		t.Fatalf("recovered window %q not found; list-windows=%q", branch, out)
	}

	got, _ := srv.Client.GetWindowOption(wid, statestore.MetadataKeyToOptionName("ai.active_session_id"))
	if got != wantID {
		t.Errorf("@ai_active_session_id on recovered window = %q, want %q (resume id was dropped)", got, wantID)
	}
}
