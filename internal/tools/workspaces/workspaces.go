// Package workspaces is the atelier workspaces tool: the M-s sessions
// picker, M-n creator (repo + multi-repo AI flows), M-r recover, clone,
// tagging, and the background refresh loop.
//
// Every picker/prompt/loader renders via the shared bubbletea substrate in
// internal/tui (see sessions_tui.go, recover_tui.go) — no fzf.
package workspaces

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/dispatch"
	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/initgen"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/perf"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/tui"
	"github.com/vyrwu/atelier/internal/workspace"
)

// ============================================================================
// SessionsCommand — port of tmux_session_picker
// ============================================================================

func SessionsCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "sessions",
		Short: "Pick an existing workspace session (bubbletea picker)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)

			// Kernel forge-badge slot: poke a refresh once now (fire-and-
			// forget, TTL-throttled) so the PR badge is current on the next
			// live tick. Absent adapter → no-op.
			workspace.SpawnForgeRefresh()

			// Sticky scope (M-p): a pinned query pre-seeds the picker filter
			// so a focused context survives across picker opens.
			pin := workspace.GetScopePin(h)

			// Picker loop. The native bubbletea picker's own tea.Tick diffs
			// rows in place (see sessions_tui.go), so attention/recap changes
			// appear live without the old fzf --listen HTTP push. Cross-jumps
			// and M-q come back as outcome keys; M-t runs a nested tag prompt
			// and reopens; everything else resolves to a picked workspace.
			var row SessionRow
			for {
				var rows []SessionRow
				// tui.Loader renders a spinner only if the list build is slow;
				// fast builds show nothing (delay-gated).
				if err := tui.Loader("Loading workspaces…", func(func(string)) error {
					var e error
					rows, e = BuildSessionList(h)
					return e
				}); err != nil {
					return err
				}
				outcome, runErr := tui.Run(newSessionsModel(h, rows, pin))
				if runErr != nil {
					if errors.Is(runErr, tui.ErrCancelled) {
						debuglog.Logf("workspaces.sessions: cancelled")
					}
					return runErr
				}
				switch outcome.Key {
				case "workspaces/pick":
					return tui.ExecReplace("tools", "workspaces", "pick")
				case "workspaces/recover":
					return tui.ExecReplace("tools", "workspaces", "recover")
				case "toolselector/select":
					return tui.ExecReplace("tools", "toolselector", "select")
				case "workspaces/clone":
					return tui.ExecReplace("tools", "workspaces", "clone")
				case "quit":
					return nil
				case "tag":
					s, wnd, _ := strings.Cut(outcome.Selection, "\x00")
					if err := runTagPrompt(h, s, wnd); err != nil && !errors.Is(err, tui.ErrCancelled) {
						return err
					}
					continue // reopen the picker so the new tag pill renders
				}
				if outcome.Selection == "" {
					debuglog.Logf("workspaces.sessions: empty pick — propagating cancel")
					return tui.ErrCancelled
				}
				pickedSession, pickedWindow, ok := strings.Cut(outcome.Selection, "\x00")
				if !ok {
					return fmt.Errorf("could not parse picked entry: %q", outcome.Selection)
				}
				debuglog.Logf("workspaces.sessions: picked %s/%s", pickedSession, pickedWindow)
				row = SessionRow{Session: pickedSession, Window: pickedWindow}
				break
			}

			// Same UX as bash:
			//  - on default branch of a repo session → run pull-default
			//  - if a claude popup backs the target window, defer-spawn it
			// Async pull (FR-7.1): capture repo info here, spawn the
			// `_bg-pull` subcommand AFTER LandOuter so the user is on
			// the workspace before any git work begins.
			bgRepoPath, _ := getSessionRepoPath(h, row.Session)
			var bgDefaultBranch string
			if bgRepoPath != "" {
				bgDefaultBranch = DefaultBranch(bgRepoPath)
			}

			// Deferred Claude popup open. Two trigger conditions:
			//
			//  1. Backing popup session ALREADY exists (claude was
			//     running, user is returning) → reopen the popup so it
			//     attaches to the live session.
			//
			//  2. Backing popup session does NOT exist BUT the window
			//     has @ai_active_session_id stamped (from a prior
			//     atelier run, persisted via statestore) → spawn a
			//     fresh popup which the AI adapter (`atelier ai open`) launches with
			//     `--resume <id>`. This is the FR-5.2 auto-resume on
			//     workspace entry: tmux died, atelier restored the
			//     workspace, user M-s'es back in, Claude picks up where
			//     it left off.
			targetSid, _ := h.DisplayMessageAt(row.Session, "#{session_id}")
			targetWid, _ := h.DisplayMessageAt(row.Session+":"+row.Window, "#{window_id}")
			if targetSid != "" && targetWid != "" {
				// Deferred agent open on switch: (re)open the agent popup
				// only if the active AI integration has a live popup or a
				// tracked session for this window. Kernel asks the adapter;
				// no AI configured → never auto-open. Skipped in e2e.
				ai := integration.Active().AI
				shouldSpawn := false
				if ai != nil && !agentAutoOpenSkipped() {
					hasPopup, _ := h.HasSession(ai.AgentPopupSession(targetSid, targetWid))
					shouldSpawn = hasPopup || ai.HasResumableState(h, targetWid, "")
				}
				if shouldSpawn {
					sidNum := strings.TrimPrefix(targetSid, "$")
					widNum := strings.TrimPrefix(targetWid, "@")
					// Use the canonical atelier popup style — same
					// geometry/border/accent as every other "full"
					// popup. Change those at initgen.PopupOptions
					// and this picks it up.
					popupOpts := initgen.PopupOptions(manifest.StyleFull, "Claude Code", false)
					popupCmd := fmt.Sprintf(
						`sleep 0.2 && tmux display-popup %s -e TMUX_PARENT_SESSION_ID=%s -e TMUX_PARENT_WINDOW_ID=%s -E '%s'`,
						popupOpts, sidNum, widNum,
						dispatch.CoreCmd("ai", "open"))
					_, _ = h.Run("run-shell", "-b", popupCmd)
				}
			}

			if err := workspace.LandOuter(h, "="+row.Session, "="+row.Session+":"+row.Window); err != nil {
				return err
			}
			// Spawn _bg-pull AFTER landing the user. Detached;
			// returns immediately. Failure (or no repo) is silent.
			if bgRepoPath != "" && bgDefaultBranch != "" && targetWid != "" {
				workspace.SpawnBgPull(bgRepoPath, bgDefaultBranch, targetWid)
				workspace.SpawnForgeRefresh()
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// ============================================================================
// PickCommand — port of tmux_workspace_picker (Step 1)
// ============================================================================

func PickCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "pick",
		Short: "Pick or create a workspace (bash-exact tmux_workspace_picker)",
		RunE: func(_ *cobra.Command, _ []string) error {
			base := workspaceCodeRoot()
			if _, err := os.Stat(base); err != nil {
				return fmt.Errorf("no %s", base)
			}

			repos := []string{}
			err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				rel, _ := filepath.Rel(base, p)
				depth := strings.Count(rel, string(os.PathSeparator)) + 1
				if rel == "." {
					return nil
				}
				if !d.IsDir() {
					return nil
				}
				if depth == 2 {
					repos = append(repos, rel)
					return filepath.SkipDir
				}
				if depth >= 2 {
					return filepath.SkipDir
				}
				return nil
			})
			if err != nil {
				return err
			}
			sort.Strings(repos)

			_ = socket
			items := make([]list.Item, 0, len(repos))
			for _, r := range repos {
				items = append(items, tui.SimpleItem{IDStr: r, TitleStr: r, DescStr: "start a worktree here", Filter: r})
			}

			// M-n has two ways to start a workspace, toggled with M-m: pick an
			// existing repo (the list) or type a multi-repo task prompt (an
			// AI-named session). Cross-jumps (M-s/M-r/M-;/M-u) hop to siblings.
			for {
				outcome, err := tui.Run(tui.NewList(tui.CreatorTheme(), " New Workspace ", items,
					tui.Action("mode:auto", "alt+m", "M-m", "multi-repo"),
					tui.Action("workspaces/sessions", "alt+s", "M-s", "sessions"),
					tui.Action("workspaces/recover", "alt+r", "M-r", "recover"),
					tui.Action("toolselector/select", "alt+;", "M-;", "tools"),
					tui.Action("workspaces/clone", "alt+u", "M-u", "clone")))
				if err != nil {
					return err
				}
				switch outcome.Key {
				case "workspaces/sessions":
					return tui.ExecReplace("tools", "workspaces", "sessions")
				case "workspaces/recover":
					return tui.ExecReplace("tools", "workspaces", "recover")
				case "toolselector/select":
					return tui.ExecReplace("tools", "toolselector", "select")
				case "workspaces/clone":
					return tui.ExecReplace("tools", "workspaces", "clone")
				case "mode:auto":
					toggledBack, err := runMultiRepoPrompt()
					if toggledBack {
						continue // M-m again → back to the repo list
					}
					return err
				}
				if outcome.Selection == "" {
					debuglog.Logf("workspaces.pick: empty pick — propagating cancel")
					return tui.ErrCancelled
				}
				repo := outcome.Selection
				repoPath := filepath.Join(base, repo)
				defaultBranch := DefaultBranch(repoPath)
				debuglog.Logf("workspaces.pick: picked repo=%q → prompt flow", repo)
				return runWorkspacePrompt(repo, repoPath, defaultBranch, "")
			}
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// ============================================================================
// CloneCommand — port of tmux_clone_workspace
// ============================================================================

var cloneURLRe = regexp.MustCompile(`^(https://github\.com/|git@github\.com:)([^/]+)/([^/[:space:]]+)/?$`)

func CloneCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "clone",
		Short: "Prompt for a GitHub URL and clone (bash-exact tmux_clone_workspace)",
		RunE: func(_ *cobra.Command, _ []string) error {
			base := workspaceCodeRoot()
			query := ""
			errMsg := ""
			for {
				header := "paste a GitHub URL → clone + open default branch"
				if errMsg != "" {
					header = errMsg
				}
				outcome, err := tui.Run(tui.NewPrompt(tui.CloneTheme(), tui.PromptConfig{
					Title:       " Clone Repo ",
					Glyph:       "複 ",
					Placeholder: "https://github.com/owner/repo",
					Initial:     query,
					Header:      header,
					HeaderError: errMsg != "",
					Actions: []tui.KeyAction{
						tui.Action("workspaces/sessions", "alt+s", "M-s", "sessions"),
						tui.Action("workspaces/pick", "alt+n", "M-n", "new"),
						tui.Action("workspaces/recover", "alt+r", "M-r", "recover"),
						tui.Action("toolselector/select", "alt+;", "M-;", "tools"),
					},
				}))
				if err != nil {
					return err
				}
				switch outcome.Key {
				case "workspaces/sessions":
					return tui.ExecReplace("tools", "workspaces", "sessions")
				case "workspaces/pick":
					return tui.ExecReplace("tools", "workspaces", "pick")
				case "workspaces/recover":
					return tui.ExecReplace("tools", "workspaces", "recover")
				case "toolselector/select":
					return tui.ExecReplace("tools", "toolselector", "select")
				}
				url := strings.TrimSpace(outcome.Query)
				if url == "" {
					errMsg = "✗ enter a GitHub URL"
					continue
				}
				m := cloneURLRe.FindStringSubmatch(url)
				if m == nil {
					errMsg = "✗ unrecognized URL — expected https://github.com/o/r or git@github.com:o/r"
					query = url
					continue
				}
				owner := m[2]
				repo := strings.TrimSuffix(strings.TrimSuffix(m[3], "/"), ".git")
				target := filepath.Join(base, owner, repo)
				// Path keeps the raw name; the tmux session identity must be
				// normalized ('.'/':' → '_') to match what tmux actually stores.
				session := workspace.SessionName(owner + "/" + repo)

				if _, err := os.Stat(target); err != nil {
					_ = os.MkdirAll(filepath.Dir(target), 0o755)
					cloneErr := tui.Loader(fmt.Sprintf("Cloning %s/%s…", owner, repo), func(func(string)) error {
						return runGit("", "clone", url, target)
					})
					if cloneErr != nil {
						errMsg = fmt.Sprintf("✗ clone failed for %s/%s", owner, repo)
						query = url
						continue
					}
				}

				defaultBranch := DefaultBranch(target)
				h := tmuxhost.New("")
				// Canonical "open default branch" sequence — ensures
				// session, ensures default-branch window, lands outer
				// client, fires bg-pull, registers in cache. One
				// primitive, no inline reimplementation.
				return workspace.OpenDefaultBranch(h, session, target, defaultBranch,
					ensureDefaultBranchWindow)
			}
		},
	}
	return c
}

// ============================================================================
// DeleteCommand + helpers for the fzf bind transforms
// ============================================================================

func DeleteCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "delete",
		Short: "Delete the current worktree + cascade popup cleanup",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			pathOut, err := h.DisplayMessage("#{pane_current_path}")
			if err != nil {
				return err
			}
			worktreeRoot := workspaceWorktreeRoot()
			if !strings.HasPrefix(pathOut, worktreeRoot) {
				return fmt.Errorf("current path %q is not under %q; refusing to delete", pathOut, worktreeRoot)
			}
			repoSlug, _ := splitWorktreePath(pathOut, worktreeRoot)
			repoPath := filepath.Join(workspaceCodeRoot(), repoSlug)
			if err := removeWorktree(repoPath, pathOut); err != nil {
				return fmt.Errorf("git worktree remove: %w", err)
			}
			if _, err := h.Run("kill-window"); err != nil {
				return err
			}
			return hostpopup.CleanupOrphanedPopups(h)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// deleteRow soft-closes (or fully removes, for the sole-window and
// default-branch cases) the workspace identified by (session, window).
// Called in-process by the bubbletea M-s picker's inline delete, with the
// outer-client hop rules that keep the picker's popup-client alive across a
// self-delete.
func deleteRow(h *tmuxhost.Client, session, window string) error {
	repoPath, _ := getSessionRepoPath(h, session)
	defaultBranch := ""
	if repoPath != "" {
		defaultBranch = DefaultBranch(repoPath)
	}

	// M-s M-x is a SOFT close. It removes the workspace from the
	// live picker (kill-window + statestore prune) but does NOT
	// touch the on-disk worktree directory. M-r still sees it
	// and the user can restore the workspace by picking it.
	// Permanent worktree deletion (rm -rf the dir + branch) is
	// reserved for the M-r picker's own M-x — that flow is
	// explicit about "I really mean it".
	if repoPath != "" && window != defaultBranch {
		// Stamp the soft-close marker FIRST, before any tmux
		// mutation. When the victim is the sole window in its
		// session AND the outer client is parked on it, the
		// kill-window below destroys the session and tears down
		// the pane/client hosting THIS very process — so anything
		// after the kill (RemoveWindow, the marker, cleanup) may
		// never run. The marker only needs the (stable) worktree
		// path, so writing it up front makes M-r's "closed X ago"
		// badge survive a self-delete. (Bug: the badge silently
		// vanished exactly when you deleted the workspace you were
		// sitting on.)
		touchSoftClosedMarker(filepath.Join(workspaceWorktreeRoot(), session, window))
		// If the target window is the outer client's CURRENT
		// window, killing it forces tmux to auto-switch which
		// tears down the popup-client holding the sessions
		// picker. Hop the outer to a safe spot before the kill
		// so the picker survives. When a sibling window exists it
		// absorbs the hop (default-branch preferred). When this is
		// the SOLE window, kill-window empties — and destroys —
		// the session, so the hop must go to another workspace
		// entirely or tmux detaches the outer.
		if soleWindowInSession(h, session, window) {
			moveOuterToSiblingSession(h, session)
		} else {
			moveOuterAway(h, session, window, defaultBranch)
		}
		_ = statestore.RemoveWindow(session, window)
		_, _ = h.Run("kill-window", "-t", "="+session+":"+window)
		return hostpopup.CleanupOrphanedPopups(h)
	}
	// Default-branch window with siblings still open: DISMISS the
	// window only, keeping the session (and its attached
	// workspaces) alive. The default-branch window is ephemeral by
	// design — the create flow kills it
	// (CreateWorktreeWindow.KillDefaultBranch) and OpenDefaultBranch
	// recreates it on demand — so removing it here is reversible,
	// not destructive. No soft-close marker: the default branch
	// lives at the repo root, not an atelier worktree under
	// workspaceWorktreeRoot(), and reopening it is a single keypress
	// (open the repo again). Hop the outer off it first so the
	// picker's popup-client survives the kill.
	if repoPath != "" && window == defaultBranch && !soleWindowInSession(h, session, window) {
		moveOuterAway(h, session, window, defaultBranch)
		_ = statestore.RemoveWindow(session, window)
		_, _ = h.Run("kill-window", "-t", "="+session+":"+window)
		return hostpopup.CleanupOrphanedPopups(h)
	}
	// Sole window (default branch or non-git row): kill whole
	// session. If the outer client is parked on this session, land
	// it on another workspace first — otherwise kill-session
	// detaches the outer (and tears down the M-s popup) instead of
	// switching.
	moveOuterToSiblingSession(h, session)
	_, _ = h.Run("kill-session", "-t", "="+session)
	_ = statestore.RemoveSession(session)
	return hostpopup.CleanupOrphanedPopups(h)
}

// moveOuterAway switches the outer client off `victimWindow` before it
// gets killed, so the popup pty holding the sessions picker survives.
// Tries `defaultBranch` first; falls back to any other window in the
// session. No-op when the outer isn't on `victimWindow` to begin with.
func moveOuterAway(h *tmuxhost.Client, session, victimWindow, defaultBranch string) {
	outer, _ := h.ShowGlobalOption("@atelier_outer_client")
	outer = strings.TrimSpace(outer)
	if outer == "" {
		return
	}
	curWin, _ := h.DisplayMessage("#{window_name}")
	curSess, _ := h.DisplayMessage("#{session_name}")
	if strings.TrimSpace(curSess) != session || strings.TrimSpace(curWin) != victimWindow {
		return
	}
	// Pick a sibling. Prefer the session's default-branch window when
	// it exists; otherwise the first non-victim window.
	listOut, err := h.Run("list-windows", "-t", "="+session, "-F", "#W")
	if err != nil {
		return
	}
	candidates := strings.Split(strings.TrimSpace(string(listOut)), "\n")
	target := ""
	for _, w := range candidates {
		w = strings.TrimSpace(w)
		if w == "" || w == victimWindow {
			continue
		}
		if w == defaultBranch {
			target = w
			break
		}
		if target == "" {
			target = w
		}
	}
	if target == "" {
		return // sole window in session — kill-session path will handle this
	}
	// LandOuter handles the select-window + switch-client -c outer
	// sequence correctly (and tests enforce that no inline switch-client
	// lives in this file).
	if err := workspace.LandOuter(h, "="+session, "="+session+":"+target); err != nil {
		debuglog.LogErr("_delete-row: LandOuter to sibling", err)
		return
	}
	debuglog.Logf("_delete-row: hopped outer=%q off victim=%s/%s to sibling=%s", outer, session, victimWindow, target)
}

// soleWindowInSession reports whether `window` is the only window in
// `session` — i.e. killing it would empty and destroy the session.
func soleWindowInSession(h *tmuxhost.Client, session, window string) bool {
	out, err := h.Run("list-windows", "-t", "="+session, "-F", "#W")
	if err != nil {
		return false
	}
	n := 0
	for _, w := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(w) != "" {
			n++
		}
	}
	return n <= 1
}

// moveOuterToSiblingSession lands the outer client on another live
// workspace before `victimSession` gets killed, so the M-s popup
// survives (another workspace simply becomes active) instead of tmux
// detaching the client. No-op when the outer isn't parked on
// `victimSession` — deleting some OTHER workspace from the picker must
// not yank the user off the one they're viewing — or when there's no
// other workspace to hop to (sole-workspace server: detach is then
// unavoidable). Sibling to moveOuterAway, at session granularity.
func moveOuterToSiblingSession(h *tmuxhost.Client, victimSession string) {
	outer, _ := h.ShowGlobalOption("@atelier_outer_client")
	outer = strings.TrimSpace(outer)
	if outer == "" {
		return
	}
	// Is the outer actually on the session we're about to kill? Compare
	// by session_id (outerCurrent reads the live outer client) so a name
	// collision can't misfire.
	curSid, _, err := outerCurrent(h)
	if err != nil {
		return
	}
	victimSid, err := h.DisplayMessageAt(victimSession, "#{session_id}")
	if err != nil {
		return
	}
	if strings.TrimSpace(curSid) == "" || strings.TrimSpace(curSid) != strings.TrimSpace(victimSid) {
		return
	}
	// Land on the highest-priority OTHER workspace. BuildSessionList
	// already filters internal `_` sessions and sorts attention/claude
	// first, so the first non-victim row is the natural next workspace.
	rows, err := BuildSessionList(h)
	if err != nil {
		return
	}
	for _, r := range rows {
		if r.Session == victimSession {
			continue
		}
		if err := workspace.LandOuter(h, "="+r.Session, "="+r.Session+":"+r.Window); err != nil {
			debuglog.LogErr("_delete-row: LandOuter to sibling session", err)
			return
		}
		debuglog.Logf("_delete-row: hopped outer=%q off victim session=%s to %s/%s",
			outer, victimSession, r.Session, r.Window)
		return
	}
}

// softClosedMarker is the basename of the per-worktree file that
// records the most recent M-s M-x soft-close timestamp. Its mtime is
// the primary sort key in the M-r picker — recently-soft-closed
// worktrees rank above untouched ones.
const softClosedMarker = ".atelier-soft-closed"

