package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// softClosedMarkerName is the per-worktree marker the delete flow drops to
// mark a workspace closed-but-recoverable (mirrors workspaces.softClosedMarker).
const softClosedMarkerName = ".atelier-soft-closed"

// isSoftClosed reports whether the worktree at cwd was soft-closed (present in
// the cache for M-r recover, but should not be restored as a live window).
func isSoftClosed(cwd string) bool {
	if cwd == "" {
		return false
	}
	return pathExists(filepath.Join(cwd, softClosedMarkerName))
}

// Restore reads the persisted state cache and reproduces missing
// workspaces / windows / per-window options in tmux. Idempotent:
//   - Sessions that already exist are skipped (no clobber).
//   - Workspaces whose worktree directory is gone get skipped + warned.
//   - Globals (@atelier_k8s_active, @atelier_pgcli_active) are
//     unconditionally restored.
//
// Called by `atelier state restore`, which is itself invoked from the
// `atelier init` tmux config block on every tmux server startup. The
// idempotency means re-sourcing the config is safe.
//
// Best-effort throughout: a single bad workspace doesn't fail the rest
// of the restore. Errors go to the debug log so the user can diagnose
// without their tmux startup blowing up.
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
	} else {
		debuglog.Logf("workspace.Restore: cache has %d workspaces, last_active=%q",
			len(cached.Workspaces), cached.LastActiveSession)
		for _, ws := range cached.Workspaces {
			debuglog.Logf("workspace.Restore: cache entry session=%s kind=%s repo=%s created_at=%d windows=%d",
				ws.SessionName, ws.Kind, ws.RepoPath, ws.CreatedAt, len(ws.Windows))
			restoreOneWorkspace(h, ws)
		}
		for k, v := range cached.Globals {
			if err := h.SetGlobalOption(k, v); err != nil {
				debuglog.LogErr(fmt.Sprintf("workspace.Restore: SetGlobalOption %s", k), err)
			}
		}
		debuglog.Logf("workspace.Restore: done (%d workspaces, %d globals)",
			len(cached.Workspaces), len(cached.Globals))
	}
	// Warmup pass: covers the case where the cache was empty / stale
	// but tmux still has live sessions with @repo_path set (sessions
	// created before persistence shipped, or by paths that don't
	// register). Without this, vyrwu/atelier:main and friends keep
	// showing no freshness icon forever because bg-pull never ran.
	WarmupFreshness(h)
	return nil
}

// WarmupCandidate is one window that needs an initial bg-pull.
type WarmupCandidate struct {
	WindowID    string
	SessionName string
	RepoPath    string
}

// FindWarmupCandidates scans live tmux for windows that have @repo_path
// set on their session but no @workspace_freshness_ts or pull_error
// stamped — the "should have shown freshness but bg-pull never ran"
// state. Pure-ish discovery split from spawn for unit testability.
func FindWarmupCandidates(h *tmuxhost.Client) []WarmupCandidate {
	out, err := h.Run("list-windows", "-a", "-F",
		"#{window_id}|#{session_name}|#{@repo_path}|#{@workspace_freshness_ts}|#{@workspace_pull_error}")
	if err != nil {
		debuglog.LogErr("workspace.FindWarmupCandidates: list-windows", err)
		return nil
	}
	var found []WarmupCandidate
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}
		windowID, sessionName, repoPath, freshTs, pullErr := parts[0], parts[1], parts[2], parts[3], parts[4]
		if repoPath == "" {
			continue
		}
		if freshTs != "" || pullErr != "" {
			continue
		}
		found = append(found, WarmupCandidate{
			WindowID: windowID, SessionName: sessionName, RepoPath: repoPath,
		})
	}
	return found
}

