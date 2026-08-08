package workspaces

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/dispatch"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// The background refresh loop is the "continuous, not just on-event" half of
// badge data (issue #17). Rendering was already continuous (status-interval 3
// re-renders the cached window options); only the DATA refresh was event-tied
// (bg-pull on create/restore/navigate, forge on picker-open). This one
// long-lived process keeps that data current on a heartbeat, and — since it
// already re-reads the whole graph each tick — folds in a level-triggered
// kernel reconcile so misrouted/phantom attention and orphan popups self-heal
// continuously instead of only on a manual `atelier reconcile --fix`.
//
// Two rules make it safe:
//
//   - TTL is the throttle, the tick is the heartbeat. Each window re-queries
//     only when its own timestamp is older than its TTL, so most ticks stamp
//     nothing (forge ~1min; freshness longer — a git fetch is heavier).
//   - Sweeps run SYNCHRONOUSLY in-process. No detached child per tick — that
//     is precisely what once leaked ~388 `_bg-pull` git procs and exhausted
//     the machine. One process, bounded git ops (see bgPullGitTimeout).
const (
	// defaultRefreshInterval is the heartbeat. Well under every TTL so the
	// throttle, not the tick, decides what actually refreshes.
	defaultRefreshInterval = 45 * time.Second
	// freshnessRefreshTTL bounds how often a window re-fetches for freshness.
	// Longer than the forge TTL because `git fetch` is the heavier op.
	freshnessRefreshTTL = 3 * time.Minute
	// maxFetchesPerTick caps distinct-repo git fetches per tick so a burst of
	// simultaneously-stale windows spreads across ticks instead of running
	// dozens of sequential 30s-bounded fetches in one sweep.
	maxFetchesPerTick = 8
	// optRefreshLoopPid holds the pid of the loop that owns this server, so a
	// re-sourced config (or a second launcher) doesn't stack a second daemon.
	optRefreshLoopPid = "@atelier_refresh_loop_pid"
	// optMSPickerPort holds the fzf --listen port of an open M-s picker (set by
	// the picker's start bind, cleared on close). When set, the loop pushes a
	// live reload after any tick that changed picker-visible state.
	optMSPickerPort = "@atelier_ms_fzf_port"
)