// touchSoftClosedMarker writes/updates the soft-close marker file at
// the top of `wtPath`. The file's content is the epoch (for human
// inspection); its mtime is what M-r's sort actually reads. Best-
// effort: errors only log — the marker is a UX hint, not a load-
// bearing invariant.
func touchSoftClosedMarker(wtPath string) {
	if wtPath == "" {
		return
	}
	if _, err := os.Stat(wtPath); err != nil {
		debuglog.Logf("touchSoftClosedMarker: skip — wtPath %q not on disk (%v)", wtPath, err)
		return // worktree no longer on disk (e.g. the rare case it was already wiped externally)
	}
	path := filepath.Join(wtPath, softClosedMarker)
	now := time.Now()
	if err := os.WriteFile(path, []byte(strconv.FormatInt(now.Unix(), 10)+"\n"), 0o644); err != nil {
		debuglog.LogErr(fmt.Sprintf("touchSoftClosedMarker: write %s", path), err)
		return
	}
	// Explicit Chtimes covers the case where the file already existed
	// (re-soft-close) and write didn't bump mtime to "now" cleanly.
	_ = os.Chtimes(path, now, now)
	debuglog.Logf("touchSoftClosedMarker: wrote %s", path)
}

// readSoftClosedMarker returns the marker file's mtime when present,
// zero time otherwise. Used by the M-r picker to rank entries.
func readSoftClosedMarker(wtPath string) time.Time {
	info, err := os.Stat(filepath.Join(wtPath, softClosedMarker))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// clearSoftClosedMarker removes the marker so the entry stops
// ranking at the top of M-r once the user has actually recovered it.
func clearSoftClosedMarker(wtPath string) {
	_ = os.Remove(filepath.Join(wtPath, softClosedMarker))
}

// AutoSessionCommand: port of tmux_workspace_auto_session
func AutoSessionCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_auto-session",
		Short:  "internal: multi-repo session creator (Ctrl-A in repo picker dispatches here)",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			initialPrompt := ""
			if len(args) > 0 {
				initialPrompt = args[0]
			}
			return runAutoSession(initialPrompt)
		},
	}
}

// CreateCommand kept for back-compat (non-interactive create from --repo/--branch).
func CreateCommand() *cobra.Command {
	var repo, branch, socket string
	c := &cobra.Command{
		Use:   "create",
		Short: "Create a workspace non-interactively (--repo --branch)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if repo == "" || branch == "" {
				return fmt.Errorf("--repo and --branch are required")
			}
			repoPath := filepath.Join(workspaceCodeRoot(), repo)
			defaultBranch := DefaultBranch(repoPath)
			return runWorkspaceName(repo, repoPath, defaultBranch, branch)
		},
	}
	c.Flags().StringVar(&repo, "repo", "", "repo (owner/repo)")
	c.Flags().StringVar(&branch, "branch", "", "branch / worktree name")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// RecoverCommand opens the M-r "Workspace History" picker: every worktree
// under WorktreeRoot (whether or not a live tmux session backs it),
// with Enter to open and M-x to delete. Sibling to:
//   - M-n (creator) — make a NEW worktree
//   - M-s (sessions) — pick among LIVE tmux sessions
//
// The three pickers cross-jump via M-n/M-s/M-r/M-u footer keys.
func RecoverCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "recover",
		Short: "Pick or delete an existing worktree (M-r)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			items, err := recoverListItems()
			if err != nil {
				return err
			}
			outcome, err := tui.Run(newRecoverModel(h, items))
			if err != nil {
				if errors.Is(err, tui.ErrCancelled) {
					debuglog.Logf("workspaces.recover: cancelled")
				}
				return err
			}
			switch outcome.Key {
			case "workspaces/sessions":
				return tui.ExecReplace("tools", "workspaces", "sessions")
			case "workspaces/pick":
				return tui.ExecReplace("tools", "workspaces", "pick")
			case "toolselector/select":
				return tui.ExecReplace("tools", "toolselector", "select")
			case "workspaces/clone":
				return tui.ExecReplace("tools", "workspaces", "clone")
			}
			if outcome.Selection == "" {
				return tui.ErrCancelled
			}
			repo, branch, ok := strings.Cut(outcome.Selection, "\x00")
			if !ok {
				return fmt.Errorf("could not parse picked entry: %q", outcome.Selection)
			}
			return openWorktreeWorkspace(h, repo, branch)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// recoverListItems shapes the on-disk worktree list into rows for the M-r
// bubbletea picker: repo/branch with a subtitle badge. Soft-closed worktrees
// rank first (listWorktrees sort) so recovery is one Enter away.
//
// Badges:
//   - `⏎ closed Nm ago` — worktree was soft-closed in M-s recently.
//   - `● live` — a tmux window for this worktree is already open.
func recoverListItems() ([]list.Item, error) {
	wts, err := listWorktrees(workspaceWorktreeRoot())
	if err != nil {
		return nil, err
	}
	live := liveWorktreeWindows(tmuxhost.New(""))
	now := time.Now()
	out := make([]list.Item, 0, len(wts))
	for _, w := range wts {
		desc := "worktree on disk"
		switch {
		case !w.softClosedAt.IsZero():
			desc = "⏎ closed " + relativeAge(now.Sub(w.softClosedAt))
		case live[w.repo+"\t"+w.branch]:
			desc = "● live"
		}
		out = append(out, recoverItem{repo: w.repo, branch: w.branch, desc: desc})
	}
	return out, nil
}

// liveWorktreeWindows returns the set of (session, window) pairs
// currently open in tmux, formatted as keys "<session>\t<window>" so
// recoverPickerRows can probe membership in O(1). Best-effort: any
// tmux error returns an empty map — the badge is a UX hint, not a
// correctness invariant.
func liveWorktreeWindows(h *tmuxhost.Client) map[string]bool {
	out, err := h.Run("list-windows", "-a", "-F", "#{session_name}\t#{window_name}")
	if err != nil {
		return map[string]bool{}
	}
	live := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		live[line] = true
	}
	return live
}

// relativeAge formats a duration as a compact "Nm" / "Nh" / "Nd" tag
// for the M-r picker's soft-close badge.
func relativeAge(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours())/24)
}

// recoverDeleteRow removes a worktree and (if any) its backing tmux window,
// plus the statestore window entry so restore doesn't resurrect it. Called
// in-process by the M-r bubbletea picker's inline delete.
func recoverDeleteRow(h *tmuxhost.Client, repo, branch string) error {
	repoPath := filepath.Join(workspaceCodeRoot(), repo)
	wtPath := filepath.Join(workspaceWorktreeRoot(), repo, branch)
	// Paths keep the raw repo name; tmux/statestore identity is normalized.
	session := workspace.SessionName(repo)

	// Best-effort kill of any live tmux window for this worktree BEFORE
	// removing the directory — otherwise the window's shell sits with a
	// missing cwd.
	if has, _ := h.HasSession(session); has {
		out, _ := h.Run("list-windows", "-t", "="+session, "-F", "#{window_id}\t#W")
		for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(ln, "\t", 2)
			if len(parts) == 2 && parts[1] == branch {
				_, _ = h.Run("kill-window", "-t", parts[0])
				break
			}
		}
	}
	if err := removeWorktree(repoPath, wtPath); err != nil {
		debuglog.LogErr(fmt.Sprintf("workspaces.recover: remove %s", wtPath), err)
	}
	_ = statestore.RemoveWindow(session, branch)
	return hostpopup.CleanupOrphanedPopups(h)
}

// openWorktreeWorkspace ensures the repo's tmux session exists, ensures
// the worktree's window exists in it (creating one if absent), then
// LandOuter switches the user's outer client onto it.
func openWorktreeWorkspace(h *tmuxhost.Client, repo, branch string) error {
	repoPath := filepath.Join(workspaceCodeRoot(), repo)
	wtPath := filepath.Join(workspaceWorktreeRoot(), repo, branch)
	defaultBranch := DefaultBranch(repoPath)
	// Paths keep the raw repo name; tmux/statestore identity is normalized.
	session := workspace.SessionName(repo)

	// Recovering this worktree → drop the "soft-closed" marker so it
	// stops ranking at the top of M-r on subsequent opens.
	clearSoftClosedMarker(wtPath)

	created, err := workspace.EnsureSession(h, session, repoPath, defaultBranch)
	if err != nil {
		return err
	}
	// If a window with `branch` name already exists, jump to it.
	// LandOuter alone leaves the shell wherever the user last `cd`'d
	// — recover semantics promise the shell lands IN the worktree, so
	// also fire `cd <wtPath>\n` into the active pane when its
	// pane_current_path doesn't already match.
	out, _ := h.Run("list-windows", "-t", "="+session, "-F", "#{window_id}\t#W")
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && parts[1] == branch {
			wid := parts[0]
			spawnClaudeResume(h, session, wid)
			if err := workspace.LandOuter(h, "="+session, wid); err != nil {
				return err
			}
			ensurePaneCwd(h, wid, wtPath)
			return nil
		}
	}
	// Create the worktree window. KillDefaultBranch only when we JUST
	// auto-created the session (matches the runWorkspaceName pattern).
	spec := workspace.WorktreeWindowSpec{
		Session:    session,
		WtPath:     wtPath,
		WindowName: branch,
		Kind:       "worktree",
		Metadata:   recoverWindowMetadata(session, branch),
	}
	if created {
		spec.KillDefaultBranch = defaultBranch
	}
	newWid, err := workspace.CreateWorktreeWindow(h, spec)
	if err != nil {
		return err
	}
	spawnClaudeResume(h, session, newWid)
	return workspace.LandOuter(h, "="+session, newWid)
}

// recoverWindowMetadata loads the persisted plugin metadata for a
// soft-closed worktree window from the statestore cache, so the freshly
// re-created window is re-stamped with (notably) `ai.active_session_id`.
// Without this the M-r recover path creates a bare window and
// spawnClaudeResume finds no session id to `--resume` — the Claude
// session silently starts fresh instead of continuing where it left off.
//
// Mirrors what workspace.Restore does on server startup, which reads the
// same cache. Returns nil when nothing is cached (brand-new window).
func recoverWindowMetadata(session, branch string) map[string]string {
	st, err := statestore.Load()
	if err != nil || st == nil {
		return nil
	}
	w := st.FindWindow(session, branch)
	if w == nil || len(w.Metadata) == 0 {
		return nil
	}
	return w.Metadata
}

