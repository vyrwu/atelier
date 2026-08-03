//go:build e2e

package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestClearLaunchPrompt_SpentPromptDoesNotSurviveRespawn is the regression
// guard for the respawned-workspace resume bug. A prior atelier run leaves a
// window carrying BOTH a one-shot @ai_prompt and a durable
// @ai_active_session_id, mirrored into the statestore cache. When OpenAgent
// launches Claude it consumes the prompt via clearLaunchPrompt — which must
// wipe the prompt from the live window AND the cache mirror. Otherwise the
// next tmux server restart's Restore re-stamps the spent prompt, and
// buildClaudeStartCmd forks a fresh session off it instead of resuming: the
// user sees Claude start over on the original prompt with the prior
// conversation orphaned.
func TestClearLaunchPrompt_SpentPromptDoesNotSurviveRespawn(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	wt := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	const (
		session = "vyrwu/atelier"
		window  = "feat/issue-18"
		sessID  = "5f5670a4-d8d3-4917-9f73-48bdcdc2ac3d"
		prompt  = "Implement issue https://github.com/vyrwu/atelier/issues/18"
		kind    = "worktree"
	)

	srv := testtmux.New(t)
	srv.NewSession(session)

	// Make the session atelier-managed (PersistWindowMetadata is a no-op on
	// unmanaged sessions) and give the window the stable name the cache keys on.
	if _, err := srv.Client.Run("set-option", "-t", session, "@repo_path", wt); err != nil {
		t.Fatalf("seed @repo_path: %v", err)
	}
	if _, err := srv.Client.Run("rename-window", "-t", session+":1", window); err != nil {
		t.Fatalf("rename-window: %v", err)
	}
	widOut, err := srv.Client.Run("display-message", "-p", "-t", session+":"+window, "#{window_id}")
	if err != nil {
		t.Fatalf("resolve window id: %v", err)
	}
	wid := strings.TrimSpace(string(widOut))

	// The window as Restore leaves it post-respawn: prompt + kind + session id
	// all stamped, and the same shape mirrored into the cache.
	for opt, val := range map[string]string{
		OptPrompt:          prompt,
		OptWorkspaceKind:   kind,
		OptActiveSessionID: sessID,
	} {
		if err := srv.Client.SetWindowOption(wid, opt, val); err != nil {
			t.Fatalf("stamp %s: %v", opt, err)
		}
	}
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: session, RepoPath: wt, Kind: "worktree",
			Windows: []statestore.Window{{
				Name: window, Cwd: wt, Branch: window,
				Metadata: map[string]string{
					MetaPrompt:          prompt,
					MetaWorkspaceKind:   kind,
					MetaActiveSessionID: sessID,
				},
			}},
		}},
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// Act: OpenAgent consumes the one-shot prompt.
	clearLaunchPrompt(srv.Client, wid, prompt)

	// Live window: prompt gone; kind + session id preserved (kind is durable —
	// it's the picker's workspace signal, not a one-shot launch input).
	if got, _ := srv.Client.GetWindowOption(wid, OptPrompt); got != "" {
		t.Errorf("live @ai_prompt not cleared: %q", got)
	}
	if got, _ := srv.Client.GetWindowOption(wid, OptWorkspaceKind); got != kind {
		t.Errorf("live @ai_workspace_kind must survive: got %q want %q", got, kind)
	}
	if got, _ := srv.Client.GetWindowOption(wid, OptActiveSessionID); got != sessID {
		t.Errorf("live @ai_active_session_id must survive: got %q want %q", got, sessID)
	}

	// Cache mirror: prompt cleared; kind + session id preserved.
	cached, err := statestore.Load()
	if err != nil || cached == nil {
		t.Fatalf("reload cache: %v (nil=%v)", err, cached == nil)
	}
	md := cached.Workspaces[0].Windows[0].Metadata
	if md[MetaPrompt] != "" {
		t.Errorf("cached ai.prompt not cleared: %q", md[MetaPrompt])
	}
	if md[MetaWorkspaceKind] != kind {
		t.Errorf("cached ai.workspace_kind must survive: got %q want %q", md[MetaWorkspaceKind], kind)
	}
	if md[MetaActiveSessionID] != sessID {
		t.Errorf("cached ai.active_session_id must survive: got %q want %q", md[MetaActiveSessionID], sessID)
	}

	// Full respawn: a fresh server + Restore must re-stamp the resumable
	// session id AND the durable kind — but NOT the spent prompt.
	srv.Kill()
	srv2 := testtmux.New(t)
	if err := workspace.Restore(srv2.Client); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	out, err := srv2.Client.Run("list-windows", "-t", "="+session, "-F", "#{window_name}|#{window_id}")
	if err != nil {
		t.Fatalf("list restored windows: %v", err)
	}
	var wid2 string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if parts := strings.SplitN(line, "|", 2); len(parts) == 2 && parts[0] == window {
			wid2 = parts[1]
			break
		}
	}
	if wid2 == "" {
		t.Fatalf("restored window %q not found:\n%s", window, out)
	}
	if got, _ := srv2.Client.GetWindowOption(wid2, OptActiveSessionID); got != sessID {
		t.Errorf("restored @ai_active_session_id: got %q want %q", got, sessID)
	}
	if got, _ := srv2.Client.GetWindowOption(wid2, OptPrompt); got != "" {
		t.Errorf("restored window must NOT carry the spent prompt: %q", got)
	}
	if got, _ := srv2.Client.GetWindowOption(wid2, OptWorkspaceKind); got != kind {
		t.Errorf("restored window must carry the durable kind: got %q want %q", got, kind)
	}
}

