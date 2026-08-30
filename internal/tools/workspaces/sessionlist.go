package workspaces

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/perf"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// AgentState is a workspace's agent status, used for the row's status dot
// and the picker's primary sort key.
type AgentState int

const (
	StateBlocked AgentState = iota // agent needs you (@needs_attention)
	StateRunning                   // agent working / awaiting a sub-agent
	StateIdle                      // nothing needed
)

// SessionRow is one workspace row, exposed as STRUCTURED data. The bubbletea
// delegate (sessions_tui.go) owns all styling via lipgloss — this type
// carries only the facts. Session/Window are the tmux switch identity
// (Session may be mangled '.'/':'→'_'); DisplayName is the user-facing
// owner/repo with the dot intact.
type SessionRow struct {
	Session     string
	Window      string
	DisplayName string
	IsCurrent   bool
	State       AgentState
	Tag         string
	ForgeState  string // raw @forge_state; "" when none / no forge adapter
	Recap       string // raw AI summary text; "" when none
	Age         string // relative age, e.g. "now" / "5m" / "2h"
	AutoRepo    bool   // multi-repo/auto session (no @repo_path)
}

// sortRank is the primary picker sort key: blocked first, then running,
// then idle (mirrors the AgentState order).
func (r SessionRow) sortRank() int { return int(r.State) }

// BuildSessionList replicates tmux_session_list:
//
//   - Repo sessions stamped with @repo_path by the workspace creator
//   - Auto (multi-repo) sessions stamped with @ai_workspace_kind
//   - Filters out atelier popup sessions (starts with `_`)
//   - Icons (the agent-status dot, derived by the refresh loop):
//     red-bold `❯` current workspace
//     yellow `⏺` blocked — agent waiting on you (@needs_attention)
//     blue `⏺` running — agent working / waiting on a sub-agent (@agent_status)
//     dim `○` idle — nothing needed
//   - Cyan session / green window; auto sessions use orange (256:166)
//   - Bold session+window when current
//   - Italic-grey `· <recap>` line when @attention_recap is set
//   - Sort: blocked → running → idle, then tag → forge
func BuildSessionList(h *tmuxhost.Client) ([]SessionRow, error) {
	defer perf.Start("session-list").End()

	// Find outer (workspace) client's current sid+wid for "you are here".
	currentSid, currentWid, err := outerCurrent(h)
	if err != nil {
		return nil, err
	}

	// Kernel forge-badge slot: when a forge integration is active, the
	// picker reads the kernel-cached @forge_state, renders the glyph itself
	// (renderForgeBadge), and sorts by it (forgeStateRank). The adapter only
	// classified the state into @forge_state; the picker owns presentation.
	// Absent adapter → no column, no extra field.
	showForge := forgeActive()

	const baseFields = 11
	format := "#{session_id}|#{window_id}|#{session_name}|#{window_name}|#{@repo_path}|#{@needs_attention}|#{@ai_workspace_kind}|#{@attention_recap}|#{" + workspace.OptWorkspaceCreatedTs + "}|#{" + workspace.OptWorkspaceTag + "}|#{" + workspace.OptAgentStatus + "}"
	if showForge {
		format += "|#{" + OptForgeState + "}"
	}
	out, err := h.Run("list-windows", "-a", "-F", format)
	if err != nil {
		return nil, err
	}
	now := time.Now()

	var rows []SessionRow
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		nFields := baseFields
		if showForge {
			nFields++
		}
		fields := strings.SplitN(line, "|", nFields)
		if len(fields) < baseFields {
			continue
		}
		sid, wid, session, window := fields[0], fields[1], fields[2], fields[3]
		repoPath, attention, kind, recap, createdTs := fields[4], fields[5], fields[6], fields[7], fields[8]
		tag := strings.TrimSpace(fields[9])
		agentStatus := strings.TrimSpace(fields[10])
		forgeState := ""
		if showForge && len(fields) > baseFields {
			forgeState = strings.TrimSpace(fields[baseFields])
		}

		// Filter out atelier-managed popup sessions.
		if strings.HasPrefix(session, "_") {
			continue
		}
		// Only include sessions stamped with @repo_path OR @ai_workspace_kind.
		// Shared with the status-line attention rollup (countAttentionWindows)
		// so the picker and the badge can never disagree on what counts as a
		// listable workspace.
		if !workspace.Listable(repoPath, kind) {
			continue
		}

		// blocked = the agent needs you. Keyed on @needs_attention, NOT
		// @agent_status, so visiting the workspace (after-select-window clears
		// @needs_attention) drops the dot instantly. running = the agent is
		// working — keyed on @agent_status so a visit doesn't hide it (the
		// agent is still going); the loop clears it when the agent stops.
		state := StateIdle
		switch {
		case attention == "1":
			state = StateBlocked
		case agentStatus == workspace.AgentRunning:
			state = StateRunning
		}
		isCurrent := currentSid != "" && sid == currentSid && wid == currentWid

		age := "now"
		if !isCurrent {
			age = formatAge(now, createdTs)
		}

		// User-facing owner/repo (dot intact) recovered from @repo_path;
		// tmux mangles '.'/':'→'_' in the session_name. SessionRow.Session
		// keeps the real (mangled) name — it's the tmux switch target.
		displayName := session
		if slug := repoSlugFromPath(repoPath); slug != "" {
			displayName = slug
		}

		rows = append(rows, SessionRow{
			Session:     session,
			Window:      window,
			DisplayName: displayName,
			IsCurrent:   isCurrent,
			State:       state,
			Tag:         tag,
			ForgeState:  forgeState,
			Recap:       strings.TrimSpace(recap),
			Age:         age,
			AutoRepo:    repoPath == "",
		})
	}

	// Sort: attention (blocked → running → idle) → tag (tagged before
	// untagged, same-tag clusters) → forge state.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].sortRank() != rows[j].sortRank() {
			return rows[i].sortRank() < rows[j].sortRank()
		}
		ti, tj := rows[i].Tag, rows[j].Tag
		if ti != tj {
			if ti == "" {
				return false
			}
			if tj == "" {
				return true
			}
			return ti < tj
		}
		return forgeStateRank(rows[i].ForgeState) < forgeStateRank(rows[j].ForgeState)
	})
	return rows, nil
}

