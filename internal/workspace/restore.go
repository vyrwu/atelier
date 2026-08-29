package workspace

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// Restore reads the persisted state cache and reproduces missing workspaces in
// tmux. Idempotent:
//   - Sessions that already exist are skipped (no clobber).
//   - The workspace's dedicated root + worktree symlink tree are re-materialized.
//   - Globals (@atelier_k8s_active, @atelier_pgcli_active) are restored.
//
// Called by `atelier state restore`, invoked from the `atelier init` tmux config
// block on every server startup. The idempotency means re-sourcing is safe.
//
// Best-effort throughout: a single bad workspace doesn't fail the rest.
func Restore(h *tmuxhost.Client) error {
	debuglog.Logf("workspace.Restore: BEGIN pid=%d tmux_env=%q atelier_sock=%q",
		os.Getpid(), os.Getenv("TMUX"), os.Getenv("ATELIER_TMUX_SOCKET"))
	cached, err := statestore.Load()
	if err != nil {
		debuglog.LogErr("workspace.Restore: load cache", err)
		return fmt.Errorf("workspace.Restore: load cache: %w", err)
	}
	if cached == nil {
		debuglog.Logf("workspace.Restore: no cache, nothing to restore")
		return nil
	}
	debuglog.Logf("workspace.Restore: cache has %d workspaces, last_active=%q",
		len(cached.Workspaces), cached.LastActiveSession)
	for _, ws := range cached.Workspaces {
		debuglog.Logf("workspace.Restore: cache entry session=%s title=%q root=%q worktrees=%d windows=%d",
			ws.SessionName, ws.Title, ws.Root, len(ws.Worktrees), len(ws.Windows))
		restoreOneWorkspace(h, ws)
	}
	for k, v := range cached.Globals {
		if err := h.SetGlobalOption(k, v); err != nil {
			debuglog.LogErr(fmt.Sprintf("workspace.Restore: SetGlobalOption %s", k), err)
		}
	}
	debuglog.Logf("workspace.Restore: done (%d workspaces, %d globals)",
		len(cached.Workspaces), len(cached.Globals))
	return nil
}

// restoreOneWorkspace recreates one workspace: its dedicated root, driver-agent
// window (at the root), workspace-identity options, and worktree symlink tree.
// Worktrees are artifacts, not windows, so restore does NOT recreate a window
// per worktree — it re-links them and opens the single driver.
func restoreOneWorkspace(h *tmuxhost.Client, ws statestore.Workspace) {
	if ws.SessionName == "" {
		return
	}
	if has, _ := h.HasSession(ws.SessionName); has {
		debuglog.Logf("workspace.Restore: %s already present, skipping", ws.SessionName)
		return
	}
	root := ws.Root
	if root == "" {
		// v2-migrated workspace: materialize the root on first use (WS-9).
		root = WorkspaceRootFor(ws.SessionName)
	}
	if err := EnsureWorkspaceRoot(root); err != nil {
		debuglog.LogErr("workspace.Restore: ensure root", err)
	}
	// Re-establish the worktree symlink tree under the root.
	for _, fix := range ReconcileLayout(root, ws.Worktrees) {
		debuglog.Logf("workspace.Restore: layout %s %s (%s)", fix.Code, fix.Subject, fix.Detail)
	}

	driverName := driverWindowName(ws)
	if _, err := h.Run("new-session", "-d", "-s", ws.SessionName, "-c", root, "-n", driverName); err != nil {
		debuglog.LogErr(fmt.Sprintf("workspace.Restore: new-session %s", ws.SessionName), err)
		return
	}
	// Stamp workspace identity on the session + mark the driver window.
	_ = StampWorkspaceIdentity(h, ws.SessionName, ws.SessionName, ws.Title, ws.Intent, root)
	if ws.RepoPath != "" {
		if _, err := h.Run("set-option", "-t", ws.SessionName, OptRepoPath, ws.RepoPath); err != nil {
			debuglog.LogErr("workspace.Restore: @repo_path", err)
		}
	}
	if ws.Tag != "" {
		if _, err := h.Run("set-option", "-t", ws.SessionName, OptWorkspaceTag, ws.Tag); err != nil {
			debuglog.LogErr("workspace.Restore: @workspace_tag", err)
		}
	}
	if wid, _ := h.DisplayMessageAt(ws.SessionName+":1", "#{window_id}"); strings.TrimSpace(wid) != "" {
		MarkDriver(h, strings.TrimSpace(wid))
	}
	// Apply the driver's persisted per-agent state (attention/recap/session-id).
	if primary := primaryWindow(ws.Windows); primary != nil {
		applyWindowOptionsByName(h, ws.SessionName, driverName, *primary, ws.CreatedAt)
	} else if ws.CreatedAt > 0 {
		_, _ = h.Run("set-option", "-w", "-t", ws.SessionName+":"+driverName,
			OptWorkspaceCreatedTs, strconv.FormatInt(ws.CreatedAt, 10))
	}
}

