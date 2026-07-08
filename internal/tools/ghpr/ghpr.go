// Package ghpr is atelier's GitHub-PR-status badge provider. It enriches
// the M-s workspace picker with a per-workspace PR-state symbol (open /
// draft / merged / closed) rendered between the attention icon and the
// workspace name, and binds M-o to open the workspace's PR in the
// browser.
//
// It plugs in via the generic manifest Badge capability: the picker reads
// the @ghpr_badge window option and splices it verbatim, and spawns
// `_refresh` (detached, once per open) to keep badges current. This tool
// owns everything GitHub-specific; the picker core stays agnostic.
//
// Freshness follows the _bg-pull model: window options are stamped
// best-effort and re-fetched on demand (here: throttled to refreshTTL per
// window). Nothing is persisted across a tmux restart — the badge simply
// re-populates on the next picker open. No daemon, no poller.
package ghpr

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// tmux window options this tool owns. @ghpr_badge holds the pre-rendered
// (ANSI-colored) glyph the picker splices; @ghpr_ts is the unix-epoch of
// the last refresh, used only for this tool's own staleness throttle.
const (
	OptBadge = "@ghpr_badge"
	OptTs    = "@ghpr_ts"
	// OptState holds the semantic PR state (open/draft/merged/closed) so
	// the workspace picker can sort by it. Distinct from OptBadge, which
	// is the rendered glyph.
	OptState = "@ghpr_state"
)

// StateOrder is the picker sort order for PR states: open first, then
// draft, merged, closed. Declared in the manifest via Badge.SortOrder.
var StateOrder = []string{"open", "draft", "merged", "closed"}

// refreshTTL is how long a window's badge is considered fresh. The picker
// pokes `_refresh` on every open; windows fetched within this window are
// skipped so repeated M-s presses don't hammer `gh` / the GitHub API.
const refreshTTL = 5 * time.Minute

// OpenCommand wires `atelier tools ghpr open <row>`: bound to M-o in the
// workspace picker. The row is the full tab-delimited picker line; its
// first two fields are the target session + window names. We resolve the
// window's worktree and hand off to `gh pr view --web`, which resolves the
// PR for the branch and opens it in the browser itself.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open <row>",
		Short: "Open the workspace's PR in the browser (M-o)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			h := tmuxhost.New(socket)
			return runOpen(h, strings.Join(args, " "))
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// RefreshCommand wires the hidden `atelier tools ghpr _refresh`. The picker
// spawns it detached on open. It enumerates repo windows, throttles per
// window via @ghpr_ts, and stamps @ghpr_badge for those with a PR.
func RefreshCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_refresh",
		Short:  "internal: refresh per-workspace PR badges",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			return runRefresh(h, time.Now())
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// runOpen resolves the selected row to a worktree path and opens its PR.
func runOpen(h *tmuxhost.Client, row string) error {
	session, window, ok := parseRow(row)
	if !ok {
		debuglog.Logf("ghpr.open: unparseable row %q", row)
		return nil
	}
	cwd, err := h.DisplayMessageAt(session+":"+window, "#{pane_current_path}")
	if err != nil {
		debuglog.LogErr("ghpr.open display-message", err)
		return nil
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	debuglog.Logf("ghpr.open: gh pr view --web in %s (%s/%s)", cwd, session, window)
	// Best-effort: no PR for the branch → gh exits non-zero, which we
	// swallow (the picker binding is execute-silent; surfacing an error
	// here would just flash and vanish).
	if err := gh(cwd, "pr", "view", "--web"); err != nil {
		debuglog.LogErr("ghpr.open gh pr view --web", err)
	}
	return nil
}

// runRefresh stamps PR badges for every stale repo window.
func runRefresh(h *tmuxhost.Client, now time.Time) error {
	out, err := h.Run("list-windows", "-a", "-F",
		"#{window_id}|#{@repo_path}|#{pane_current_path}|#{@ghpr_ts}")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "|", 4)
		if len(fields) < 4 {
			continue
		}
		windowID, repoPath, cwd, tsStr := fields[0], fields[1], fields[2], fields[3]
		if repoPath == "" || cwd == "" {
			continue // non-git workspace
		}
		if fresh(now, tsStr) {
			continue
		}
		refreshWindow(h, windowID, cwd, now)
	}
	return nil
}

