package workspaces

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/perf"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// SessionRow is one row in the workspace selector. Lines are emitted as
//
//	<session>\t<window>\t<name>\t<recap>
//
// The picker runs --with-nth=3,4 (display the name plus the recap on its own
// line beneath it) and --nth=1 (search the name only, NOT the recap). Fields
// 1-2 stay plain for output parsing. Display holds the styled name; Recap holds
// the indented second line — always present so every row is a uniform two-line
// height, blank when there's no AI summary (#43). No --wrap: a too-wide recap is
// truncated to the popup width by fzf.
type SessionRow struct {
	Session string
	Window  string
	Display string
	Recap   string
}

// Visible cell widths of the fixed left-hand columns. Their sum is the
// picker's --wrap-sign width, which hangs wrapped recap text under the
// workspace name. Kept in sync with the escape sequences that render them.
const (
	timeColCells  = 4 // "%3s" (3) + trailing space
	iconColCells  = 2 // attention glyph (or space) + trailing space
	badgeColCells = 2 // forge-badge slot (glyph+space or two spaces)
)

// recapIndentCells is the number of leading spaces that aligns the recap line
// under the workspace name — the visible width of the fixed columns before the
// name (time + icon, plus the forge-badge slot when a forge integration is
// active). Pure.
func recapIndentCells(showForge bool) int {
	n := timeColCells + iconColCells
	if showForge {
		n += badgeColCells
	}
	return n
}

// zeroWidthSpace terminates the reserved (empty-recap) second line. fzf
// collapses a multi-line item whose trailing line is whitespace-only after
// ANSI stripping — a blank line of plain spaces (or one ending in an ANSI
// reset) is trimmed away, dropping the reserved row and re-introducing the
// one-vs-two-line height oscillation (#43). A zero-width space is invisible yet
// counts as non-whitespace to fzf's trimmer, so the line reserves height while
// rendering blank. Verified against fzf 0.72 under --ansi; a non-breaking space
// does NOT work (fzf trims it as whitespace). Go's unicode.IsSpace agrees —
// it excludes U+200B but includes U+00A0 — so the TrimSpace guard in
// recap_line_test.go mirrors fzf's behavior.
const zeroWidthSpace = "\u200b"

// formatRecapLine renders the AI recap as an italic dim-grey line beneath the
// workspace name (fzf multi-line item): a leading newline, `indent` spaces to
// sit under the name, then `· summary`. The picker runs WITHOUT --wrap, so a
// recap wider than the popup is truncated to width by fzf with an ellipsis —
// row height stays a predictable two lines. Empty recap → a blank (but present)
// second line so every row is a uniform two-line height (#43). Pure.
func formatRecapLine(recap string, indent int) string {
	if recap == "" {
		return "\n" + strings.Repeat(" ", indent) + zeroWidthSpace
	}
	return fmt.Sprintf("\n%s\033[3;38;5;103m· %s\033[0m", strings.Repeat(" ", indent), recap)
}