// TestClearLaunchPrompt_MultiRepoStaysListable is the regression guard for
// the phantom-notification bug: a repo-less multi-repo workspace (no
// @repo_path) is listable in the M-s picker ONLY via @ai_workspace_kind.
// clearLaunchPrompt used to strip that option on every Claude launch, so the
// workspace vanished from the picker while its later Stop-hook @needs_attention
// flag still inflated the ⏺ rollup — a notification with no workspace behind
// it. After launch the kind must survive and the window must stay Listable.
func TestClearLaunchPrompt_MultiRepoStaysListable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const (
		session = "auto/rotate-rails-secrets"
		prompt  = "Rotate SECRET_KEY_BASE and RAILS_MASTER_KEY with zero downtime."
	)

	srv := testtmux.New(t)
	srv.NewSession(session)

	widOut, err := srv.Client.Run("display-message", "-p", "-t", session+":1", "#{window_id}")
	if err != nil {
		t.Fatalf("resolve window id: %v", err)
	}
	wid := strings.TrimSpace(string(widOut))

	// A repo-less multi-repo workspace: NO @repo_path, kind is its only
	// listable signal.
	if err := srv.Client.SetWindowOption(wid, OptPrompt, prompt); err != nil {
		t.Fatalf("stamp @ai_prompt: %v", err)
	}
	if err := srv.Client.SetWindowOption(wid, OptWorkspaceKind, WorkspaceKindMultiRepo); err != nil {
		t.Fatalf("stamp @ai_workspace_kind: %v", err)
	}

	// Precondition: listable before launch.
	if !workspace.Listable("", WorkspaceKindMultiRepo) {
		t.Fatal("multi-repo workspace must be listable before launch")
	}

	clearLaunchPrompt(srv.Client, wid, prompt)

	if got, _ := srv.Client.GetWindowOption(wid, OptPrompt); got != "" {
		t.Errorf("live @ai_prompt not cleared: %q", got)
	}
	kind, _ := srv.Client.GetWindowOption(wid, OptWorkspaceKind)
	if kind != WorkspaceKindMultiRepo {
		t.Fatalf("live @ai_workspace_kind must survive launch: got %q want %q", kind, WorkspaceKindMultiRepo)
	}
	repoPath, _ := srv.Client.GetWindowOption(wid, workspace.OptRepoPath)
	if !workspace.Listable(repoPath, kind) {
		t.Errorf("multi-repo workspace vanished from the picker after launch (repo=%q kind=%q)", repoPath, kind)
	}
}