// refreshWindow fetches PR state for one worktree and stamps its badge.
func refreshWindow(h *tmuxhost.Client, windowID, cwd string, now time.Time) {
	state, ok := prState(cwd)
	// Stamp the timestamp regardless so a branch with no PR isn't retried
	// on every single picker open.
	stampPersisted(h, windowID, OptTs, "ghpr.ts", strconv.FormatInt(now.Unix(), 10))
	if !ok {
		stampPersisted(h, windowID, OptBadge, "ghpr.badge", "")
		stampPersisted(h, windowID, OptState, "ghpr.state", "")
		return
	}
	stampPersisted(h, windowID, OptBadge, "ghpr.badge", renderBadge(state))
	stampPersisted(h, windowID, OptState, "ghpr.state", state)
	debuglog.Logf("ghpr._refresh: window=%s state=%s", windowID, state)
}

// stampPersisted sets (or unsets, when value is empty) a tmux window option
// AND mirrors it into the statestore under a plugin-namespaced metadata key
// so it survives a tmux restart — workspace restore re-stamps every
// persisted metadata entry generically, so PR badges are back OOTB after a
// restore without waiting for the next refresh. Best-effort.
func stampPersisted(h *tmuxhost.Client, windowID, opt, metaKey, value string) {
	if value == "" {
		_ = h.UnsetWindowOption(windowID, opt)
	} else {
		_ = h.SetWindowOption(windowID, opt, value)
	}
	_ = workspace.PersistWindowMetadata(h, windowID, metaKey, value)
}

// prView is the subset of `gh pr view --json` output we care about.
type prView struct {
	State   string `json:"state"` // OPEN | MERGED | CLOSED
	IsDraft bool   `json:"isDraft"`
}

// prState returns a normalized badge state for the branch checked out in
// cwd: "open", "draft", "merged", "closed". ok is false when there's no
// associated PR (or gh/network failed) — the caller clears the badge.
func prState(cwd string) (state string, ok bool) {
	out, err := ghOutput(cwd, "pr", "view", "--json", "state,isDraft")
	if err != nil {
		return "", false
	}
	var v prView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return "", false
	}
	return classify(v.State, v.IsDraft), true
}

// classify maps gh's state + draft flag to a badge state. Pure helper.
func classify(state string, isDraft bool) string {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	default: // OPEN (or anything unexpected → treat as open)
		if isDraft {
			return "draft"
		}
		return "open"
	}
}

// renderBadge returns the spliceable, ANSI-colored badge token (with a
// leading space) for a state. Each state gets its own GitHub octicon
// (Nerd Font v3) AND GitHub's state color, so the badge is distinguishable
// by shape and by color: open=green pull-request, draft=grey draft,
// merged=purple merge, closed=red closed. Requires a Nerd Font (the tool
// UI already assumes one). Pure helper — unit-tested.
func renderBadge(state string) string {
	spec := map[string]struct{ glyph, color string }{
		"open":   {"\uf407", "35"},  // oct-git_pull_request,        green
		"draft":  {"\uf4dd", "244"}, // oct-git_pull_request_draft,  grey
		"merged": {"\uf419", "141"}, // oct-git_merge,               purple
		"closed": {"\uf4dc", "203"}, // oct-git_pull_request_closed, red
	}[state]
	if spec.color == "" {
		return ""
	}
	return " \033[38;5;" + spec.color + "m" + spec.glyph + "\033[0m"
}

// parseRow splits a picker row ("<session>\t<window>\t<display>") into its
// session and window names. Pure helper — unit-tested.
func parseRow(row string) (session, window string, ok bool) {
	fields := strings.SplitN(row, "\t", 3)
	if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
		return "", "", false
	}
	return fields[0], fields[1], true
}

// fresh reports whether a @ghpr_ts value is within refreshTTL of now.
// Empty / unparseable timestamps are stale. Pure helper.
func fresh(now time.Time, tsStr string) bool {
	secs, err := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
	if err != nil || secs <= 0 {
		return false
	}
	return now.Sub(time.Unix(secs, 0)) < refreshTTL
}

// gh runs a gh command in dir, discarding output. Used for --web (which
// opens a browser and needs no captured output).
func gh(dir string, args ...string) error {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	return cmd.Run()
}

// ghOutput runs a gh command in dir and returns trimmed stdout.
func ghOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