// RefreshLoopCommand is the hidden `_refresh-loop` daemon started once from the
// generated tmux config via `run-shell -b`. It ticks forever, refreshing
// freshness + forge badges (TTL-throttled) and reconciling kernel state, until
// the tmux server it bound to (by pid) goes away or is replaced — see
// loopShouldContinue; it does NOT rely on being signaled on server exit.
func RefreshLoopCommand() *cobra.Command {
	var socket string
	var interval time.Duration
	c := &cobra.Command{
		Use:    "_refresh-loop",
		Short:  "internal: continuous TTL-throttled freshness + forge refresh, plus kernel self-heal",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRefreshLoop(tmuxhost.New(socket), interval)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (default: ATELIER_TMUX_SOCKET)")
	c.Flags().DurationVar(&interval, "interval", defaultRefreshInterval, "tick interval")
	return c
}

func runRefreshLoop(h *tmuxhost.Client, interval time.Duration) error {
	// Never run under e2e sockets: the loop would fire real git fetches +
	// reconcile --fix against test fixtures and leave background procs racing
	// t.TempDir cleanup (same discipline as SpawnBgPull / SpawnForgeRefresh).
	if strings.HasPrefix(os.Getenv("ATELIER_TMUX_SOCKET"), "atelier-test-") {
		debuglog.Logf("workspaces._refresh-loop: SKIP (test socket)")
		return nil
	}
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	if !acquireRefreshLoopLock(h) {
		debuglog.Logf("workspaces._refresh-loop: another loop already owns this server — exiting")
		return nil
	}
	// Bind our lifetime to THIS tmux server instance by its pid. run-shell -b
	// children are NOT reliably SIGHUP'd on server exit — they're orphaned to
	// pid 1 — and the socket can be reused by a brand-new server, so an orphan
	// could otherwise run forever and double up with the new server's own loop.
	// The server pid changes across restarts, so comparing it each tick catches
	// both "server gone" and "server replaced".
	serverPid := currentServerPid(h)
	if serverPid == "" {
		return nil
	}
	debuglog.Logf("workspaces._refresh-loop: started pid=%d server=%s interval=%s", os.Getpid(), serverPid, interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Wait one interval before the first tick: restore's warmup pass already
	// fired bg-pull for live windows at startup, so an immediate tick would be
	// redundant work (and mostly TTL-skipped anyway).
	var lastPickerSig string
	for {
		<-ticker.C
		if !loopShouldContinue(h, serverPid) {
			debuglog.Logf("workspaces._refresh-loop: exiting (server gone/replaced or ownership lost)")
			return nil
		}
		refreshTick(h, integration.Active().Forge, time.Now())
		// After the sweeps, push a live reload to an open M-s picker if the
		// picker-visible state changed this tick (the point of the observer:
		// see new attention/recap without reopening M-s).
		lastPickerSig = maybeReloadPicker(h, lastPickerSig)
	}
}

// maybeReloadPicker pushes a live reload to an open M-s picker when the
// picker-visible state changed since the last tick, and returns the new
// signature. No-op when no picker is open (its port global is unset), so this
// costs one list-windows call per tick only while M-s is actually up.
func maybeReloadPicker(h *tmuxhost.Client, last string) string {
	port, _ := h.ShowGlobalOption(optMSPickerPort)
	port = strings.TrimSpace(port)
	if port == "" {
		return last
	}
	sig := pickerSignature(h)
	if sig == "" || sig == last {
		return sig
	}
	postFzfReload(port)
	debuglog.Logf("workspaces._refresh-loop: pushed M-s reload (port %s)", port)
	return sig
}

// pickerSigFields are the window options the M-s picker renders. The signature
// deliberately EXCLUDES @attention_recap_ts — a recap age tick alone shouldn't
// disturb the user's cursor; only a change to visible content (attention,
// status, recap text, forge badge, tag, or the window set) warrants a reload.
var pickerSigFields = strings.Join([]string{
	"#{window_id}", "#{session_name}", "#{window_name}",
	"#{@needs_attention}", "#{@agent_status}", "#{@attention_recap}",
	"#{@forge_state}", "#{@workspace_tag}", "#{@repo_path}", "#{@ai_workspace_kind}",
}, "\x1f")

// pickerSignature returns a change-detection signature over every window's
// picker-visible fields. Context-independent (no current-client marker), so it
// compares cleanly across ticks. "" on error.
func pickerSignature(h *tmuxhost.Client) string {
	out, err := h.Run("list-windows", "-a", "-F", pickerSigFields)
	if err != nil {
		return ""
	}
	return string(out)
}

// postFzfReload POSTs a reload action to the open picker's fzf --listen server,
// re-running the same _session-list generator the picker's own binds use. Local
// HTTP, short timeout; a dead port (picker already closed) fails silently.
func postFzfReload(port string) {
	body := "reload(" + dispatch.ToolCmd("workspaces", "_session-list") + ")"
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Post("http://127.0.0.1:"+port, "text/plain", strings.NewReader(body))
	if err != nil {
		debuglog.LogErr("workspaces._refresh-loop: fzf reload post", err)
		return
	}
	_ = resp.Body.Close()
}

// loopShouldContinue is the per-tick liveness + ownership gate. It exits the
// loop when the tmux server is gone or has been replaced (pid mismatch — the
// socket-reuse orphan case), or when another loop has taken ownership of the
// singleton lock (self-eviction, closing the TOCTOU acquire race to one tick).
func loopShouldContinue(h *tmuxhost.Client, serverPid string) bool {
	if cur := currentServerPid(h); cur == "" || cur != serverPid {
		return false
	}
	owner, _ := h.ShowGlobalOption(optRefreshLoopPid)
	return strings.TrimSpace(owner) == strconv.Itoa(os.Getpid())
}

// currentServerPid returns the tmux server pid, or "" if no server responds.
func currentServerPid(h *tmuxhost.Client) string {
	out, err := h.DisplayMessage("#{pid}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// refreshTick runs one full pass: freshness sweep, forge sweep (if a forge
// adapter is active), then a level-triggered kernel reconcile. Each stage is
// best-effort and independent — a failure in one never skips the others.
func refreshTick(h *tmuxhost.Client, forge integration.ForgeIntegration, now time.Time) {
	refreshFreshnessAll(h, now)

	if forge != nil {
		if err := refreshForgeBadges(h, forge, now, forgeLoopRefreshTTL); err != nil {
			debuglog.LogErr("workspaces._refresh-loop: forge", err)
		}
	}

	// Recap + attention: the observer half of #51. Per active workspace, the AI
	// adapter re-reads the agent's transcript and re-derives the one-line recap
	// AND whether the agent is blocked waiting on the user (→ attention). It
	// self-throttles on the transcript mtime, so this is a no-op on any
	// workspace whose agent hasn't done anything since the last pass.
	if ai := integration.Active().AI; ai != nil {
		refreshRecapsAll(h, ai)
	}

	// Self-heal: repair the LOOP-SAFE kernel invariants on every heartbeat —
	// orphan popups, stray popup attention, a stale/launcher/popup outer
	// pointer. This is #81's level-triggered reconcile on #17's tick. It
	// deliberately does NOT run the racy repairs (disarming a client-detached
	// hook, clearing the outer-client hint): on a bare 45s snapshot those can
	// false-positive against a legitimately in-flight popup open and break it.
	// ReconcileLoop enforces that subset; manual `atelier reconcile --fix` (a
	// human who knows no popup is mid-open) still repairs everything.
	results, err := state.ReconcileLoop(h)
	if err != nil {
		debuglog.LogErr("workspaces._refresh-loop: reconcile", err)
		return
	}
	for _, r := range results {
		if r.Repaired {
			debuglog.Logf("workspaces._refresh-loop: reconcile repaired %s (%s)", r.Code, r.Subject)
		}
	}
}

// refreshFreshnessAll sweeps every git workspace window whose freshness
// timestamp is older than freshnessRefreshTTL and refreshes it SYNCHRONOUSLY
// (never a detached child — that per-event spawn is what leaked ~388 git
// procs). Two bounds keep the tax flat:
//
//   - Per-repo fetch dedup. @repo_path is session-scoped, so a repo with N
//     worktree windows would otherwise run N identical `git fetch` into the
//     same bare repo. We fetch each repo's origin ONCE per tick, then measure
//     ahead/behind per window (a cheap local op).
//   - Per-tick fetch cap. Windows created together expire together; without a
//     cap one tick could run dozens of sequential 30s-bounded fetches. Capping
//     distinct-repo fetches spreads a synchronized burst across ticks.
func refreshFreshnessAll(h *tmuxhost.Client, now time.Time) {
	format := "#{window_id}|#{" + workspace.OptRepoPath + "}|#{" + workspace.OptWorkspaceFreshnessTs + "}"
	out, err := h.Run("list-windows", "-a", "-F", format)
	if err != nil {
		debuglog.LogErr("workspaces._refresh-loop: list-windows", err)
		return
	}
	fetchOK := map[string]bool{}   // repoPath → fetched OK this tick
	fetchFail := map[string]bool{} // repoPath → fetch failed this tick
	fetches := 0
	refreshed := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "|", 3)
		if len(f) < 3 {
			continue
		}
		windowID, repoPath, tsStr := f[0], f[1], f[2]
		if repoPath == "" {
			continue // non-git workspace: no freshness
		}
		if freshnessFresh(now, tsStr) {
			continue // still within TTL — skip
		}
		branch := DefaultBranch(repoPath)
		if branch == "" {
			continue
		}
		// Fetch each repo at most once per tick, capped.
		if !fetchOK[repoPath] && !fetchFail[repoPath] {
			if fetches >= maxFetchesPerTick {
				continue // cap reached — this repo's windows refresh a later tick
			}
			fetches++
			if err := fetchOrigin(repoPath, branch); err != nil {
				debuglog.LogErr("workspaces._refresh-loop: fetch "+repoPath, err)
				fetchFail[repoPath] = true
			} else {
				fetchOK[repoPath] = true
			}
		}
		if fetchFail[repoPath] {
			// Stamp the error (which also stamps a freshness ts) so the TTL
			// throttles the retry instead of hammering a bad repo every tick.
			stampPullError(h, windowID, "fetch failed")
			continue
		}
		if err := measureFreshness(h, repoPath, branch, windowID); err != nil {
			debuglog.LogErr("workspaces._refresh-loop: measure "+windowID, err)
		}
		refreshed++
	}
	if refreshed > 0 || fetches > 0 {
		debuglog.Logf("workspaces._refresh-loop: freshness fetched=%d refreshed=%d window(s)", fetches, refreshed)
	}
}

// refreshRecapsAll re-derives recap + attention for every listable workspace
// window via the active AI adapter, passing the window's worktree (its active
// pane cwd) so the adapter can locate the agent transcript. Each RefreshRecap
// self-throttles on transcript mtime, so this sweep is a cheap stat on quiet
// workspaces and only spends a model call where the agent actually did work.
func refreshRecapsAll(h *tmuxhost.Client, ai integration.AIIntegration) {
	format := "#{window_id}|#{" + workspace.OptRepoPath + "}|#{@ai_workspace_kind}|#{pane_current_path}"
	out, err := h.Run("list-windows", "-a", "-F", format)
	if err != nil {
		debuglog.LogErr("workspaces._refresh-loop: list-windows (recap)", err)
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "|", 4)
		if len(f) < 4 {
			continue
		}
		windowID, repoPath, kind, cwd := f[0], f[1], f[2], f[3]
		if cwd == "" || !workspace.Listable(repoPath, kind) {
			continue
		}
		if err := ai.RefreshRecap(h, windowID, cwd); err != nil {
			debuglog.LogErr("workspaces._refresh-loop: recap "+windowID, err)
		}
	}
}

// freshnessFresh reports whether an @workspace_freshness_ts value is within
// freshnessRefreshTTL of now. Empty/unparseable = stale (needs a refresh).
// Pure — mirrors forgeFresh so both throttles read identically.
func freshnessFresh(now time.Time, tsStr string) bool {
	secs, err := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
	if err != nil || secs <= 0 {
		return false
	}
	return now.Sub(time.Unix(secs, 0)) < freshnessRefreshTTL
}

// acquireRefreshLoopLock records this process as the server's refresh loop,
// returning false when a still-live loop already holds it. Best-effort: the
// pid is stashed in a global tmux option and validated with signal 0 so a
// crashed prior loop's stale pid doesn't block a fresh one.
func acquireRefreshLoopLock(h *tmuxhost.Client) bool {
	if existing, _ := h.ShowGlobalOption(optRefreshLoopPid); strings.TrimSpace(existing) != "" {
		if pid, err := strconv.Atoi(strings.TrimSpace(existing)); err == nil && processAlive(pid) {
			return false
		}
	}
	if err := h.SetGlobalOption(optRefreshLoopPid, strconv.Itoa(os.Getpid())); err != nil {
		debuglog.LogErr("workspaces._refresh-loop: claim lock", err)
	}
	return true
}

// processAlive reports whether pid names a live process (signal 0 probe).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
