package workspaces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/dispatch"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// The background refresh loop is the daemon of the drawing's three feeds
// (WS-7): it keeps PR statuses updated (the batched forge sweep), updates
// workspace summaries (the second AI call, change-gated), and re-derives each
// driver agent's recap + attention — plus a level-triggered kernel reconcile so
// misrouted/phantom attention and orphan popups self-heal. One long-lived
// process, TTL-throttled so most ticks stamp nothing.
const (
	defaultRefreshInterval = 45 * time.Second
	optRefreshLoopPid      = "@atelier_refresh_loop_pid"
	optRefreshLoopLastTick = "@atelier_refresh_loop_last_tick"
	optMSPickerPort        = "@atelier_ms_fzf_port"
)

// RefreshLoopCommand is the hidden `_refresh-loop` daemon started once from the
// generated tmux config via `run-shell -b`.
func RefreshLoopCommand() *cobra.Command {
	var socket string
	var interval time.Duration
	c := &cobra.Command{
		Use:    "_refresh-loop",
		Short:  "internal: continuous PR sweep + workspace summaries + recap + self-heal",
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
	serverPid := currentServerPid(h)
	if serverPid == "" {
		return nil
	}
	debuglog.Logf("workspaces._refresh-loop: started pid=%d server=%s interval=%s", os.Getpid(), serverPid, interval)
	stampHeartbeat(h, time.Now())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastPickerSig string
	for {
		<-ticker.C
		if !loopShouldContinue(h, serverPid) {
			debuglog.Logf("workspaces._refresh-loop: exiting (server gone/replaced or ownership lost)")
			return nil
		}
		refreshTick(h, integration.Active().Forge, integration.Active().AI, time.Now())
		stampHeartbeat(h, time.Now())
		lastPickerSig = maybeReloadPicker(h, lastPickerSig)
	}
}

// refreshTick runs one pass: PR sweep, recap sweep, workspace-summary sweep, and
// a level-triggered kernel + layout reconcile. Each stage is best-effort.
func refreshTick(h *tmuxhost.Client, forge integration.ForgeIntegration, ai integration.AIIntegration, now time.Time) {
	if forge != nil {
		if err := refreshForgePRs(h, forge, now, forgeLoopRefreshTTL); err != nil {
			debuglog.LogErr("workspaces._refresh-loop: forge", err)
		}
	}
	if ai != nil {
		refreshRecapsAll(h, ai)
		refreshSummariesAll(h, ai)
	}

	results, err := state.ReconcileLoop(h)
	if err != nil {
		debuglog.LogErr("workspaces._refresh-loop: reconcile", err)
	} else {
		for _, r := range results {
			if r.Repaired {
				debuglog.Logf("workspaces._refresh-loop: reconcile repaired %s (%s)", r.Code, r.Subject)
			}
		}
	}
	reconcileLayoutAll()
}

// refreshRecapsAll re-derives recap + attention for every driver window via the
// active AI adapter, passing the driver's cwd (the workspace root) so it can
// locate the agent transcript. RefreshRecap self-throttles on transcript mtime.
func refreshRecapsAll(h *tmuxhost.Client, ai integration.AIIntegration) {
	// @workspace_id is session-scoped; resolve listability from list-SESSIONS
	// (window-context inheritance is version-fragile — see workspace.ListableSessions).
	listable := workspace.ListableSessions(h)
	format := "#{window_id}\x1f#{session_name}\x1f#{" + workspace.OptWorkspaceDriver + "}\x1f#{pane_current_path}"
	out, err := h.Run("list-windows", "-a", "-F", format)
	if err != nil {
		debuglog.LogErr("workspaces._refresh-loop: list-windows (recap)", err)
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x1f")
		if len(f) < 4 {
			continue
		}
		windowID, session, driver, cwd := f[0], f[1], f[2], f[3]
		if cwd == "" || !listable[session] || strings.TrimSpace(driver) != "1" {
			continue
		}
		if err := ai.RefreshRecap(h, windowID, cwd); err != nil {
			debuglog.LogErr("workspaces._refresh-loop: recap "+windowID, err)
		}
	}
}

// refreshSummariesAll re-derives each workspace's rollup summary (WS-7), gated
// on a content hash of its driver recap + PR states so the model call runs only
// when an input changed. Spend guard: capped per tick.
func refreshSummariesAll(h *tmuxhost.Client, ai integration.AIIntegration) {
	st, err := statestore.Load()
	if err != nil || st == nil {
		return
	}
	// Read each driver's live recap once.
	recapBySession := driverRecaps(h)
	const maxSummariesPerTick = 4
	spent := 0
	for i := range st.Workspaces {
		if spent >= maxSummariesPerTick {
			break
		}
		ws := &st.Workspaces[i]
		recap := recapBySession[ws.SessionName]
		hash := summaryInputHash(recap, ws.PRs)
		if hash == ws.SummaryHash || (recap == "" && len(ws.PRs) == 0) {
			continue // nothing changed / nothing to summarize
		}
		prs := make([]integration.PullRequest, 0, len(ws.PRs))
		for _, p := range ws.PRs {
			prs = append(prs, fromStorePR(p))
		}
		summary, err := ai.SummarizeWorkspace(context.Background(), ws.Intent, recap, prs)
		if err != nil {
			debuglog.LogErr("workspaces._refresh-loop: summary "+ws.SessionName, err)
			continue
		}
		spent++
		session, s, hh := ws.SessionName, summary, hash
		_ = statestore.UpdateWorkspace(session, func(w *statestore.Workspace) {
			w.Summary = s
			w.SummaryHash = hh
		})
	}
}

// driverRecaps returns session → the driver window's @attention_recap.
func driverRecaps(h *tmuxhost.Client) map[string]string {
	out, err := h.Run("list-windows", "-a", "-F",
		"#{session_name}\x1f#{"+workspace.OptWorkspaceDriver+"}\x1f#{"+workspace.OptRecap+"}")
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) < 3 || strings.TrimSpace(f[1]) != "1" {
			continue
		}
		m[statestore.CanonicalSessionName(f[0])] = f[2]
	}
	return m
}