// WarmupFreshness scans every live tmux window for ones whose session
// has @repo_path set but the window itself has no @workspace_freshness_ts
// (and no @workspace_pull_error). For each such window, fires a
// background pull so the freshness icon populates.
//
// Idempotent and cheap: a successful prior pull stamps freshness_ts,
// so the next warmup skips the window. The bg-pull itself does an
// up-to-date fetch in ~1-2s.
func WarmupFreshness(h *tmuxhost.Client) {
	candidates := FindWarmupCandidates(h)
	fired := 0
	for _, c := range candidates {
		defaultBranch, err := computeDefaultBranch(c.RepoPath)
		if err != nil || defaultBranch == "" {
			debuglog.Logf("workspace.WarmupFreshness: skip %s:%s — no default branch (repo=%s err=%v)",
				c.SessionName, c.WindowID, c.RepoPath, err)
			continue
		}
		debuglog.Logf("workspace.WarmupFreshness: firing bg-pull for %s window=%s repo=%s branch=%s",
			c.SessionName, c.WindowID, c.RepoPath, defaultBranch)
		SpawnBgPull(c.RepoPath, defaultBranch, c.WindowID)
		fired++
	}
	debuglog.Logf("workspace.WarmupFreshness: candidates=%d fired=%d", len(candidates), fired)
}

func restoreOneWorkspace(h *tmuxhost.Client, ws statestore.Workspace) {
	if ws.SessionName == "" || len(ws.Windows) == 0 {
		return
	}
	if has, _ := h.HasSession(ws.SessionName); has {
		debuglog.Logf("workspace.Restore: %s already present, skipping", ws.SessionName)
		return
	}
	// Restore only windows the user has OPEN: worktree present AND not
	// soft-closed. Soft-closed branches stay in the cache for M-r recover but
	// MUST NOT come back as live windows — resurrecting them bloats the
	// session with branches you already closed (and makes `exit` cycle through
	// all of them). A hard-removed worktree (cwd gone) is skipped likewise.
	isWorktree := ws.RepoPath != "" || ws.Kind == "worktree"
	var open []statestore.Window
	for _, w := range ws.Windows {
		switch {
		case isWorktree && w.Cwd == "":
			// Null-cwd worktree window: launcher-bare-create junk (a bare
			// "zsh" shell that leaked into the cache). It has no worktree
			// path to place it at, so restoring it would land a stray shell
			// in $HOME with the wrong context — the "zsh in M-s" bug. Unlike
			// a soft-closed or gone worktree (which stay cached for M-r
			// recover), this is unrecoverable, so PRUNE it from the cache so
			// an already-polluted cache heals on the next launch.
			debuglog.Logf("workspace.Restore: prune %s:%s — worktree window with no cwd (junk)", ws.SessionName, w.Name)
			if err := statestore.RemoveWindow(ws.SessionName, w.Name); err != nil {
				debuglog.LogErr(fmt.Sprintf("workspace.Restore: prune null-cwd %s:%s", ws.SessionName, w.Name), err)
			}
		case w.Cwd != "" && !pathExists(w.Cwd):
			debuglog.Logf("workspace.Restore: skip %s:%s — worktree gone (%q)", ws.SessionName, w.Name, w.Cwd)
		case isSoftClosed(w.Cwd):
			debuglog.Logf("workspace.Restore: skip %s:%s — soft-closed", ws.SessionName, w.Name)
		default:
			open = append(open, w)
		}
	}
	if len(open) == 0 {
		debuglog.Logf("workspace.Restore: %s has no open windows (all gone/soft-closed), skipping", ws.SessionName)
		return
	}
	debuglog.Logf("workspace.Restore: restoring %s (kind=%s repo=%s open=%d/%d)",
		ws.SessionName, ws.Kind, ws.RepoPath, len(open), len(ws.Windows))

	first := open[0]

	// Create session with the first open window in place.
	args := []string{"new-session", "-d", "-s", ws.SessionName}
	if first.Cwd != "" {
		args = append(args, "-c", first.Cwd)
	}
	if first.Name != "" {
		args = append(args, "-n", first.Name)
	}
	debuglog.Logf("workspace.Restore: new-session args=%v", args)
	if _, err := h.Run(args...); err != nil {
		debuglog.LogErr(fmt.Sprintf("workspace.Restore: new-session %s", ws.SessionName), err)
		return
	}

	if ws.RepoPath != "" {
		if _, err := h.Run("set-option", "-t", ws.SessionName, OptRepoPath, ws.RepoPath); err != nil {
			debuglog.LogErr("workspace.Restore: @repo_path", err)
		}
	}

	// Resolve the first window's @ID so we can stamp window-scoped options.
	winIDBytes, _ := h.DisplayMessageAt(ws.SessionName+":"+first.Name, "#{window_id}")
	winID := strings.TrimSpace(winIDBytes)

	// Restore the @workspace_created_ts timestamp so the picker's age
	// column shows actual workspace age across restarts, not empty.
	if ws.CreatedAt > 0 && winID != "" {
		if err := h.SetWindowOption(winID, OptWorkspaceCreatedTs,
			strconv.FormatInt(ws.CreatedAt, 10)); err != nil {
			debuglog.LogErr("workspace.Restore: @workspace_created_ts", err)
		}
	}

	applyWindowOptionsByName(h, ws.SessionName, first)
	scheduleBgPullForWindow(h, ws, first)

	for _, w := range open[1:] {
		args := []string{"new-window", "-d", "-t", ws.SessionName}
		if w.Cwd != "" {
			args = append(args, "-c", w.Cwd)
		}
		if w.Name != "" {
			args = append(args, "-n", w.Name)
		}
		if _, err := h.Run(args...); err != nil {
			debuglog.LogErr(fmt.Sprintf("workspace.Restore: new-window %s:%s",
				ws.SessionName, w.Name), err)
			continue
		}
		applyWindowOptionsByName(h, ws.SessionName, w)
		scheduleBgPullForWindow(h, ws, w)
	}
}