// spawnClaudeResume queues a Claude popup for `repo:windowID` when the
// window has either (a) a live popup-backing session already, or (b) a
// persisted `@ai_active_session_id` — same triggers as the sessions
// picker's auto-resume. The popup launches with `--resume <id>` when
// only the option is present (popup-session reborn fresh) or attaches
// to the existing pty when the session is already live.
//
// Best-effort: errors don't abort the workspace open. Queued via
// `run-shell -b` with a 0.2s sleep so the popup fires AFTER LandOuter
// and the user is visually on the new workspace.
func spawnClaudeResume(h *tmuxhost.Client, session, windowID string) {
	if agentAutoOpenSkipped() {
		return
	}
	sid, err := h.DisplayMessageAt(session, "#{session_id}")
	if err != nil {
		return
	}
	sid = strings.TrimSpace(sid)
	wid := strings.TrimSpace(windowID)
	if sid == "" || wid == "" {
		return
	}
	// Deferred agent open: only (re)open the agent popup on land when the
	// active AI integration has something to show — a live popup, or
	// resumable state (tracked session id, or an on-disk transcript for the
	// cwd; a soft-close prunes the tracked id, so the on-disk check lets the
	// FIRST recover-after-delete still resume). No AI configured → never
	// auto-open; the workspace lands on a bare shell.
	ai := integration.Active().AI
	if ai == nil {
		return
	}
	hasPopup, _ := h.HasSession(ai.AgentPopupSession(sid, wid))
	if !hasPopup {
		cwd, _ := h.DisplayMessageAt(wid, "#{pane_current_path}")
		if !ai.HasResumableState(h, wid, strings.TrimSpace(cwd)) {
			return
		}
	}
	sidNum := strings.TrimPrefix(sid, "$")
	widNum := strings.TrimPrefix(wid, "@")
	popupOpts := initgen.PopupOptions(manifest.StyleFull, "Claude Code", false)
	popupCmd := fmt.Sprintf(
		`sleep 0.2 && tmux display-popup %s -e TMUX_PARENT_SESSION_ID=%s -e TMUX_PARENT_WINDOW_ID=%s -E '%s'`,
		popupOpts, sidNum, widNum, dispatch.CoreCmd("ai", "open"))
	_, _ = h.Run("run-shell", "-b", popupCmd)
	debuglog.Logf("openWorktreeWorkspace: queued agent resume for %s:%s (hasPopup=%v)",
		sid, wid, hasPopup)
}

// ensurePaneCwd sends `cd <wtPath>\n` to the active pane of `windowID`
// if its pane_current_path is anything other than `wtPath` (or a
// subdirectory of it). Best-effort: errors are logged, not returned —
// landing on the window is the load-bearing action; cd is the
// nice-to-have.
//
// Subdirs of `wtPath` are accepted so a user who's `cd src/`'d inside
// the worktree doesn't get yanked back to the root on every M-r.
func ensurePaneCwd(h *tmuxhost.Client, windowID, wtPath string) {
	cur, err := h.DisplayMessageAt(windowID, "#{pane_current_path}")
	if err != nil {
		debuglog.LogErr("ensurePaneCwd: pane_current_path", err)
		return
	}
	cur = strings.TrimSpace(cur)
	if cur == wtPath || strings.HasPrefix(cur, wtPath+"/") {
		return
	}
	debuglog.Logf("ensurePaneCwd: cwd=%q != wtPath=%q — sending cd", cur, wtPath)
	// shellQuote the path so spaces / special chars survive the shell parse.
	if _, err := h.Run("send-keys", "-t", windowID, fmt.Sprintf("cd %q", wtPath), "Enter"); err != nil {
		debuglog.LogErr("ensurePaneCwd: send-keys", err)
	}
}

// ============================================================================
// Internal: tmux_workspace_name port
// ============================================================================

// runWorkspaceName is the bash tmux_workspace_name port. Loops until the
// user picks/confirms a name or cancels. Pre-fills query on error retries.
func runWorkspaceName(repo, repoPath, defaultBranch, initialName string) error {
	session := workspace.SessionName(repo)

	ensureSession := func() (created bool, err error) {
		return workspace.EnsureSession(tmuxhost.New(""), session, repoPath, defaultBranch)
	}

	query := initialName
	errMsg := ""
	name := initialName

	for {
		if name == "" {
			header := "branch name → new worktree · empty opens " + defaultBranch
			if errMsg != "" {
				header = errMsg
			}
			outcome, err := tui.Run(tui.NewPrompt(tui.CreatorTheme(), tui.PromptConfig{
				Title:       " Workspace in " + repo + " ",
				Glyph:       "製 ",
				Placeholder: "branch name",
				Initial:     query,
				Header:      header,
				HeaderError: errMsg != "",
				Actions: []tui.KeyAction{
					tui.Action("mode:ai", "alt+m", "M-m", "AI name"),
					tui.Action("workspaces/sessions", "alt+s", "M-s", "sessions"),
					tui.Action("workspaces/recover", "alt+r", "M-r", "recover"),
					tui.Action("toolselector/select", "alt+;", "M-;", "tools"),
					tui.Action("workspaces/clone", "alt+u", "M-u", "clone"),
				},
			}))
			if err != nil {
				return err
			}
			switch outcome.Key {
			case "mode:ai":
				return tui.ExecReplace("tools", "workspaces", "_prompt", repo, repoPath, defaultBranch)
			case "workspaces/sessions":
				return tui.ExecReplace("tools", "workspaces", "sessions")
			case "workspaces/recover":
				return tui.ExecReplace("tools", "workspaces", "recover")
			case "toolselector/select":
				return tui.ExecReplace("tools", "toolselector", "select")
			case "workspaces/clone":
				return tui.ExecReplace("tools", "workspaces", "clone")
			}
			name = strings.TrimSpace(outcome.Query)
			if name == "" {
				// Canonical "open default branch" sequence: ensure
				// session + default-branch window + LandOuter +
				// bg-pull + cache registration. Single primitive.
				return workspace.OpenDefaultBranch(
					tmuxhost.New(""), session, repoPath, defaultBranch,
					ensureDefaultBranchWindow)
			}
		}

		// If the name maps to an existing window or worktree, reuse it
		// (jump / attach) instead of rebuilding.
		h := tmuxhost.New("")
		if _, newWid, handled, err := reuseExistingWorkspace(h, session, repoPath, name, defaultBranch); err != nil {
			return err
		} else if handled {
			return workspace.LandOuter(h, "="+session, newWid)
		}

		// A branch with this name exists but isn't a worktree — re-prompt.
		if branchExists(repoPath, name) {
			errMsg = fmt.Sprintf("✗ branch '%s' exists but isn't a worktree — pick another name", name)
			query = name
			name = ""
			continue
		}

		// Build the worktree.
		wtPath := filepath.Join(workspaceWorktreeRoot(), repo, name)
		_ = os.MkdirAll(filepath.Dir(wtPath), 0o755)
		var newWid string
		buildErr := tui.Loader(fmt.Sprintf("Building workspace '%s'…", name), func(status func(string)) error {
			status(fmt.Sprintf("Fetching origin/%s…", defaultBranch))
			if err := runGit(repoPath, "fetch", "origin", defaultBranch); err != nil {
				return err
			}
			base, tracking := resolveWorktreeBase(repoPath, name, defaultBranch)
			if tracking {
				status(fmt.Sprintf("Tracking origin/%s…", name))
			} else {
				status("Building worktree…")
			}
			if err := runGit(repoPath, "worktree", "add", wtPath, "-b", name, base); err != nil {
				return err
			}
			status("Stamping tmux options…")
			created, err := ensureSession()
			if err != nil {
				return err
			}
			spec := workspace.WorktreeWindowSpec{
				Session:    session,
				WtPath:     wtPath,
				WindowName: name,
			}
			if created {
				spec.KillDefaultBranch = defaultBranch
			}
			newWid, err = workspace.CreateWorktreeWindow(h, spec)
			return err
		})
		if buildErr != nil {
			errMsg = fmt.Sprintf("✗ workspace '%s' build failed — try another name", name)
			query = name
			name = ""
			continue
		}
		return workspace.LandOuter(h, "="+session, newWid)
	}
}

// ============================================================================
// Internal: tmux_workspace_prompt port (auto-mode Claude-named branch)
// ============================================================================

func PromptCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_prompt",
		Short:  "internal: auto-mode Claude-named branch flow (Ctrl-A in manual name)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			repo := args[0]
			repoPath := args[1]
			defaultBranch := args[2]
			initialPrompt := ""
			if len(args) > 3 {
				initialPrompt = args[3]
			}
			return runWorkspacePrompt(repo, repoPath, defaultBranch, initialPrompt)
		},
	}
}

func runWorkspacePrompt(repo, repoPath, defaultBranch, initialPrompt string) error {
	session := workspace.SessionName(repo)
	prompt := strings.TrimSpace(initialPrompt)
	// Skip fzf when a caller (Ctrl-A from _name, tests supplying the
	// prompt arg) already provided the prompt — the picker only exists
	// to elicit the task description from the user.
	if prompt == "" {
		outcome, err := tui.Run(tui.NewPrompt(tui.CreatorTheme(), tui.PromptConfig{
			Title:       " Workspace in " + repo + " ",
			Glyph:       "製 ",
			Placeholder: "task description — AI names the branch (empty opens " + defaultBranch + ")",
			Header:      "AI branch-naming · M-m for a manual name",
			Actions: []tui.KeyAction{
				tui.Action("mode:name", "alt+m", "M-m", "manual name"),
				tui.Action("workspaces/sessions", "alt+s", "M-s", "sessions"),
				tui.Action("workspaces/recover", "alt+r", "M-r", "recover"),
				tui.Action("toolselector/select", "alt+;", "M-;", "tools"),
				tui.Action("workspaces/clone", "alt+u", "M-u", "clone"),
			},
		}))
		if err != nil {
			return err
		}
		switch outcome.Key {
		case "mode:name":
			return tui.ExecReplace("tools", "workspaces", "_name", repo, repoPath, defaultBranch)
		case "workspaces/sessions":
			return tui.ExecReplace("tools", "workspaces", "sessions")
		case "workspaces/recover":
			return tui.ExecReplace("tools", "workspaces", "recover")
		case "toolselector/select":
			return tui.ExecReplace("tools", "toolselector", "select")
		case "workspaces/clone":
			return tui.ExecReplace("tools", "workspaces", "clone")
		}
		prompt = strings.TrimSpace(outcome.Query)
		if prompt == "" {
			// Empty → open default branch (canonical primitive).
			return workspace.OpenDefaultBranch(
				tmuxhost.New(""), session, repoPath, defaultBranch,
				ensureDefaultBranchWindow)
		}
	}

	// Test hook: e2e tests need `_prompt → build → state` to be
	// synchronous so their assertions don't race the deferred
	// display-popup. Set ATELIER_SYNC_BUILD=1 (only meaningful in
	// tests) to bypass the deferred spinner popup and run the build
	// inline in this pty.
	if os.Getenv("ATELIER_SYNC_BUILD") == "1" {
		return runWorkspaceBuild(prompt, repo, repoPath, defaultBranch)
	}

	// Defer the build into a spinner-sized popup that renders ON TOP of
	// the Claude popup the creator was launched from — the user keeps
	// seeing their Claude session while the build runs, then _build's
	// LandOuter swaps to the new workspace and reopens Claude there.
	//
	// This must nest on the tool popup's INNER client, not the outer
	// client: tmux allows only one popup per client, so a second
	// display-popup on the outer client (which the Claude popup occupies)
	// is silently dropped and never launches _build. OpenOverInnerPopup
	// targets the inner client and defers past this process's exit so the
	// creator popup closes first and frees that client to stack the
	// spinner. Writing the prompt to a tmp file avoids shell-escaping any
	// special chars the user typed; other args are shell-safe
	// repo/path/branch names.
	specPath, err := writeBuildSpec(prompt)
	if err != nil {
		return err
	}
	invoke := fmt.Sprintf("%s --spec-file=%s --repo=%s --repo-path=%s --default-branch=%s",
		dispatch.ToolCmd("workspaces", "_build"),
		specPath, repo, repoPath, defaultBranch)
	return hostpopup.OpenOverInnerPopup(
		tmuxhost.New(""),
		hostpopup.SpinnerStyleArgs("Building workspace"),
		invoke,
	)
}

