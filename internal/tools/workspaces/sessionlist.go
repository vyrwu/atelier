package workspaces

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/perf"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// SessionRow is one row in the workspace selector. Lines are emitted as
//
//	<session>\t<window>\t<colored-display>
//
// matching bash tmux_session_list. with-nth=3 in fzf hides the first two.
type SessionRow struct {
	Session string
	Window  string
	Display string
}

// BuildSessionList replicates tmux_session_list:
//
//   - Repo sessions stamped with @repo_path by the workspace creator
//   - Auto (multi-repo) sessions stamped with @claude_workspace_kind
//   - Filters out atelier popup sessions (starts with `_`)
//   - Icons:
//     red-bold `❯` current workspace
//     yellow `⏺` attention
//     dim `○` claude-present (no attention)
//   - Cyan session / green window; auto sessions use orange (256:166)
//   - Bold session+window when current
//   - Italic-grey `· <recap>` suffix when @attention_recap set AND claude-present
//   - Priority sort: claude+attention < claude < attention < regular
//     (default-branch row of a repo sorts after non-default within each layer)
func BuildSessionList(h *tmuxhost.Client) ([]SessionRow, error) {
	defer perf.Start("session-list").End()

	// Find outer (workspace) client's current sid+wid for "you are here".
	currentSid, currentWid, err := outerCurrent(h)
	if err != nil {
		return nil, err
	}

	// Badge providers (e.g. ghpr's PR-status symbol) declare a window
	// option the picker splices between the workspace name and the recap.
	// The picker stays agnostic to what each badge means — it just reads
	// the declared option and renders its (pre-colored) value. This is the
	// generic mechanism task #75 will fold @ai_workspace_kind into.
	badgeSpecs := discoverBadges()
	badgeKeys := badgeOptionKeys(badgeSpecs)
	// Providers may also declare a row-sort signal (e.g. ghpr: open →
	// draft → merged → closed). Read those options too; they're appended
	// to the format AFTER the badge columns.
	sorts := badgeSorts(badgeSpecs)
	sortKeys := sortOptionKeys(sorts)

	// TODO(plugins-refactor): the `@ai_workspace_kind` field is the AI
	// plugin's namespace leaking into the foundational picker. When
	// task #75 lands, the picker should source workspace-kind from
	// statestore.Workspace.Kind directly (it's already mirrored there)
	// instead of reading an AI plugin option.
	const baseFields = 10
	format := "#{session_id}|#{window_id}|#{session_name}|#{window_name}|#{session_last_attached}|#{@repo_path}|#{@needs_attention}|#{@ai_workspace_kind}|#{@attention_recap}|#{@last_seen}"
	for _, k := range badgeKeys {
		format += "|#{" + k + "}"
	}
	for _, k := range sortKeys {
		format += "|#{" + k + "}"
	}
	out, err := h.Run("list-windows", "-a", "-F", format)
	if err != nil {
		return nil, err
	}
	now := time.Now()

	allSessions, _ := h.ListSessions()
	hasClaude := make(map[string]bool, len(allSessions))
	for _, s := range allSessions {
		if strings.HasPrefix(s, "_claudepop_") || strings.HasPrefix(s, "_atelier_claude_") {
			rest := strings.TrimPrefix(strings.TrimPrefix(s, "_atelier_claude_"), "_claudepop_")
			hasClaude[rest] = true
		}
	}

	// Memoize DefaultBranch per repo path. Every window of a repo
	// session shares one @repo_path and one default branch, but the
	// picker has one row per window — without the cache this shells out
	// `git symbolic-ref` once per row (dozens of sequential git spawns
	// in a busy sandbox) on the critical path before the picker opens.
	// Cache collapses that to one git call per distinct repo.
	defaultBranchCache := make(map[string]string)
	defaultBranchFor := func(repoPath string) string {
		if b, ok := defaultBranchCache[repoPath]; ok {
			return b
		}
		b := DefaultBranch(repoPath)
		defaultBranchCache[repoPath] = b
		return b
	}

	type entry struct {
		priority int
		row      SessionRow
		lastAtt  string
		sortRank []int // provider-declared row-sort ranks, in provider order
	}
	var entries []entry

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "|", baseFields+len(badgeKeys)+len(sortKeys))
		if len(fields) < baseFields {
			continue
		}
		sid, wid, session, window, lastAtt := fields[0], fields[1], fields[2], fields[3], fields[4]
		repoPath, attention, kind, recap, lastSeen := fields[5], fields[6], fields[7], fields[8], fields[9]
		// Badge values (each already carries its own leading space +
		// ANSI color) follow the fixed fields, in provider order; the
		// sort-signal columns follow the badge columns.
		badgeStr := strings.Join(fields[baseFields:baseFields+len(badgeKeys)], "")
		sortRank := make([]int, len(sorts))
		for si, bs := range sorts {
			fi := baseFields + len(badgeKeys) + si
			v := ""
			if fi < len(fields) {
				v = fields[fi]
			}
			sortRank[si] = bs.rankOf(v)
		}
		// Prefer @last_seen (stamped by client-session-changed hook on
		// switch-away) over session_last_attached (frozen at initial
		// attach). last_seen is missing for sessions that haven't
		// been switched away from since the hook started firing —
		// fall back to last_attached so those rows still show a
		// number rather than blank.
		if strings.TrimSpace(lastSeen) != "" {
			lastAtt = lastSeen
		}

		// Filter out atelier-managed popup sessions.
		if strings.HasPrefix(session, "_") {
			continue
		}
		// Only include sessions stamped with @repo_path OR @claude_workspace_kind.
		if repoPath == "" && kind == "" {
			continue
		}

		sidNum := strings.TrimPrefix(sid, "$")
		widNum := strings.TrimPrefix(wid, "@")
		claudeKey := sidNum + "_" + widNum
		isClaude := hasClaude[claudeKey]
		isAttn := attention == "1"
		isCurrent := currentSid != "" && sid == currentSid && wid == currentWid

		// Layout: "<time> <icon><session>/<window>  · <recap>"
		//
		// Time is a 3-char right-aligned dim-grey column on the left.
		// Icon (❯ ⏺ ○) follows after a single space — the icon column
		// is 2 cells wide (glyph + trailing space) so name text stays
		// vertically aligned across rows.
		//
		// Recap stays at the END (variable; allowed to push right).
		var ageText string
		if isCurrent {
			ageText = "now"
		} else {
			ageText = formatAge(now, lastAtt)
		}
		timeCol := fmt.Sprintf("\033[38;5;103m%3s\033[0m ", ageText)

		var icon string
		switch {
		case isCurrent:
			icon = "\033[1;31m❯\033[0m "
		case isAttn:
			icon = "\033[33m⏺\033[0m "
		case isClaude:
			icon = "\033[90m○\033[0m "
		default:
			icon = "  "
		}

		recapStr := ""
		if isClaude && recap != "" {
			recapStr = fmt.Sprintf(" \033[3;38;5;103m· %s\033[0m", recap)
		}

		// Bold weight for current.
		weight := ""
		if isCurrent {
			weight = "1;"
		}

		var display string
		var priority int
		if repoPath != "" {
			defaultBranch := defaultBranchFor(repoPath)
			isDefault := window == defaultBranch
			// Priority layers (bash):
			//   0/1 claude+attention (default last)
			//   2/3 claude
			//   4/5 attention
			//   6/7 regular
			switch {
			case isAttn && isClaude:
				if isDefault {
					priority = 1
				} else {
					priority = 0
				}
			case isClaude:
				if isDefault {
					priority = 3
				} else {
					priority = 2
				}
			case isAttn:
				if isDefault {
					priority = 5
				} else {
					priority = 4
				}
			default:
				if isDefault {
					priority = 7
				} else {
					priority = 6
				}
			}
			// session=cyan(36), window=green(32). Badge (if any) sits
			// between the window name and the recap suffix.
			display = fmt.Sprintf("%s%s\033[%s36m%s\033[0m/\033[%s32m%s\033[0m%s%s",
				timeCol, icon, weight, session, weight, window, badgeStr, recapStr)
		} else {
			// Non-git (auto) session.
			priority = 8
			if isAttn {
				priority = 0
			}
			// session=orange(256:166), window=green(32). Badge (if any)
			// sits between the window name and the recap suffix.
			display = fmt.Sprintf("%s%s\033[%s38;5;166m%s\033[0m/\033[%s32m%s\033[0m%s%s",
				timeCol, icon, weight, session, weight, window, badgeStr, recapStr)
		}

		entries = append(entries, entry{
			priority: priority,
			row:      SessionRow{Session: session, Window: window, Display: display},
			lastAtt:  lastAtt,
			sortRank: sortRank,
		})
	}

	// Stable sort by priority, then by any provider-declared row-sort
	// signal (e.g. ghpr: open → draft → merged → closed), ties finally
	// broken by reverse last_attached (newest first).
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].priority != entries[j].priority {
			return entries[i].priority < entries[j].priority
		}
		for k := range entries[i].sortRank {
			if entries[i].sortRank[k] != entries[j].sortRank[k] {
				return entries[i].sortRank[k] < entries[j].sortRank[k]
			}
		}
		return entries[i].lastAtt > entries[j].lastAtt
	})

	out2 := make([]SessionRow, 0, len(entries))
	for _, e := range entries {
		out2 = append(out2, e.row)
	}
	return out2, nil
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