// BuildSessionList builds the Active Workspaces (M-a) picker rows:
//
//   - Repo sessions stamped with @repo_path by the workspace creator
//   - Auto (multi-repo) sessions stamped with @ai_workspace_kind
//   - Filters out atelier popup sessions (starts with `_`)
//   - Icons:
//     red-bold `❯` current workspace
//     yellow `⏺` needs attention
//     dim `○` no attention (idle — every workspace can host an agent, so
//     the circle reads "nothing wants you here" rather than "agent present")
//   - Cyan session / green window; auto sessions use orange (256:166)
//   - Bold session+window when current
//   - Italic-grey `· <recap>` line when @attention_recap is set
//   - Time column is age since creation (@created_ts) — a "how old / should
//     I GC this" signal, not a last-touched clock
//   - Ordered by the active sort mode (readSortMode / sortEntries), which
//     the user cycles with Tab; Attention is the default
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
	mode := readSortMode(h)

	const baseFields = 10
	format := "#{session_id}|#{window_id}|#{session_name}|#{window_name}|#{@repo_path}|#{@needs_attention}|#{@ai_workspace_kind}|#{@attention_recap}|#{" + workspace.OptCreatedTs + "}|#{" + workspace.OptWorkspaceTag + "}"
	if showForge {
		format += "|#{" + OptForgeState + "}"
	}
	out, err := h.Run("list-windows", "-a", "-F", format)
	if err != nil {
		return nil, err
	}
	now := time.Now()

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

	var entries []sessionEntry

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
		repoPath, attention, kind, recap, createdStr := fields[4], fields[5], fields[6], fields[7], fields[8]
		tag := strings.TrimSpace(fields[9])
		// Kernel forge badge: the cached @forge_state field (if present)
		// follows the fixed fields. The picker renders the glyph itself and
		// computes the sort rank — the adapter only classified the state.
		forgeBadge := ""
		forgeRank := forgeStateRank("")
		if showForge && len(fields) > baseFields {
			forgeBadge = renderForgeBadge(fields[baseFields])
			forgeRank = forgeStateRank(fields[baseFields])
		}
		// Fixed-width forge-badge slot rendered between the attention icon and
		// the workspace name (layout: time · icon · badge · name · recap).
		// Only present when a forge integration is active; the empty slot keeps
		// name columns aligned for workspaces with no PR.
		badgeCol := ""
		if showForge {
			badgeCol = forgeBadgeColumn(forgeBadge)
		}

		// Filter out atelier-managed popup sessions.
		if strings.HasPrefix(session, "_") {
			continue
		}
		// Only include sessions stamped with @repo_path OR @ai_workspace_kind.
		if repoPath == "" && kind == "" {
			continue
		}

		isAttn := attention == "1"
		isCurrent := currentSid != "" && sid == currentSid && wid == currentWid
		createdTs, _ := strconv.ParseInt(strings.TrimSpace(createdStr), 10, 64)

		// Layout (multi-line item):
		//   <time> <icon> <badge> <window> <session>
		//                         · <recap>
		//
		// Time is a 3-char right-aligned dim-grey column showing age since
		// creation (@created_ts) — a GC signal, uniform across rows. Icon
		// (❯ ⏺ ○) follows after a single space — the icon column is 2 cells
		// wide (glyph + trailing space) so name text stays vertically aligned.
		//
		// Recap drops onto its own line, indented to sit under the name.
		timeCol := fmt.Sprintf("\033[38;5;103m%3s\033[0m ", formatAge(now, createdStr))

		var icon string
		switch {
		case isCurrent:
			icon = "\033[1;31m❯\033[0m "
		case isAttn:
			icon = "\033[33m⏺\033[0m "
		default:
			// ○ = idle / no attention. Every workspace can host an agent, so
			// the circle means "nothing wants you here," not "agent present."
			icon = "\033[90m○\033[0m "
		}

		// Show the persisted AI summary whenever one exists — NOT only when
		// the agent popup is currently live. Restore re-stamps @attention_recap,
		// but a fresh launch doesn't recreate the popup, so gating on a live
		// popup hid every workspace's last summary after relaunch. The recap is
		// only ever written by the AI agent, so its presence already means the
		// workspace is agent-associated.
		//
		// The recap renders on its OWN line beneath the workspace name,
		// indented to sit under the name column. No wrap — fzf truncates it to
		// the popup width, keeping row height a predictable two lines.
		recapStr := formatRecapLine(recap, recapIndentCells(showForge))

		// Bold weight for current.
		weight := ""
		if isCurrent {
			weight = "1;"
		}

		// Default-branch rows sink within a group; auto (multi-repo) sessions
		// have no default branch. session=cyan(36) git / orange(256:166) auto.
		isDefault := false
		sessionColor := "38;5;166"
		if repoPath != "" {
			isDefault = window == defaultBranchFor(repoPath)
			sessionColor = "36"
		}
		display := formatSessionDisplay(timeCol, icon, badgeCol, weight, sessionColor, session, window, tag)

		entries = append(entries, sessionEntry{
			row:       SessionRow{Session: session, Window: window, Display: display, Recap: recapStr},
			isAttn:    isAttn,
			isDefault: isDefault,
			createdTs: createdTs,
			session:   session,
			window:    window,
			tag:       tag,
			forgeRank: forgeRank,
		})
	}

	sortEntries(entries, mode)

	out2 := make([]SessionRow, 0, len(entries))
	for _, e := range entries {
		out2 = append(out2, e.row)
	}
	debuglog.Logf("workspaces.BuildSessionList: sort=%s rows=%d forge=%v", mode, len(out2), showForge)
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