// driverWindowName derives a short tmux window name for the driver window from
// the workspace slug (the tmux #W shown in the status bar). Cosmetic — the
// picker renders @workspace_title, not this. Strips a migrated "auto/" prefix
// and keeps the last path segment.
func driverWindowName(ws statestore.Workspace) string {
	name := strings.TrimPrefix(ws.SessionName, "auto/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		name = "agent"
	}
	return name
}

// primaryWindow picks the window record whose per-agent state seeds the driver:
// the first flagged for attention (so a blocked workspace restores blocked),
// else the first window. Nil when there are no windows.
func primaryWindow(windows []statestore.Window) *statestore.Window {
	if len(windows) == 0 {
		return nil
	}
	for i := range windows {
		if windows[i].Attention {
			return &windows[i]
		}
	}
	return &windows[0]
}

// applyWindowOptionsByName stamps a restored window's persisted tmux options
// from its cached record, targeting session:name (the new server assigns fresh
// @ids). fallbackCreatedTs is the workspace-level CreatedAt used when the record
// has none of its own.
func applyWindowOptionsByName(h *tmuxhost.Client, session, name string, w statestore.Window, fallbackCreatedTs int64) {
	target := session + ":" + name
	if w.Attention {
		_, _ = h.Run("set-option", "-w", "-t", target, OptAttention, "1")
	}
	if w.CreatedAt > 0 {
		_, _ = h.Run("set-option", "-w", "-t", target, OptWorkspaceCreatedTs, strconv.FormatInt(w.CreatedAt, 10))
	} else if fallbackCreatedTs > 0 {
		_, _ = h.Run("set-option", "-w", "-t", target, OptWorkspaceCreatedTs, strconv.FormatInt(fallbackCreatedTs, 10))
	}
	if w.Recap != "" {
		_, _ = h.Run("set-option", "-w", "-t", target, OptRecap, w.Recap)
	}
	if w.RecapTs != 0 {
		_, _ = h.Run("set-option", "-w", "-t", target, OptRecapTs, strconv.FormatInt(w.RecapTs, 10))
	}
	// Re-stamp every plugin-namespaced metadata entry as its tmux window option.
	for key, value := range w.Metadata {
		if value == "" {
			continue
		}
		_, _ = h.Run("set-option", "-w", "-t", target, statestore.MetadataKeyToOptionName(key), value)
	}
}

// SyncCache reconciles the on-disk cache against current tmux state, removing
// entries for sessions that no longer exist. Idempotent. Invoked by `atelier
// state sync`.
func SyncCache(h *tmuxhost.Client) error {
	cached, err := statestore.Load()
	if err != nil {
		return fmt.Errorf("workspace.SyncCache: load: %w", err)
	}
	if cached == nil {
		return nil
	}
	live, err := liveSessions(h)
	if err != nil {
		return fmt.Errorf("workspace.SyncCache: list tmux: %w", err)
	}
	for _, ws := range cached.Workspaces {
		if !live[ws.SessionName] {
			if err := statestore.RemoveSession(ws.SessionName); err != nil {
				debuglog.LogErr(fmt.Sprintf("workspace.SyncCache: RemoveSession %s", ws.SessionName), err)
			}
		}
	}
	return nil
}

func liveSessions(h *tmuxhost.Client) (map[string]bool, error) {
	live := map[string]bool{}
	sessions, err := h.ListSessions()
	if err != nil {
		return live, err
	}
	for _, s := range sessions {
		live[s] = true
	}
	return live, nil
}

func pathExists(path string) bool {
	if path == "" {
		return true // empty cwd is allowed — tmux uses $HOME
	}
	_, err := os.Stat(path)
	return err == nil
}
