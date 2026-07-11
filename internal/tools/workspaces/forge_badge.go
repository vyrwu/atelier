package workspaces

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

// forgeRefreshTTL bounds how often the forge is queried per window; repeated
// picker opens within this window reuse the cached state.
const forgeRefreshTTL = 1 * time.Minute

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
// SpawnBgPull / spawnForgeRefresh discipline: the detached popup process
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

// renderForgeBadge returns the spliceable, ANSI-colored badge token (leading
// space) for a forge state. Each state gets its GitHub-octicon glyph (Nerd
// Font v3) + a state color: open=green, draft=grey, merged=purple,
// closed=red. Kernel-owned so every forge adapter renders identically. Pure.
func renderForgeBadge(state string) string {
	spec := map[integration.ForgeState]struct{ glyph, color string }{
		integration.ForgeOpen:   {"", "35"},  // pull request, green
		integration.ForgeDraft:  {"", "244"}, // draft, grey
		integration.ForgeMerged: {"", "141"}, // merge, purple
		integration.ForgeClosed: {"", "203"}, // closed, red
	}[integration.ForgeState(strings.TrimSpace(state))]
	if spec.color == "" {
		return ""
	}
	return " \033[38;5;" + spec.color + "m" + spec.glyph + "\033[0m"
}

// formatSessionDisplay assembles one session-picker row. The column ORDER is
// load-bearing and has silently regressed before (an unrelated PR moved the
// badge after the workspace name, and the test guarding it was deleted in the
// same commit — see #11 reverting #15). It lives as a pure function, tested at
// the rendered-string level, so a future reorder fails loudly regardless of
// how the badge itself is produced.
//
// Layout:  <time> <attention-icon> <forge-badge> <session>/<window> <recap>
//
// sessionColor is the SGR color body for the session name ("36" cyan for git
// workspaces, "38;5;166" orange for auto sessions); weight is "" or "1;".
func formatSessionDisplay(timeCol, icon, badgeCol, weight, sessionColor, session, window, recap string) string {
	return fmt.Sprintf("%s%s%s\033[%s%sm%s\033[0m/\033[%s32m%s\033[0m%s",
		timeCol, icon, badgeCol, weight, sessionColor, session, weight, window, recap)
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
	wt := filepath.Join(workspaceWorktreeRoot(), session, window)
	return wt, forgeDirExists(wt)
}

func forgeDirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// forgeBadgeColumn normalizes a rendered forge badge to a fixed 2-cell slot
// so the workspace-name column stays aligned across rows with and without a
// PR. Empty → two spaces; present → glyph + trailing space (renderForgeBadge
// emits a leading space, which we strip). The slot sits between the attention
// icon and the workspace name.
func forgeBadgeColumn(badge string) string {
	if strings.TrimSpace(badge) == "" {
		return "  "
	}
	return strings.TrimPrefix(badge, " ") + " "
}

// spawnForgeRefresh fires `atelier tools workspaces _forge-refresh` detached
// (own process group so it survives the popup pty closing), best-effort.
// Poked once per picker open when a forge integration is active; the
// per-window TTL keeps it from hammering the forge API.
func spawnForgeRefresh() {
	if !forgeActive() {
		return
	}
	// Skip in e2e test contexts (matches the pre-refactor badge behavior):
	// the detached `gh` query holds no value in tests and would spawn
	// network calls. Test sockets are named `atelier-test-*`.
	if strings.HasPrefix(os.Getenv("ATELIER_TMUX_SOCKET"), "atelier-test-") {
		return
	}
	self, err := os.Executable()
	if err != nil {
		self = "atelier"
	}
	cmd := exec.Command(self, "tools", "workspaces", "_forge-refresh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		debuglog.LogErr("workspaces.spawnForgeRefresh", err)
		return
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	debuglog.Logf("workspaces.spawnForgeRefresh: pid=%d", pid)
}

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
			return refreshForgeBadges(tmuxhost.New(socket), forge, time.Now())
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// OpenForgeCommand is the hidden `_open-forge <row>`: bound to M-o in the
// picker. Resolves the selected row's worktree and hands off to the active
// forge adapter's Open (e.g. `gh pr view --web`).
func OpenForgeCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_open-forge <row>",
		Short:  "internal: open the workspace's forge item (PR) in a browser (M-o)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			forge := integration.Active().Forge
			if forge == nil {
				return nil
			}
			return openForge(tmuxhost.New(socket), forge, strings.Join(args, " "))
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func refreshForgeBadges(h *tmuxhost.Client, forge integration.ForgeIntegration, now time.Time) error {
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
		if forgeFresh(now, tsStr) {
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

// forgeFresh reports whether an @forge_ts value is within forgeRefreshTTL of
// now. Empty/unparseable = stale. Pure.
func forgeFresh(now time.Time, tsStr string) bool {
	secs, err := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
	if err != nil || secs <= 0 {
		return false
	}
	return now.Sub(time.Unix(secs, 0)) < forgeRefreshTTL
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