// summaryInputHash is the change-detection key: a hash of the driver recap +
// each PR's identity/state. Order-independent — the PRs are sorted by repo+number
// first, because the two writers of ws.PRs persist different orders for the same
// set (the forge sweep sorts by state-rank+number; `atelier pr register` just
// appends), and an order flip must NOT spuriously trigger the metered summary
// call. Pure.
func summaryInputHash(recap string, prs []statestore.PR) string {
	keys := make([]string, 0, len(prs))
	for _, p := range prs {
		keys = append(keys, fmt.Sprintf("%s\x1e%d\x1e%s\x1e%s\x1e%s",
			p.Repo, p.Number, p.State, p.CI, p.ReviewDecision))
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(recap + "\x1f" + strings.Join(keys, "\x1f")))
	return hex.EncodeToString(sum[:8])
}

// reconcileLayoutAll re-establishes each workspace's worktree symlink tree
// (relink missing links, GC orphans) against the statestore. Best-effort.
func reconcileLayoutAll() {
	st, err := statestore.Load()
	if err != nil || st == nil {
		return
	}
	for i := range st.Workspaces {
		ws := &st.Workspaces[i]
		if ws.Root == "" || len(ws.Worktrees) == 0 {
			continue
		}
		for _, fix := range workspace.ReconcileLayout(ws.Root, ws.Worktrees) {
			debuglog.Logf("workspaces._refresh-loop: layout %s %s (%s)", fix.Code, fix.Subject, fix.Detail)
		}
	}
}

// --- daemon lifecycle (unchanged) -------------------------------------------

func stampHeartbeat(h *tmuxhost.Client, now time.Time) {
	if err := h.SetGlobalOption(optRefreshLoopLastTick, strconv.FormatInt(now.Unix(), 10)); err != nil {
		debuglog.LogErr("workspaces._refresh-loop: heartbeat", err)
	}
}

// DaemonHealth is a liveness snapshot of the refresh loop for `atelier doctor`.
type DaemonHealth struct {
	Pid          string
	Alive        bool
	HasHeartbeat bool
	SinceTick    time.Duration
	Interval     time.Duration
}

// ReadDaemonHealth reads the loop's liveness globals. serverUp is false when no
// tmux server answers.
func ReadDaemonHealth(h *tmuxhost.Client, now time.Time) (health DaemonHealth, serverUp bool) {
	if _, err := h.DisplayMessage("#{pid}"); err != nil {
		return DaemonHealth{}, false
	}
	health.Interval = defaultRefreshInterval
	pid, _ := h.ShowGlobalOption(optRefreshLoopPid)
	health.Pid = strings.TrimSpace(pid)
	if n, err := strconv.Atoi(health.Pid); err == nil {
		health.Alive = processAlive(n)
	}
	if ts, _ := h.ShowGlobalOption(optRefreshLoopLastTick); strings.TrimSpace(ts) != "" {
		if secs, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64); err == nil {
			health.HasHeartbeat = true
			health.SinceTick = now.Sub(time.Unix(secs, 0))
		}
	}
	return health, true
}

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

// pickerSigFields are the window-scoped options the M-s picker renders, used for
// change-detection. Excludes recap TS (a mere age tick shouldn't disturb the
// cursor) and the session-scoped identity options (id/title/tag) — those rarely
// change and reading them per-window is unreliable across tmux versions.
var pickerSigFields = strings.Join([]string{
	"#{window_id}", "#{session_name}",
	"#{" + workspace.OptAttention + "}", "#{" + workspace.OptAgentStatus + "}",
	"#{" + workspace.OptRecap + "}",
}, "\x1f")

func pickerSignature(h *tmuxhost.Client) string {
	out, err := h.Run("list-windows", "-a", "-F", pickerSigFields)
	if err != nil {
		return ""
	}
	return string(out)
}

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

func loopShouldContinue(h *tmuxhost.Client, serverPid string) bool {
	if cur := currentServerPid(h); cur == "" || cur != serverPid {
		return false
	}
	owner, _ := h.ShowGlobalOption(optRefreshLoopPid)
	return strings.TrimSpace(owner) == strconv.Itoa(os.Getpid())
}

func currentServerPid(h *tmuxhost.Client) string {
	out, err := h.DisplayMessage("#{pid}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

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