// writeBuildSpec persists the prompt to a temp file so `_build` can read
// it without shell-escaping concerns. Caller is `_build`, which removes
// the file after reading.
func writeBuildSpec(prompt string) (string, error) {
	f, err := os.CreateTemp("", "atelier-build-*.txt")
	if err != nil {
		return "", fmt.Errorf("write build spec: %w", err)
	}
	if _, err := f.WriteString(prompt); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write build spec: %w", err)
	}
	_ = f.Close()
	return f.Name(), nil
}

// BuildCommand is the deferred entry point invoked from a spinner-sized
// popup queued by runWorkspacePrompt. Runs the actual workspace build
// (fetch → worktree → stamp → LandOuter → queue Claude popup) inside
// its own popup so the picker's larger popup rectangle isn't visible as
// empty background around the spinner.
func BuildCommand() *cobra.Command {
	var specFile, repo, repoPath, defaultBranch string
	c := &cobra.Command{
		Use:    "_build",
		Short:  "internal: workspace build stage (spawned in spinner popup by _prompt)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			promptBytes, err := os.ReadFile(specFile)
			if err != nil {
				return fmt.Errorf("read spec: %w", err)
			}
			_ = os.Remove(specFile)
			return runWorkspaceBuild(string(promptBytes), repo, repoPath, defaultBranch)
		},
	}
	c.Flags().StringVar(&specFile, "spec-file", "", "path to prompt spec file (deleted after read)")
	c.Flags().StringVar(&repo, "repo", "", "repo (owner/name)")
	c.Flags().StringVar(&repoPath, "repo-path", "", "absolute path to repo")
	c.Flags().StringVar(&defaultBranch, "default-branch", "", "repo's default branch")
	return c
}

func runWorkspaceBuild(prompt, repo, repoPath, defaultBranch string) error {
	session := workspace.SessionName(repo)
	h := tmuxhost.New("")

	// Feed the existing tag vocabulary to the naming call so the AI reuses
	// labels instead of fragmenting synonyms (issue #56).
	autoTag := workspaceAutoTagEnabled()
	var existingTags []string
	if autoTag {
		existingTags = collectTags(h)
	}

	var name, wtPath, tag, newWid string
	err := tui.Loader("Building workspace…", func(status func(string)) error {
		n, w, tg, e := buildClaudeNamedWorkspace(status, prompt, repo, repoPath, defaultBranch, existingTags, autoTag)
		name, wtPath, tag = n, w, tg
		if e != nil {
			return e
		}
		// Stamping stage: ensureSession + new-window + set-option.
		// Kept inside the loader so the four-stage sequence renders
		// cleanly. Visible client moves (select-window / switch-client)
		// happen AFTER the loader closes so the user isn't shown a
		// transient view.
		status("Stamping tmux options…")
		created, err := workspace.EnsureSession(h, session, repoPath, defaultBranch)
		if err != nil {
			return err
		}
		spec := workspace.WorktreeWindowSpec{
			Session:    session,
			WtPath:     wtPath,
			WindowName: name,
			Kind:       "worktree",
			// TODO(plugins-refactor): the prompt + workspace-kind
			// metadata writes are AI-plugin concerns — workspaces is
			// hardcoding the AI namespace. When task #75 lands, the
			// AI plugin should contribute these via a "before-create"
			// hook instead of workspaces knowing about ai.* keys.
			Metadata: map[string]string{
				"ai.prompt":         prompt,
				"ai.workspace_kind": "worktree",
			},
		}
		// AI-suggested grouping tag (issue #56): stamped through the same
		// metadata map, so CreateWorktreeWindow writes @workspace_tag and
		// mirrors it to the statestore in one pass — no separate SetTag.
		// Empty when auto-tag is off or the model proposed no tag.
		if tag != "" {
			spec.Metadata[workspace.TagMetadataKey] = tag
		}
		if created {
			spec.KillDefaultBranch = defaultBranch
		}
		newWid, err = workspace.CreateWorktreeWindow(h, spec)
		return err
	})
	if err != nil {
		// The picker popup is already gone by the time _build runs, so no
		// re-prompt loop is possible from here. When Claude's generated
		// name collides with an existing workspace (common: vague prompts
		// all name to `chore/wip`), reuse it and land there instead of
		// dumping the user out of the creator.
		if errors.Is(err, errBranchAlreadyExists) {
			if wt, wid, handled, rerr := reuseExistingWorkspace(h, session, repoPath, name, defaultBranch); rerr == nil && handled {
				wtPath, newWid = wt, wid
				err = nil
			}
		}
		if err != nil {
			// Surface the failure via display-message (visible on the outer
			// client's statusline) and exit; user re-invokes M-n to retry.
			_, _ = h.Run("display-message", fmt.Sprintf("✗ workspace build failed: %v", err))
			return err
		}
	}

	// Queue the Claude popup BEFORE LandOuter. LandOuter's
	// detachStalePopups closes any `_atelier_*` popup scoped to a
	// DIFFERENT (sid,wid) than the target — and `_build` is
	// itself running inside such a popup (the spinner popup queued
	// by `_prompt`). The deferred detach fires asynchronously and
	// SIGHUPs our pty before we can queue the Claude popup if we
	// wait. By queuing first, the `sleep 0.15 && display-popup -c
	// <outerClient>` command is already in tmux's run-shell queue;
	// it fires on the outer client regardless of whether our pty
	// survives.
	newSid, _ := h.DisplayMessageAt(newWid, "#{session_id}")
	sidNum := strings.TrimPrefix(strings.TrimSpace(newSid), "$")
	widNum := strings.TrimPrefix(newWid, "@")
	outerClient, _ := h.ShowGlobalOption("@atelier_outer_client")
	clientArg := ""
	if outerClient != "" {
		clientArg = fmt.Sprintf(" -c '%s'", outerClient)
	}
	// TMUX_PARENT_PANE_PWD pins the popup's cwd to the NEW worktree
	// path. Without it, popup.ResolveParentContext falls back to
	// reading @atelier_outer_pane's pane_current_path — and that
	// global was stamped by M-; on the user's PREVIOUS workspace
	// pane, so Claude would launch in the wrong cwd while still
	// being bound to the new window's popup-session. Symptom: user
	// selects workspace B, Claude opens in workspace A's worktree.
	popupCmd := fmt.Sprintf(
		`sleep 0.15 && tmux display-popup%s -b rounded -S "fg=colour103" -T "#[align=centre] Claude Code " -w100%% -h99%% -y S -e TMUX_PARENT_SESSION_ID=%s -e TMUX_PARENT_WINDOW_ID=%s -e TMUX_PARENT_PANE_PWD=%q -E '%s'`,
		clientArg, sidNum, widNum, wtPath, dispatch.CoreCmd("ai", "open"))
	// Skip the deferred agent auto-open on test sockets (matching the
	// spawnClaudeResume / openWorktreeWorkspace guards): the popup's
	// `ai open` runs clearLaunchPrompt, which consumes the one-shot
	// prompt and persists ai.prompt="" — that fires ~0.15s later and
	// races the e2e test's statestore read, flakily wiping the prompt.
	if !agentAutoOpenSkipped() {
		_, _ = h.Run("run-shell", "-b", popupCmd)
	}

	if err := workspace.LandOuter(h, "="+session, newWid); err != nil {
		return err
	}
	// Log post-state so we can see where the client actually landed.
	if v, err := h.DisplayMessage("#{client_name}|#{client_session}|#{window_id}|#{window_name}"); err == nil {
		debuglog.Logf("runWorkspaceBuild: post-switch state=%q", v)
	}
	return nil
}

// NameCommand is the alias used from _prompt's Ctrl-A.
func NameCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_name",
		Short:  "internal: manual-name flow",
		Hidden: true,
		Args:   cobra.MinimumNArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			repo := args[0]
			repoPath := args[1]
			defaultBranch := args[2]
			initial := ""
			if len(args) > 3 {
				initial = args[3]
			}
			return runWorkspaceName(repo, repoPath, defaultBranch, initial)
		},
	}
}

// ============================================================================
// Internal: tmux_workspace_build port
// ============================================================================

var conventionalBranchRe = regexp.MustCompile(`^(feat|fix|chore|refactor|docs|test|perf|style)/[a-z0-9-]+$`)

// branchPromptMaxChars caps the user's intent text we send to Claude for
// branch/session-name generation. Naming only needs the gist —
// dumping a 2000-char Sentry alert (URLs, stack traces, emoji codes)
// just slows the model down and produces lower-quality names. 400 chars
// holds 60-80 words, enough context for any reasonable name.
const branchPromptMaxChars = 400

// truncateForBranchPrompt clamps the intent text to branchPromptMaxChars
// runes. Truncation adds a `…` so the model knows there was more.
func truncateForBranchPrompt(s string) string {
	r := []rune(s)
	if len(r) <= branchPromptMaxChars {
		return s
	}
	return string(r[:branchPromptMaxChars-1]) + "…"
}

// composeNamingIntent builds the user message for a tag-aware naming call:
// the existing-tag vocabulary (so the model reuses labels instead of
// coining synonyms) followed by the truncated intent, in the EXISTING
// TAGS / INTENT shape the tag system prompts document. Pure.
func composeNamingIntent(prompt string, existingTags []string) string {
	tags := "(none)"
	if len(existingTags) > 0 {
		tags = strings.Join(existingTags, ", ")
	}
	return "EXISTING TAGS: " + tags + "\nINTENT: " + truncateForBranchPrompt(prompt)
}

// parseNameAndTag splits GenerateName's raw output into the name (first
// non-empty line) and an optional tag (second non-empty line). The
// tag-aware contract puts the name on line 1 and a tag slug — or an empty
// line for "no tag" — on line 2. Callers on the single-line contract just
// ignore the second value. Pure.
func parseNameAndTag(raw string) (name, tag string) {
	var lines []string
	for _, l := range strings.Split(raw, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			lines = append(lines, s)
		}
	}
	if len(lines) > 0 {
		name = lines[0]
	}
	if len(lines) > 1 {
		tag = lines[1]
	}
	return name, tag
}

// autoTagMaxLen caps an AI-proposed tag. Tags are single-token grouping
// labels; anything longer is the model leaking a description into the tag.
const autoTagMaxLen = 24