// scheduleBgPullForWindow fires the async pull for a freshly-restored
// window so the status-line freshness icon populates without the user
// having to M-s into the workspace first.
//
// Only fires for worktree-kind workspaces with @repo_path set — the
// _bg-pull subcommand requires a real repo path. Multi-repo
// workspaces are skipped (their "branch" is meaningless across
// multiple repos).
func scheduleBgPullForWindow(h *tmuxhost.Client, ws statestore.Workspace, w statestore.Window) {
	if ws.RepoPath == "" {
		return
	}
	// Resolve the freshly-assigned window @ID via tmux (the cache
	// keys on names, not ids; ids are reassigned every server
	// restart).
	wid, _ := h.DisplayMessageAt(ws.SessionName+":"+w.Name, "#{window_id}")
	wid = strings.TrimSpace(wid)
	if wid == "" {
		return
	}
	defaultBranch, _ := computeDefaultBranch(ws.RepoPath)
	if defaultBranch == "" {
		return
	}
	SpawnBgPull(ws.RepoPath, defaultBranch, wid)
}

// computeDefaultBranch returns the repo's default branch.
//
// Primary path: `git symbolic-ref --short refs/remotes/origin/HEAD`
// (returns `origin/main` for repos where the symref is set).
//
// Fallback: some clones never had origin/HEAD set locally (e.g. when
// cloned with `--no-tags` or with older git). For those we probe the
// common default branch names (main, master) via `git rev-parse` —
// network-free, just looks at refs/remotes/origin/<name>.
//
// Returns empty + error if nothing matched; callers (warmup, restore)
// log and skip rather than crash.
func computeDefaultBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if out, err := cmd.Output(); err == nil {
		ref := strings.TrimSpace(string(out))
		if i := strings.Index(ref, "/"); i >= 0 {
			return ref[i+1:], nil
		}
		return ref, nil
	}
	// Fallback 1: origin/<name> exists locally. Order matters: main
	// > master, since "main" has been git's default since 2020 and
	// matches most modern repos.
	for _, name := range []string{"main", "master"} {
		probe := exec.Command("git", "-C", repoPath, "rev-parse", "--verify",
			"--quiet", "refs/remotes/origin/"+name)
		if err := probe.Run(); err == nil {
			return name, nil
		}
	}
	// Fallback 2: no remote-tracking refs exist yet (cloned but
	// never fetched, or just `git init`+`remote add` without fetch).
	// Use the local HEAD's branch name — the first bg-pull's fetch
	// will then populate origin/<name> for next time.
	if out, err := exec.Command("git", "-C", repoPath, "symbolic-ref",
		"--short", "HEAD").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("computeDefaultBranch: no origin/HEAD, no origin/{main,master}, and no local HEAD in %s", repoPath)
}