// formatAge renders a short relative-time suffix for a unix epoch.
// Returns "30s", "5m", "2h", "3d". Empty / unparseable / zero / future
// timestamps return "" so the caller skips the suffix rather than
// rendering a confusing zero.
func formatAge(now time.Time, tsStr string) string {
	tsStr = strings.TrimSpace(tsStr)
	if tsStr == "" {
		return ""
	}
	secs, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil || secs <= 0 {
		return ""
	}
	d := now.Sub(time.Unix(secs, 0))
	if d < 0 {
		return ""
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// outerCurrent returns the (session_id, window_id) the outer workspace client
// is currently attached to. Used to highlight the "you are here" row.
func outerCurrent(h *tmuxhost.Client) (sid, wid string, err error) {
	out, err := h.Run("list-clients", "-F", "#{client_session}|#{session_id}|#{window_id}")
	if err != nil {
		return "", "", nil // best-effort
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		if strings.HasPrefix(parts[0], "_") {
			continue
		}
		return parts[1], parts[2], nil
	}
	return "", "", nil
}

// DefaultBranch returns the repo's default branch (origin/HEAD → main →
// master → "main"). Stub-wraps internal/workspace.DefaultBranch.
func DefaultBranch(repoPath string) string {
	// Delegate to internal/workspace.DefaultBranch via re-implementation
	// to avoid an import cycle (workspaces is consumed by core's
	// cli/workspace; pulling workspace here would cycle if expanded).
	// Inline:
	out := runGitQuiet(repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if out != "" {
		if i := strings.Index(out, "/"); i >= 0 {
			return out[i+1:]
		}
		return out
	}
	for _, b := range []string{"main", "master"} {
		if runGitQuiet(repoPath, "rev-parse", "--verify", b) != "" {
			return b
		}
	}
	return "main"
}