// autoTagPlaceholders are literal words a model emits instead of an empty
// line when it means "no tag". Any of these normalizes to no tag.
var autoTagPlaceholders = map[string]bool{
	"none": true, "empty": true, "null": true, "nil": true,
	"na": true, "n-a": true, "no": true, "no-tag": true,
}

var autoTagSlugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitizeAutoTag turns a model's raw tag line into a safe grouping slug,
// or "" when nothing usable remains. Soft by design: any doubt yields ""
// (no tag) — auto-tagging never blocks creation and M-t always overrides.
// Lowercased, non-[a-z0-9-] collapsed to hyphens, edges trimmed,
// length-capped, known placeholders dropped. Pure.
func sanitizeAutoTag(raw string) string {
	t := strings.ToLower(strings.TrimSpace(raw))
	t = strings.TrimPrefix(t, "#")
	t = autoTagSlugRe.ReplaceAllString(t, "-")
	t = strings.Trim(t, "-")
	if len(t) > autoTagMaxLen {
		t = strings.Trim(t[:autoTagMaxLen], "-")
	}
	if autoTagPlaceholders[t] {
		return ""
	}
	return t
}

// workspaceAutoTagEnabled reports whether creation should ask the AI for a
// grouping tag. The ATELIER_AUTO_TAG env var overrides ("0"/"false"
// disables) so tests and one-off runs can flip it without touching config;
// otherwise it reads [workspaces] auto_tag (default true).
func workspaceAutoTagEnabled() bool {
	if v := os.Getenv("ATELIER_AUTO_TAG"); v != "" {
		return v != "0" && !strings.EqualFold(v, "false")
	}
	cfg, err := LoadConfig()
	if err != nil {
		return true
	}
	return cfg.AutoTag
}

const branchNamingSysPrompt = `You are a deterministic naming engine. You DO NOT have a conversation; you EMIT a single value.

Task: given an INTENT line, output exactly one git branch name in conventional-commits form.

Output contract — REQUIRED:
- EXACTLY ONE LINE.
- Format: <type>/<short-kebab-description>
- Allowed types: feat, fix, chore, refactor, docs, test, perf, style.
- Description: 2-5 words, kebab-case, lowercase, characters in [a-z0-9-] only.
- NO quotes, NO backticks, NO code blocks, NO leading/trailing whitespace.
- NO commentary, NO clarifying questions, NO acknowledgments, NO "here is", NO "I would suggest".
- If the intent is ambiguous, vague, or you would otherwise want to ask a follow-up, DO NOT ASK. Instead pick the best-effort name from whatever signal exists.

Opaque-input rule — REQUIRED:
- The INTENT is the ONLY information available to you. Treat its
  contents as OPAQUE TEXT. Do not attempt to look up, fetch, resolve,
  or interpret anything beyond the literal words.
- URLs (e.g. https://github.com/.../issues/123, https://sentry.io/...,
  Slack message links, Linear/JIRA ticket URLs) are LITERAL STRINGS,
  NOT references to resolve. Extract a name from the URL's path
  segments, the words around it, or the surrounding context — never
  imagine you can read the linked content.
- Ticket IDs (PLA-123, JIRA-456, #789) are LITERAL TOKENS. Extract a
  name from words around them, NOT from imagined ticket content.
- You have no tools, no network, no file access. Guess from the text
  alone. If guessing is impossible, fall back to "chore/wip".

Type-selection heuristics (apply in order, first match wins):
- "fix"/"bug"/"crash"/"broken"/"error"/"sentry"/"alert" anywhere → type = fix
- "test"/"spec"/"e2e" → test
- "doc"/"readme"/"comment"/"clarify" → docs
- "refactor"/"rename"/"extract"/"cleanup" → refactor
- "perf"/"speed"/"slow"/"optimize" → perf
- everything else → feat

Fallback: if intent is empty / unparseable / all-symbolic / pure URL with no readable context, emit "chore/wip".

Examples (intent → output):
- "Sentry alert: Redis::CannotConnectError" → fix/redis-cannot-connect
- "add dark mode toggle" → feat/dark-mode-toggle
- "?????" → chore/wip
- "I'm not sure what this should do" → chore/wip
- "Refactor the auth middleware to support OIDC" → refactor/auth-middleware-oidc
- "Bug in https://github.com/foo/bar/issues/4321 — billing webhook 500s" → fix/billing-webhook-500s
- "fix PLA-364 follow-up: clinic worker SA binding" → fix/clinic-worker-sa-binding
- "https://sentry.io/organizations/x/issues/12345/" → chore/wip
- "https://sentry.io/.../issues/12345/  Redis EOFError on QuotesController" → fix/redis-eoferror-quotes

Now read the intent on the next message and emit ONE line per the contract above.`

// branchNamingWithTagSysPrompt is the tag-aware variant of
// branchNamingSysPrompt (issue #56): same branch contract on line 1, plus
// an optional grouping tag on line 2. Kept as its own prompt so the
// auto-tag=false path stays byte-identical to the single-line contract.
const branchNamingWithTagSysPrompt = `You are a deterministic naming engine. You DO NOT have a conversation; you EMIT values.

Task: given an INTENT, output a git branch name AND an optional grouping tag.

Output contract — REQUIRED:
- EXACTLY TWO LINES.
- LINE 1 — a git branch name in conventional-commits form:
  - Format: <type>/<short-kebab-description>
  - Allowed types: feat, fix, chore, refactor, docs, test, perf, style.
  - Description: 2-5 words, kebab-case, lowercase, characters in [a-z0-9-] only.
- LINE 2 — a grouping tag, OR an EMPTY line meaning "no tag":
  - Format: a single short slug, 1-2 words, kebab-case, lowercase, [a-z0-9-] only.
  - A tag GROUPS related workspaces (by client, feature area, subsystem, or project). It is NOT the branch type and NOT a restatement of the branch.
  - If the INTENT carries an "EXISTING TAGS:" list and one of those tags fits the work semantically, REUSE it VERBATIM — never coin a synonym for a tag that already exists.
  - Only if none of the existing tags fit, propose a new short slug.
  - If no meaningful grouping is evident, leave LINE 2 EMPTY.
- NO quotes, NO backticks, NO code blocks, NO commentary, NO extra lines, NO "here is".
- If the intent is ambiguous or vague, DO NOT ASK — best-effort the branch on line 1; leave line 2 empty when unsure.

Opaque-input rule — REQUIRED:
- The INTENT is the ONLY information available. Treat its contents as OPAQUE TEXT.
- URLs, ticket IDs (PLA-123, JIRA-456, #789), and links are LITERAL STRINGS, not references to resolve. Extract names from the surrounding words, never from imagined linked content.
- You have no tools, no network, no file access. Guess from the text alone. If guessing the branch is impossible, emit "chore/wip" on line 1 and an empty line 2.

Type-selection heuristics for LINE 1 (first match wins):
- "fix"/"bug"/"crash"/"broken"/"error"/"sentry"/"alert" → fix
- "test"/"spec"/"e2e" → test
- "doc"/"readme"/"comment"/"clarify" → docs
- "refactor"/"rename"/"extract"/"cleanup" → refactor
- "perf"/"speed"/"slow"/"optimize" → perf
- everything else → feat

Examples (input → the two output lines; "∅" marks an empty second line):

EXISTING TAGS: billing, infra
INTENT: billing webhook 500s on retry
→ fix/billing-webhook-500s
→ billing

EXISTING TAGS: acme, globex
INTENT: acme onboarding — wire up their SSO
→ feat/acme-sso-onboarding
→ acme

EXISTING TAGS: (none)
INTENT: migrate the payments service to the new ledger API
→ feat/payments-ledger-migration
→ payments

EXISTING TAGS: (none)
INTENT: ?????
→ chore/wip
→ ∅

Now read the input on the next message and emit per the contract (line 1 branch, line 2 tag or empty). Do NOT print the "→" or "∅" markers — those only illustrate the examples.`

// noopStatus is the default progress callback (used by callers/tests that
// don't render a loader).
func noopStatus(string) {}

func buildClaudeNamedWorkspace(status func(string), prompt, repo, repoPath, defaultBranch string, existingTags []string, autoTag bool) (name, wtPath, tag string, err error) {
	if status == nil {
		status = noopStatus
	}
	status("Inferring branch name…")
	// The kernel owns the naming CONTRACT (system prompt + conventional-
	// commits validation below); the active AI integration only runs its
	// model to satisfy it. Auto-mode requires an AI integration.
	ai := integration.Active().AI
	if ai == nil {
		return "", "", "", fmt.Errorf("auto-mode requires an AI integration (set `[ai] provider` in config.toml)")
	}
	// When auto-tagging is on, use the two-line contract and feed the
	// existing tag vocabulary so the model reuses labels (issue #56).
	// Otherwise the single-line contract is byte-identical to before.
	sysPrompt, intent := branchNamingSysPrompt, truncateForBranchPrompt(prompt)
	if autoTag {
		sysPrompt, intent = branchNamingWithTagSysPrompt, composeNamingIntent(prompt, existingTags)
	}
	raw, err := ai.GenerateName(context.Background(), sysPrompt, intent)
	if err != nil {
		return "", "", "", err
	}
	rawName, rawTag := parseNameAndTag(raw)
	name = strings.ToLower(strings.TrimSpace(rawName))
	if !conventionalBranchRe.MatchString(name) {
		return name, "", "", fmt.Errorf("invalid name: %q", name)
	}
	// Tag is a soft suggestion: sanitize to a slug, empty on any doubt.
	// Never let a bad tag fail the build — the branch is what matters.
	if autoTag {
		tag = sanitizeAutoTag(rawTag)
	}
	if branchExists(repoPath, name) {
		// `chore/wip` names every vague/unparseable prompt, so distinct
		// tasks all collide on it — give each its own worktree
		// (chore/wip-2, -3, …) instead of reusing an unrelated wip
		// branch. A *specific* name colliding means "resume that task":
		// surface errBranchAlreadyExists so _build reuses the existing
		// workspace.
		if name == wipFallbackBranch {
			name = nextFreeBranch(repoPath, name)
		} else {
			return name, "", tag, fmt.Errorf("branch %q %w", name, errBranchAlreadyExists)
		}
	}
	wtPath = filepath.Join(workspaceWorktreeRoot(), repo, name)
	_ = os.MkdirAll(filepath.Dir(wtPath), 0o755)
	status(fmt.Sprintf("Fetching origin/%s...", defaultBranch))
	if err := runGit(repoPath, "fetch", "origin", defaultBranch); err != nil {
		return name, "", tag, fmt.Errorf("fetch: %w", err)
	}
	base, tracking := resolveWorktreeBase(repoPath, name, defaultBranch)
	if tracking {
		status(fmt.Sprintf("Tracking origin/%s...", name))
	} else {
		status("Building worktree...")
	}
	if err := runGit(repoPath, "worktree", "add", wtPath, "-b", name, base); err != nil {
		return name, "", tag, fmt.Errorf("worktree add: %w", err)
	}
	return name, wtPath, tag, nil
}