// applyWindowOptionsByName stamps the persisted window options on the
// freshly-created window. Targets by session:window name rather than
// @id because the new tmux server assigns fresh ids — names are the
// only stable identity across restarts.
func applyWindowOptionsByName(h *tmuxhost.Client, session string, w statestore.Window) {
	target := session + ":" + w.Name
	if w.Attention {
		_, _ = h.Run("set-option", "-w", "-t", target, "@needs_attention", "1")
	}
	if w.Recap != "" {
		_, _ = h.Run("set-option", "-w", "-t", target, "@attention_recap", w.Recap)
	}
	if w.RecapTs != 0 {
		_, _ = h.Run("set-option", "-w", "-t", target, "@attention_recap_ts",
			strconv.FormatInt(w.RecapTs, 10))
	}
	// Re-stamp every plugin-namespaced metadata entry as the
	// corresponding tmux window option. Core never knows which
	// plugin owns which key — plugins read their state back via
	// `tmux show-options @<plugin>_<field>` after restore.
	for key, value := range w.Metadata {
		if value == "" {
			continue
		}
		_, _ = h.Run("set-option", "-w", "-t", target,
			statestore.MetadataKeyToOptionName(key), value)
	}
}

// SyncCache reconciles the on-disk cache against current tmux state,
// removing entries for sessions and windows that no longer exist.
// Idempotent. Invoked by `atelier state sync` from the session-closed
// / window-unlinked tmux hooks — the cache stops accumulating ghosts
// when the user kills tmux state outside atelier's mediation.
//
// This is a sweep rather than per-event surgery because tmux's hook
// format variables (#{hook_*}) vary by hook type and are unreliable
// across versions. One robust path is simpler than five fragile ones.
func SyncCache(h *tmuxhost.Client) error {
	cached, err := statestore.Load()
	if err != nil {
		return fmt.Errorf("workspace.SyncCache: load: %w", err)
	}
	if cached == nil {
		return nil
	}

	live, err := liveSessionsAndWindows(h)
	if err != nil {
		return fmt.Errorf("workspace.SyncCache: list tmux: %w", err)
	}

	for _, ws := range cached.Workspaces {
		if !live.hasSession(ws.SessionName) {
			if err := statestore.RemoveSession(ws.SessionName); err != nil {
				debuglog.LogErr(fmt.Sprintf("workspace.SyncCache: RemoveSession %s", ws.SessionName), err)
			}
			continue
		}
		for _, w := range ws.Windows {
			if !live.hasWindow(ws.SessionName, w.Name) {
				if err := statestore.RemoveWindow(ws.SessionName, w.Name); err != nil {
					debuglog.LogErr(fmt.Sprintf("workspace.SyncCache: RemoveWindow %s:%s",
						ws.SessionName, w.Name), err)
				}
			}
		}
	}
	return nil
}

// liveTmuxState snapshots current tmux sessions + windows for SyncCache
// to diff against. Captured in one pair of list-* calls to avoid race
// drift between checks.
type liveTmuxState struct {
	sessions map[string]bool
	windows  map[string]bool // key: "session|window"
}

func (l liveTmuxState) hasSession(s string) bool { return l.sessions[s] }
func (l liveTmuxState) hasWindow(s, w string) bool {
	return l.windows[s+"|"+w]
}

func liveSessionsAndWindows(h *tmuxhost.Client) (liveTmuxState, error) {
	live := liveTmuxState{
		sessions: map[string]bool{},
		windows:  map[string]bool{},
	}
	sessions, err := h.ListSessions()
	if err != nil {
		return live, err
	}
	for _, s := range sessions {
		live.sessions[s] = true
	}
	out, err := h.Run("list-windows", "-a", "-F", "#{session_name}|#{window_name}")
	if err != nil {
		return live, err
	}
	for _, line := range splitLines(string(out)) {
		live.windows[line] = true
	}
	return live, nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func pathExists(path string) bool {
	if path == "" {
		return true // empty cwd is allowed — tmux uses $HOME
	}
	_, err := os.Stat(path)
	return err == nil
}
