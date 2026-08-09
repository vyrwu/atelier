package workspaces

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// The forge badge is a KERNEL-owned capability slot in the workspace
// picker: a per-workspace code-forge status glyph (PR open/draft/merged/
// closed) rendered right after the workspace name, plus M-o to open it. The
// kernel owns the glyph, color, sort order, window-option caching, and
// refresh cadence; the active integration.ForgeIntegration adapter (GitHub,
// GitLab, …) only *classifies* the state. When no forge adapter is
// configured, the slot is simply absent — graceful degradation.
const (
	// OptForgeState caches the classified ForgeState per window so the
	// picker renders instantly without re-querying the forge on every open.
	OptForgeState = "@forge_state"
	// OptForgeTs is the unix-epoch of the last refresh, for this slot's own
	// staleness throttle.
	OptForgeTs = "@forge_ts"
)

// forgeRefreshTTL bounds how often the forge is queried per window on the
// EVENT-driven path (picker-open, navigate); repeated opens within this window
// reuse the cached state.
const forgeRefreshTTL = 1 * time.Minute

// forgeLoopRefreshTTL is the TTL the CONTINUOUS background loop uses. Set to 1
// minute so a PR going draft/ready/merged shows on the badge within ~a minute.
// The `gh` cost is modest at real workspace counts (~N calls/min) and well under
// GitHub's REST budget, so freshness wins here over polling frugality.
const forgeLoopRefreshTTL = 1 * time.Minute

// forgeStateOrder is the kernel's picker sort order for forge states: open
// first, then draft, merged, closed. Windows with no forge item sort last.
var forgeStateOrder = []integration.ForgeState{
	integration.ForgeOpen, integration.ForgeDraft,
	integration.ForgeMerged, integration.ForgeClosed,
}

// forgeActive reports whether a forge integration is installed.
func forgeActive() bool { return integration.Active().Forge != nil }

// agentAutoOpenSkipped reports whether the deferred agent auto-open should be
// skipped — true in e2e test contexts (atelier-test-* sockets), matching the
// SpawnBgPull / SpawnForgeRefresh discipline: the detached popup process
// races t.TempDir cleanup, and tests assert landing/state, not the popup.
func agentAutoOpenSkipped() bool {
	return strings.HasPrefix(os.Getenv("ATELIER_TMUX_SOCKET"), "atelier-test-")
}

// forgeStateRank returns the picker sort rank of a forge state (lower =
// earlier). Unknown/none states sort last.
func forgeStateRank(state string) int {
	for i, s := range forgeStateOrder {
		if string(s) == strings.TrimSpace(state) {
			return i
		}
	}
	return len(forgeStateOrder)
}

// forgeWorkspaceCwd returns the CANONICAL directory for a workspace window —
// the branch's worktree, or the repo root for the default-branch window.
// Forge lookups MUST use this, never #{pane_current_path}: a bare or cd'd-away
// window's live pane path points at an unrelated repo, which surfaces (and
// opens) the WRONG PR. Returns ("", false) when the canonical dir is absent
// (e.g. a bare "zsh" window with no matching worktree) → no badge.
func forgeWorkspaceCwd(session, window, repoPath string) (string, bool) {
	if repoPath != "" && window == DefaultBranch(repoPath) {
		return repoPath, forgeDirExists(repoPath)
	}
	// The worktree dir is <root>/<owner>/<repo>/<branch> with the repo's real
	// name — which may contain '.' (e.g. cloudnativedenmark.dk). The tmux
	// session name has that '.' mangled to '_', so build the path from
	// @repo_path (dot intact) and fall back to the session only when no repo
	// path is stamped (multi-repo/auto sessions, whose names are dot-free).
	slug := repoSlugFromPath(repoPath)
	if slug == "" {
		slug = session
	}
	wt := filepath.Join(workspaceWorktreeRoot(), slug, window)
	return wt, forgeDirExists(wt)
}

// repoSlugFromPath recovers the user-facing "owner/repo" from a workspace's
// @repo_path (its last two path segments), preserving characters like '.'
// that tmux strips from session names. Returns "" when the path is too
// shallow to yield both segments.
func repoSlugFromPath(repoPath string) string {
	if repoPath == "" {
		return ""
	}
	repo := filepath.Base(repoPath)
	owner := filepath.Base(filepath.Dir(repoPath))
	if repo == "" || repo == "." || repo == string(filepath.Separator) ||
		owner == "" || owner == "." || owner == string(filepath.Separator) {
		return ""
	}
	return owner + "/" + repo
}

func forgeDirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// The detached spawn recipe lives in the workspace primitive as
// workspace.SpawnForgeRefresh — fired both here (picker open) and at every
// workspace-land event so the status-line forge badge stays current.

// ForgeRefreshCommand is the hidden `_forge-refresh`: enumerate repo
// windows, throttle per window via @forge_ts, ask the active forge adapter
// to classify each, and cache the result in @forge_state.
func ForgeRefreshCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_forge-refresh",
		Short:  "internal: refresh per-workspace forge (PR) badges",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			forge := integration.Active().Forge
			if forge == nil {
				return nil
			}
			return refreshForgeBadges(tmuxhost.New(socket), forge, time.Now(), forgeRefreshTTL)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func refreshForgeBadges(h *tmuxhost.Client, forge integration.ForgeIntegration, now time.Time, ttl time.Duration) error {
	out, err := h.Run("list-windows", "-a", "-F",
		"#{window_id}|#{@repo_path}|#{session_name}|#{window_name}|#{"+OptForgeTs+"}")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "|", 5)
		if len(fields) < 5 {
			continue
		}
		windowID, repoPath, session, window, tsStr := fields[0], fields[1], fields[2], fields[3], fields[4]
		if repoPath == "" {
			continue // non-git workspace
		}
		// Resolve the PR from the workspace's CANONICAL worktree, never the
		// live pane cwd — a bare/cd'd-away window's pane path points at an
		// unrelated repo and would surface the WRONG PR. A missing canonical
		// dir (bare "zsh" window) clears any stale badge and shows none.
		cwd, ok := forgeWorkspaceCwd(session, window, repoPath)
		if !ok {
			stampForge(h, windowID, OptForgeState, "forge.state", "")
			continue
		}
		if forgeFresh(now, tsStr, ttl) {
			continue
		}
		// Stamp the timestamp regardless so a branch with no forge item isn't
		// retried on every picker open.
		stampForge(h, windowID, OptForgeTs, "forge.ts", strconv.FormatInt(now.Unix(), 10))
		st, err := forge.Status(integration.WorkspaceRef{WindowID: windowID, Cwd: cwd, RepoPath: repoPath})
		if err != nil || st.State == integration.ForgeNone {
			stampForge(h, windowID, OptForgeState, "forge.state", "")
			continue
		}
		stampForge(h, windowID, OptForgeState, "forge.state", string(st.State))
		debuglog.Logf("workspaces._forge-refresh: window=%s (%s/%s) cwd=%s state=%s",
			windowID, session, window, cwd, st.State)
	}
	return nil
}

func openForge(h *tmuxhost.Client, forge integration.ForgeIntegration, row string) error {
	session, window, ok := parseForgeRow(row)
	if !ok {
		return nil
	}
	// Resolve the PR from the workspace's CANONICAL worktree (repo+branch),
	// not the live pane cwd — otherwise a bare/cd'd-away window opens the
	// WRONG workspace's PR.
	repoPath, _ := getSessionRepoPath(h, session)
	cwd, ok := forgeWorkspaceCwd(session, window, repoPath)
	if !ok {
		debuglog.Logf("workspaces.openForge: no canonical worktree for %s/%s — skipping", session, window)
		return nil
	}
	if err := forge.Open(integration.WorkspaceRef{Cwd: cwd}); err != nil {
		debuglog.LogErr("workspaces.openForge", err)
	}
	return nil
}

// stampForge sets (or unsets, when empty) a window option and mirrors it
// into the statestore so the cached forge state survives a tmux restart
// (restore re-stamps persisted metadata generically). Best-effort.
func stampForge(h *tmuxhost.Client, windowID, opt, metaKey, value string) {
	if value == "" {
		_ = h.UnsetWindowOption(windowID, opt)
	} else {
		_ = h.SetWindowOption(windowID, opt, value)
	}
	_ = workspace.PersistWindowMetadata(h, windowID, metaKey, value)
}

// forgeFresh reports whether an @forge_ts value is within ttl of now.
// Empty/unparseable = stale. Pure — the caller supplies the TTL so the
// event-driven path and the background loop can throttle at different cadences.
func forgeFresh(now time.Time, tsStr string, ttl time.Duration) bool {
	secs, err := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
	if err != nil || secs <= 0 {
		return false
	}
	return now.Sub(time.Unix(secs, 0)) < ttl
}

// parseForgeRow splits a picker row ("<session>\t<window>\t<display>") into
// session + window names. Pure.
func parseForgeRow(row string) (session, window string, ok bool) {
	fields := strings.SplitN(row, "\t", 3)
	if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
		return "", "", false
	}
	return fields[0], fields[1], true
}