// ============================================================================
// Internal: tmux_workspace_auto_session port
// ============================================================================

var autoSessionNameRe = regexp.MustCompile(`^auto/[a-z0-9-]+$`)

const sessionNamingSysPrompt = `You are a deterministic naming engine. You DO NOT have a conversation; you EMIT a single value.

Task: given an INTENT line, output exactly one tmux session name for a multi-repo task.

Output contract — REQUIRED:
- EXACTLY ONE LINE.
- Format: auto/<short-kebab-description>
- Description: 2-5 words, kebab-case, lowercase, characters in [a-z0-9-] only.
- NO quotes, NO backticks, NO code blocks, NO leading/trailing whitespace.
- NO commentary, NO clarifying questions, NO acknowledgments, NO "here is", NO "I would suggest".
- If the intent is ambiguous, vague, or you would otherwise want to ask a follow-up, DO NOT ASK. Instead pick the best-effort name from whatever signal exists.

Opaque-input rule — REQUIRED:
- The INTENT is the ONLY information available. Treat URLs, ticket
  IDs, and any other "lookup-able" tokens as LITERAL OPAQUE STRINGS.
  Do not attempt to resolve or interpret beyond the literal words.
- You have no tools, no network, no file access. Guess from the text
  alone. If guessing is impossible, fall back to "auto/wip".

Fallback: if intent is empty / unparseable / all-symbolic / pure URL with no readable context, emit "auto/wip".

Examples (intent → output):
- "audit observability stack across all repos" → auto/audit-observability-stack
- "?????" → auto/wip
- "I'm not sure what this should do" → auto/wip
- "https://github.com/foo/bar/issues/123 multi-repo billing rewire" → auto/multi-repo-billing-rewire

Now read the intent on the next message and emit ONE line per the contract above.`

// sessionNamingWithTagSysPrompt is the tag-aware variant of
// sessionNamingSysPrompt (issue #56): the auto/ session name on line 1,
// plus an optional grouping tag on line 2. Contains "auto/" so the mock
// adapter still emits auto-prefixed names.
const sessionNamingWithTagSysPrompt = `You are a deterministic naming engine. You DO NOT have a conversation; you EMIT values.

Task: given an INTENT, output a tmux session name for a multi-repo task AND an optional grouping tag.

Output contract — REQUIRED:
- EXACTLY TWO LINES.
- LINE 1 — a tmux session name:
  - Format: auto/<short-kebab-description>
  - Description: 2-5 words, kebab-case, lowercase, characters in [a-z0-9-] only.
- LINE 2 — a grouping tag, OR an EMPTY line meaning "no tag":
  - Format: a single short slug, 1-2 words, kebab-case, lowercase, [a-z0-9-] only.
  - A tag GROUPS related workspaces (by client, feature area, subsystem, or project). It is NOT a restatement of the session name.
  - If the INTENT carries an "EXISTING TAGS:" list and one fits semantically, REUSE it VERBATIM — never coin a synonym for a tag that already exists.
  - Only if none fit, propose a new short slug. If no grouping is evident, leave LINE 2 EMPTY.
- NO quotes, NO backticks, NO code blocks, NO commentary, NO extra lines, NO "here is".
- If the intent is ambiguous or vague, DO NOT ASK — best-effort line 1; leave line 2 empty when unsure.

Opaque-input rule — REQUIRED:
- The INTENT is the ONLY information available. Treat URLs, ticket IDs, and any "lookup-able" tokens as LITERAL OPAQUE STRINGS. Do not resolve or interpret beyond the literal words.
- You have no tools, no network, no file access. Guess from the text alone. If guessing is impossible, emit "auto/wip" on line 1 and an empty line 2.

Examples (input → the two output lines; "∅" marks an empty second line):

EXISTING TAGS: observability
INTENT: audit observability stack across all repos
→ auto/audit-observability-stack
→ observability

EXISTING TAGS: (none)
INTENT: ?????
→ auto/wip
→ ∅

Now read the input on the next message and emit per the contract (line 1 session name, line 2 tag or empty). Do NOT print the "→" or "∅" markers — those only illustrate the examples.`

func runAutoSession(initialPrompt string) error {
	prompt := strings.TrimSpace(initialPrompt)
	if prompt == "" {
		outcome, err := tui.Run(tui.NewPrompt(tui.CreatorTheme(), tui.PromptConfig{
			Title:       " New Multi-Repo Workspace ",
			Glyph:       "製 ",
			Placeholder: "describe the task — the AI names the session",
			Header:      "multi-repo (AI-named) session",
		}))
		if err != nil {
			return err
		}
		prompt = strings.TrimSpace(outcome.Query)
		if prompt == "" {
			return nil
		}
	}
	return buildAutoSession(prompt)
}

// runMultiRepoPrompt is the M-n auto-mode prompt: like runAutoSession's
// prompt, but with an M-m toggle back to the repo list. Returns toggledBack
// when the user pressed M-m (so the caller reopens the list); otherwise it
// builds the session and returns the build error (or ErrCancelled on Esc).
func runMultiRepoPrompt() (toggledBack bool, err error) {
	outcome, err := tui.Run(tui.NewPrompt(tui.CreatorTheme(), tui.PromptConfig{
		Title:       " New Multi-Repo Workspace ",
		Glyph:       "製 ",
		Placeholder: "describe the task — the AI names the session",
		Header:      "multi-repo (AI-named) session · M-m to pick a single repo",
		Actions:     []tui.KeyAction{tui.Action("mode:repo", "alt+m", "M-m", "pick repo")},
	}))
	if err != nil {
		return false, err
	}
	if outcome.Key == "mode:repo" {
		return true, nil
	}
	p := strings.TrimSpace(outcome.Query)
	if p == "" {
		return false, tui.ErrCancelled
	}
	return false, buildAutoSession(p)
}

// buildAutoSession creates an AI-named multi-repo session for the given task
// prompt: ask the AI for a session name (+ optional tag), create the session,
// stamp its metadata, then land the outer client (queuing the agent popup).
func buildAutoSession(prompt string) error {
	base := workspaceMultiRepoRoot()
	_ = os.MkdirAll(base, 0o755)
	autoTag := workspaceAutoTagEnabled()
	h := tmuxhost.New("")
	var existingTags []string
	if autoTag {
		existingTags = collectTags(h)
	}

	var name, tag string
	var alreadyExists bool
	if err := tui.Loader("Building workspace…", func(status func(string)) error {
		status("Asking the AI for a session name…")
		// Kernel owns the naming contract (system prompt + validation); the
		// active AI integration runs its model. Auto-mode needs one.
		ai := integration.Active().AI
		if ai == nil {
			return fmt.Errorf("auto-mode requires an AI integration (set `[ai] provider` in config.toml)")
		}
		sysPrompt, intent := sessionNamingSysPrompt, truncateForBranchPrompt(prompt)
		if autoTag {
			sysPrompt, intent = sessionNamingWithTagSysPrompt, composeNamingIntent(prompt, existingTags)
		}
		raw, e := ai.GenerateName(context.Background(), sysPrompt, intent)
		if e != nil {
			return e
		}
		rawName, rawTag := parseNameAndTag(raw)
		name = strings.ToLower(strings.TrimSpace(rawName))
		if !autoSessionNameRe.MatchString(name) {
			return fmt.Errorf("invalid name: %q", name)
		}
		if autoTag {
			tag = sanitizeAutoTag(rawTag)
		}
		if has, _ := h.HasSession(name); has {
			alreadyExists = true
			return nil
		}
		status("Stamping tmux options…")
		if _, err := h.Run("new-session", "-d", "-s", name, "-c", base); err != nil {
			return err
		}
		_, _ = h.Run("set-option", "-w", "-t", "="+name+":1",
			statestore.MetadataKeyToOptionName("ai.prompt"), prompt)
		_, _ = h.Run("set-option", "-w", "-t", "="+name+":1",
			statestore.MetadataKeyToOptionName("ai.workspace_kind"), "multi-repo")
		if tag != "" {
			_, _ = h.Run("set-option", "-w", "-t", "="+name+":1",
				statestore.MetadataKeyToOptionName(workspace.TagMetadataKey), tag)
		}
		// Default window 1 of an auto-session is unnamed — register it under
		// its tmux-default name "1" so statestore restore can find it back.
		defaultWinName, _ := h.DisplayMessageAt("="+name+":1", "#{window_name}")
		defaultWinName = strings.TrimSpace(defaultWinName)
		if defaultWinName == "" {
			defaultWinName = "1"
		}
		meta := map[string]string{"ai.prompt": prompt, "ai.workspace_kind": "multi-repo"}
		if tag != "" {
			meta[workspace.TagMetadataKey] = tag
		}
		workspace.RegisterCreatedWorkspace(workspace.NewWorkspaceInfo{
			Session: name, Kind: "multi-repo", WindowName: defaultWinName, Cwd: base, Metadata: meta,
		})
		return nil
	}); err != nil {
		return err
	}

	if alreadyExists {
		return workspace.LandOuter(h, "="+name, "="+name+":1")
	}

	// Queue the agent popup BEFORE LandOuter (see runWorkspacePrompt for the
	// race rationale). Pin TMUX_PARENT_* so the agent resolves to the NEW
	// session/window and starts in base.
	newSid, _ := h.DisplayMessageAt("="+name+":1", "#{session_id}")
	newWid, _ := h.DisplayMessageAt("="+name+":1", "#{window_id}")
	sidNum := strings.TrimPrefix(strings.TrimSpace(newSid), "$")
	widNum := strings.TrimPrefix(strings.TrimSpace(newWid), "@")
	popupCmd := fmt.Sprintf(
		"sleep 0.15 && tmux display-popup %s -e TMUX_PARENT_SESSION_ID=%s -e TMUX_PARENT_WINDOW_ID=%s -e TMUX_PARENT_PANE_PWD=%q -E '%s'",
		initgen.PopupOptions(manifest.StyleFull, "Claude Code", false),
		sidNum, widNum, base,
		dispatch.CoreCmd("ai", "open"))
	if !agentAutoOpenSkipped() {
		_, _ = h.Run("run-shell", "-b", popupCmd)
	}
	return workspace.LandOuter(h, "="+name, "="+name+":1")
}

// ============================================================================
// Helpers
// ============================================================================

func workspaceCodeRoot() string {
	home, _ := os.UserHomeDir()
	if v := os.Getenv("ATELIER_CODE_ROOT"); v != "" {
		return v
	}
	return filepath.Join(home, "code", "github")
}

func workspaceWorktreeRoot() string {
	home, _ := os.UserHomeDir()
	if v := os.Getenv("ATELIER_WORKTREE_ROOT"); v != "" {
		return v
	}
	return filepath.Join(home, "code", ".worktrees", "github")
}

