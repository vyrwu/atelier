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
// guard for the respawned-workspace resume bug. A prior atelier run leaves the
// driver window carrying BOTH a one-shot @ai_prompt and a durable
// @ai_active_session_id, mirrored into the statestore cache. When OpenAgent
// launches Claude it consumes the prompt via clearLaunchPrompt — which must
// wipe the prompt from the live window AND the cache mirror. Otherwise the
// next tmux server restart's Restore re-stamps the spent prompt, and
// buildClaudeStartCmd forks a fresh session off it instead of resuming: the
// user sees Claude start over on the original prompt with the prior
// conversation orphaned.
func TestClearLaunchPrompt_SpentPromptDoesNotSurviveRespawn(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	root := filepath.Join(t.TempDir(), "workspace-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	const (
		session    = "vyrwu/atelier"
		driverName = "atelier" // driverWindowName(session)
		sessID     = "5f5670a4-d8d3-4917-9f73-48bdcdc2ac3d"
		prompt     = "Implement issue https://github.com/vyrwu/atelier/issues/18"
	)

	srv := testtmux.New(t)
	srv.NewSession(session)

	// Make the session atelier-managed (PersistWindowMetadata is a no-op on
	// sessions without @workspace_id) via the identity primitive, and name
	// window 1 the way the driver window is named.
	if err := workspace.StampWorkspaceIdentity(srv.Client, session, session, "", "", root); err != nil {
		t.Fatalf("stamp identity: %v", err)
	}
	if _, err := srv.Client.Run("rename-window", "-t", session+":1", driverName); err != nil {
		t.Fatalf("rename-window: %v", err)
	}
	widOut, err := srv.Client.Run("display-message", "-p", "-t", session+":"+driverName, "#{window_id}")
	if err != nil {
		t.Fatalf("resolve window id: %v", err)
	}
	wid := strings.TrimSpace(string(widOut))

	// The driver window as Restore leaves it post-respawn: prompt + session id
	// both stamped, and the same shape mirrored into the cache.
	for opt, val := range map[string]string{
		OptPrompt:          prompt,
		OptActiveSessionID: sessID,
	} {
		if err := srv.Client.SetWindowOption(wid, opt, val); err != nil {
			t.Fatalf("stamp %s: %v", opt, err)
		}
	}
	if err := statestore.Save(&statestore.State{
		Workspaces: []statestore.Workspace{{
			SessionName: session, Root: root, RepoPath: root,
			Windows: []statestore.Window{{
				Name: driverName, Cwd: root, Branch: driverName,
				Metadata: map[string]string{
					MetaPrompt:          prompt,
					MetaActiveSessionID: sessID,
				},
			}},
		}},
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// Act: OpenAgent consumes the one-shot prompt.
	clearLaunchPrompt(srv.Client, wid, prompt)

	// Live window: prompt gone; session id preserved (durable resume pointer).
	if got, _ := srv.Client.GetWindowOption(wid, OptPrompt); got != "" {
		t.Errorf("live @ai_prompt not cleared: %q", got)
	}
	if got, _ := srv.Client.GetWindowOption(wid, OptActiveSessionID); got != sessID {
		t.Errorf("live @ai_active_session_id must survive: got %q want %q", got, sessID)
	}

	// Cache mirror: prompt cleared; session id preserved.
	cached, err := statestore.Load()
	if err != nil || cached == nil {
		t.Fatalf("reload cache: %v (nil=%v)", err, cached == nil)
	}
	md := cached.Workspaces[0].Windows[0].Metadata
	if md[MetaPrompt] != "" {
		t.Errorf("cached ai.prompt not cleared: %q", md[MetaPrompt])
	}
	if md[MetaActiveSessionID] != sessID {
		t.Errorf("cached ai.active_session_id must survive: got %q want %q", md[MetaActiveSessionID], sessID)
	}

	// Full respawn: a fresh server + Restore must re-stamp the resumable
	// session id on the driver window — but NOT the spent prompt.
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
		if parts := strings.SplitN(line, "|", 2); len(parts) == 2 && parts[0] == driverName {
			wid2 = parts[1]
			break
		}
	}
	if wid2 == "" {
		t.Fatalf("restored driver window %q not found:\n%s", driverName, out)
	}
	if got, _ := srv2.Client.GetWindowOption(wid2, OptActiveSessionID); got != sessID {
		t.Errorf("restored @ai_active_session_id: got %q want %q", got, sessID)
	}
	if got, _ := srv2.Client.GetWindowOption(wid2, OptPrompt); got != "" {
		t.Errorf("restored window must NOT carry the spent prompt: %q", got)
	}
}
