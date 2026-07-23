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

	// Live window: prompt gone; kind (durable identity) + session id survive.
	if got, _ := srv.Client.GetWindowOption(wid, OptPrompt); got != "" {
		t.Errorf("live @ai_prompt not cleared: %q", got)
	}
	if got, _ := srv.Client.GetWindowOption(wid, OptWorkspaceKind); got != kind {
		t.Errorf("live @ai_workspace_kind must survive (picker identity): got %q want %q", got, kind)
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

	// Full respawn: a fresh server + Restore must re-stamp ONLY the resumable
	// session id — no prompt, no kind — so Claude resumes.
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
		t.Errorf("restored window must carry durable kind: got %q want %q", got, kind)
	}
}

// TestClearLaunchPrompt_PreservesMultiRepoKind is the regression guard for the
// "cross-repo (auto) workspace vanishes from M-s after Claude launches" bug.
// A multi-repo workspace has no @repo_path, so @ai_workspace_kind is the M-s
// picker's ONLY signal that the session is a workspace. clearLaunchPrompt used
// to consume it on launch, dropping the workspace out of the switcher.
func TestClearLaunchPrompt_PreservesMultiRepoKind(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const session = "auto/audit-observability"
	srv := testtmux.New(t)
	srv.NewSession(session)
	// Auto workspaces carry NO @repo_path — kind is the sole picker signal.
	if _, err := srv.Client.Run("set-option", "-w", "-t", session+":1",
		OptWorkspaceKind, WorkspaceKindMultiRepo); err != nil {
		t.Fatalf("stamp kind: %v", err)
	}
	widOut, err := srv.Client.Run("display-message", "-p", "-t", session+":1", "#{window_id}")
	if err != nil {
		t.Fatalf("resolve window id: %v", err)
	}
	wid := strings.TrimSpace(string(widOut))

	// Launch consumes the (empty) prompt; kind must survive.
	clearLaunchPrompt(srv.Client, wid, "")

	if got, _ := srv.Client.GetWindowOption(wid, OptWorkspaceKind); got != WorkspaceKindMultiRepo {
		t.Errorf("@ai_workspace_kind cleared — auto workspace would vanish from M-s: got %q want %q",
			got, WorkspaceKindMultiRepo)
	}
}