func workspaceMultiRepoRoot() string {
	home, _ := os.UserHomeDir()
	if v := os.Getenv("ATELIER_MULTI_REPO_ROOT"); v != "" {
		return v
	}
	return filepath.Join(home, "code")
}

func splitWorktreePath(p, root string) (repoSlug, branch string) {
	rel := strings.TrimPrefix(p, root)
	rel = strings.TrimPrefix(rel, "/")
	parts := strings.Split(rel, "/")
	if len(parts) >= 3 {
		return parts[0] + "/" + parts[1], parts[2]
	}
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return rel, ""
}

type wt struct {
	repo, branch, path string
	// softClosedAt is the mtime of <path>/.atelier-soft-closed when
	// present (set by M-s M-x soft-close). Zero time means the
	// worktree is "untouched" — never soft-closed (or already
	// recovered via M-r). The M-r picker uses this as the primary
	// sort key so recently-closed worktrees rank at the top.
	softClosedAt time.Time
}

// listWorktrees walks the worktree root and returns every dir that has
// a `.git` entry (file or directory) — that's the standard "this dir is
// a git checkout" signal.
//
// Layout convention is `<root>/<owner>/<repo>/<branch>` where `<branch>`
// can itself contain slashes (e.g. `feat/add-foo`), so we can't just
// scan three levels deep. We walk until we hit `.git`, then derive
// `repo = <owner>/<repo>` and `branch = <rest>`. For non-github-style
// roots (no owner level) the same logic falls out by counting components.
func listWorktrees(root string) ([]wt, error) {
	var out []wt
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Stat error on a child → skip the subtree silently; one
			// permission error on an unrelated dir shouldn't fail the
			// whole picker.
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		if path == root {
			return nil
		}
		if _, statErr := os.Stat(filepath.Join(path, ".git")); statErr != nil {
			return nil
		}
		// path looks like `<root>/<owner>/<repo>/<branch parts...>`.
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return filepath.SkipDir
		}
		parts := strings.Split(rel, string(filepath.Separator))
		var repo, branch string
		switch {
		case len(parts) >= 3:
			// github-style: owner/repo/branch...
			repo = parts[0] + "/" + parts[1]
			branch = strings.Join(parts[2:], "/")
		case len(parts) == 2:
			// flat: repo/branch
			repo = parts[0]
			branch = parts[1]
		default:
			return filepath.SkipDir
		}
		out = append(out, wt{
			repo:         repo,
			branch:       branch,
			path:         path,
			softClosedAt: readSoftClosedMarker(path),
		})
		// Don't descend further: nested .git inside a worktree (e.g.
		// vendored submodules) shouldn't show up as separate entries.
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		// Recently soft-closed entries float to the top — newest first.
		// This makes "I just M-x'd that, give it back" a single key
		// press (Enter) in M-r without scanning the whole list.
		iSC, jSC := !out[i].softClosedAt.IsZero(), !out[j].softClosedAt.IsZero()
		if iSC != jSC {
			return iSC
		}
		if iSC && jSC {
			return out[i].softClosedAt.After(out[j].softClosedAt)
		}
		// Both untouched → alphabetical by repo + branch.
		if out[i].repo != out[j].repo {
			return out[i].repo < out[j].repo
		}
		return out[i].branch < out[j].branch
	})
	return out, nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)
	debuglog.LogGitCmd(dir, args, errBuf.Bytes(), err, dur)
	perf.Add("git", dur)
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

// runGitCtx is runGit under a caller-supplied deadline. A background refresh
// tick runs `git fetch` synchronously in-process; on a flaky network that
// fetch can hang indefinitely, wedging the loop (and, before the loop, leaving
// the freshness icon silently empty). The context kills the git process at the
// deadline and returns a clear timeout error so the caller stamps a pull-error
// instead of blocking.
func runGitCtx(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)
	debuglog.LogGitCmd(dir, args, errBuf.Bytes(), err, dur)
	perf.Add("git", dur)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("git %s: timed out (network hang?)", strings.Join(args, " "))
		}
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func runGitQuiet(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	start := time.Now()
	out, err := cmd.Output()
	dur := time.Since(start)
	debuglog.LogGitCmd(dir, args, out, err, dur)
	perf.Add("git", dur)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func branchExists(repoPath, branch string) bool {
	return runGitQuiet(repoPath, "show-ref", "--verify", "refs/heads/"+branch) != ""
}

// wipFallbackBranch is the deterministic name the branch-naming prompt
// emits for vague/unparseable prompts. It's the one name that legitimately
// collides across unrelated tasks, so it gets numeric disambiguation
// rather than reuse — see buildClaudeNamedWorkspace.
const wipFallbackBranch = "chore/wip"

// nextFreeBranch returns the first name of the form base, base-2, base-3,
// … that has no existing branch in the repo.
func nextFreeBranch(repoPath, base string) string {
	if !branchExists(repoPath, base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !branchExists(repoPath, candidate) {
			return candidate
		}
	}
}

// errBranchAlreadyExists is returned by buildClaudeNamedWorkspace when the
// Claude-generated branch name collides with an existing branch. The
// _build flow catches it (errors.Is) to reuse the existing workspace
// instead of dumping the user out of the creator.
var errBranchAlreadyExists = errors.New("already exists")

// reuseExistingWorkspace handles the "requested branch/window already
// exists" case shared by the manual-name and Claude-named build flows.
// It never re-runs `git worktree add`. Returns handled=true with the
// target window (and its worktree path) when the name maps to an existing
// atelier window or an existing git worktree — the caller should land on
// newWid rather than error out. Returns handled=false when the branch
// exists but isn't a worktree (not reusable) or doesn't exist at all.
func reuseExistingWorkspace(h *tmuxhost.Client, session, repoPath, name, defaultBranch string) (wtPath, newWid string, handled bool, err error) {
	// An atelier window with this name already exists → reuse it directly.
	if has, _ := h.HasSession(session); has {
		// list-windows with #{window_id}\t#W so we target the existing
		// window by @ID instead of by name (which silently fails when the
		// name contains `/`).
		out, _ := h.Run("list-windows", "-t", "="+session, "-F", "#{window_id}\t#W")
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				continue
			}
			if parts[1] == name {
				return worktreePathForBranch(repoPath, name), parts[0], true, nil
			}
		}
	}
	// The branch exists as a worktree but has no window yet → stamp one.
	if branchExists(repoPath, name) {
		wt := worktreePathForBranch(repoPath, name)
		if wt == "" {
			return "", "", false, nil // branch exists but isn't a worktree
		}
		created, cerr := workspace.EnsureSession(h, session, repoPath, defaultBranch)
		if cerr != nil {
			return "", "", false, cerr
		}
		spec := workspace.WorktreeWindowSpec{
			Session:    session,
			WtPath:     wt,
			WindowName: name,
		}
		if created {
			spec.KillDefaultBranch = defaultBranch
		}
		wid, cerr := workspace.CreateWorktreeWindow(h, spec)
		if cerr != nil {
			return "", "", false, cerr
		}
		return wt, wid, true, nil
	}
	return "", "", false, nil
}

// remoteBranchExists checks whether origin has a branch with this name.
// Authoritative — queries the remote (not the local refs/remotes/ cache),
// so an out-of-date local fetch doesn't hide a branch that origin
// actually has. Empty output → branch absent OR network/auth failure;
// callers fall back to the default-branch path in either case.
func remoteBranchExists(repoPath, branch string) bool {
	return runGitQuiet(repoPath, "ls-remote", "--heads", "origin", branch) != ""
}

// resolveWorktreeBase chooses the git ref to base a new worktree on.
// If origin has a branch with the same name as the worktree we're about
// to create, fetch it and base the new tracking branch on origin/<name>
// — otherwise the worktree would be empty (a fresh branch off main) and
// the user's existing remote work would be invisible. Falls back to
// origin/<defaultBranch> when the remote check fails or returns nothing.
//
// Returns the base ref string ("origin/<name>" or "origin/<defaultBranch>")
// and a bool indicating whether the remote-tracking path was taken.
func resolveWorktreeBase(repoPath, name, defaultBranch string) (string, bool) {
	if !remoteBranchExists(repoPath, name) {
		return "origin/" + defaultBranch, false
	}
	if err := runGit(repoPath, "fetch", "origin", name); err != nil {
		return "origin/" + defaultBranch, false
	}
	return "origin/" + name, true
}

func worktreePathForBranch(repoPath, branch string) string {
	out := runGitQuiet(repoPath, "worktree", "list", "--porcelain")
	var curPath string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			curPath = strings.TrimPrefix(line, "worktree ")
		}
		if line == "branch refs/heads/"+branch {
			return curPath
		}
	}
	return ""
}

// removeWorktree tries the clean `git worktree remove --force` first
// (which updates the main repo's worktree registration). When the main
// repo no longer exists on disk — common in the recover picker, since
// it shows orphan worktrees by definition — git can't chdir into
// repoPath and the call fails. Fall back to a direct os.RemoveAll on
// the worktree directory: the worktree's `.git` file is a pointer to
// a now-defunct gitdir, so nuking the directory is safe.
func removeWorktree(repoPath, wtPath string) error {
	if _, err := os.Stat(repoPath); err == nil {
		if err := runGit(repoPath, "worktree", "remove", "--force", wtPath); err == nil {
			return nil
		}
		// Git failed despite the main repo existing — fall through to
		// the direct removal. Worst case: a stale `worktrees/<name>`
		// entry under the main repo's .git/worktrees, which `git
		// worktree prune` cleans up.
	}
	if err := os.RemoveAll(wtPath); err != nil {
		return fmt.Errorf("worktree remove (fallback rm -rf): %w", err)
	}
	return nil
}

// ensureDefaultBranchWindow makes sure the given session has a window
// named after the default branch. If the window already exists, no-op.
// If absent (e.g. session was created with only worktree windows, or the
// default-branch window was deleted), a new window is created at repoPath
// with that name. select-window can then safely target =session:branch.
func ensureDefaultBranchWindow(h *tmuxhost.Client, session, repoPath, defaultBranch string) error {
	out, err := h.Run("list-windows", "-t", "="+session, "-F", "#W")
	if err != nil {
		return err
	}
	for _, w := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if w == defaultBranch {
			return nil
		}
	}
	last := lastWindowIndex(session)
	next := last + 1
	_, err = h.Run("new-window", "-t", fmt.Sprintf("%s:%d", session, next),
		"-c", repoPath, "-n", defaultBranch)
	return err
}

func lastWindowIndex(session string) int {
	h := tmuxhost.New("")
	out, err := h.Run("list-windows", "-t", "="+session, "-F", "#I")
	if err != nil {
		return 0
	}
	max := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		n := 0
		_, _ = fmt.Sscanf(line, "%d", &n)
		if n > max {
			max = n
		}
	}
	return max
}

func getSessionRepoPath(h *tmuxhost.Client, session string) (string, error) {
	out, _ := h.Run("show-option", "-t", session, "-qv", workspace.OptRepoPath)
	return strings.TrimSpace(string(out)), nil
}
