package workspaces

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/perf"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// WorkspaceRow is one row in the M-s picker — ONE row per workspace (not per
// window). Lines are emitted as:
//
//	<session>\t<title>\t<display>\t<summary>
//
// The picker runs --with-nth=3,4 (title line + summary line beneath) and
// --nth=2 (search the title only). Field 1 (session) is the switch target every
// bind keys on. Display holds the styled title row; Summary the indented second
// line — always present so every row is a uniform two-line height.
type WorkspaceRow struct {
	Session string
	Title   string
	Display string
	Summary string
}

// Column cell widths for aligning the summary line under the title.
const (
	timeColCells  = 4 // "%3s" + trailing space
	attnColCells  = 3 // attention marker/count + trailing space
	forgeColCells = 5 // "NNPR" slot + trailing space
)

func summaryIndentCells() int { return timeColCells + attnColCells + forgeColCells }

const zeroWidthSpace = "\u200b"

// formatSummaryLine renders the workspace summary as an italic dim-grey line
// under the title (fzf multi-line item). Empty summary → a blank (present)
// second line so row height stays a uniform two lines. Pure.
func formatSummaryLine(summary string, indent int) string {
	if summary == "" {
		return "\n" + strings.Repeat(" ", indent) + zeroWidthSpace
	}
	return fmt.Sprintf("\n%s\033[3;38;5;103m· %s\033[0m", strings.Repeat(" ", indent), summary)
}

// wsAgg is the merged live-tmux + statestore view of one workspace.
type wsAgg struct {
	session   string
	sid       string
	title     string
	tag       string
	driverWid string
	attention bool   // driver blocked
	running   bool   // driver running
	recap     string // driver recap (summary fallback)
	recapTs   string // driver recap timestamp (age)
	createdTs string
	current   bool
	prs       []statestore.PR
	summary   string
}

// BuildWorkspaceList assembles one row per live workspace, merging live tmux
// per-driver state (attention/recap/current) with persisted PRs + summary. Sort:
// attention → tag → forge → title.
func BuildWorkspaceList(h *tmuxhost.Client) ([]WorkspaceRow, error) {
	defer perf.Start("workspace-list").End()

	currentSid, _, err := outerCurrent(h)
	if err != nil {
		return nil, err
	}
	st, _ := statestore.Load()

	format := strings.Join([]string{
		"#{session_id}", "#{session_name}", "#{window_index}",
		"#{" + workspace.OptWorkspaceID + "}", "#{" + workspace.OptWorkspaceTitle + "}",
		"#{" + workspace.OptWorkspaceDriver + "}", "#{" + workspace.OptWorkspaceTag + "}",
		"#{" + workspace.OptAttention + "}", "#{" + workspace.OptAgentStatus + "}",
		"#{" + workspace.OptRecap + "}", "#{" + workspace.OptRecapTs + "}",
		"#{" + workspace.OptWorkspaceCreatedTs + "}", "#{window_id}",
	}, "\x1f")
	out, err := h.Run("list-windows", "-a", "-F", format)
	if err != nil {
		return nil, err
	}

	bySession := map[string]*wsAgg{}
	var order []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x1f")
		if len(f) < 13 {
			continue
		}
		sid, session, idxStr := f[0], f[1], f[2]
		wsID, title, driver, tag := f[3], f[4], f[5], f[6]
		attention, status, recap, recapTs, createdTs, wid := f[7], f[8], f[9], f[10], f[11], f[12]
		if strings.HasPrefix(session, "_") || !workspace.Listable(wsID) {
			continue
		}
		agg := bySession[session]
		if agg == nil {
			agg = &wsAgg{session: session, sid: sid, title: title, createdTs: createdTs}
			bySession[session] = agg
			order = append(order, session)
		}
		// The driver window (explicit marker, else lowest index) supplies the
		// workspace's attention/recap/tag.
		isDriver := strings.TrimSpace(driver) == "1"
		idx, _ := strconv.Atoi(strings.TrimSpace(idxStr))
		if isDriver || (agg.driverWid == "" && idx <= 1) || agg.driverWid == "" {
			agg.driverWid = wid
			agg.attention = attention == "1"
			agg.running = strings.TrimSpace(status) == workspace.AgentRunning
			agg.recap = recap
			agg.recapTs = recapTs
			if tag != "" {
				agg.tag = strings.TrimSpace(tag)
			}
			if title != "" {
				agg.title = title
			}
		}
		if sid == currentSid {
			agg.current = true
		}
	}

	// Merge persisted PRs + summary + title fallback from statestore.
	if st != nil {
		for i := range st.Workspaces {
			ws := &st.Workspaces[i]
			agg := bySession[statestore.CanonicalSessionName(ws.SessionName)]
			if agg == nil {
				continue
			}
			agg.prs = ws.PRs
			agg.summary = ws.Summary
			if agg.title == "" {
				agg.title = ws.Title
			}
		}
	}

	now := time.Now()
	showForge := forgeActive()
	rows := make([]WorkspaceRow, 0, len(order))
	type ranked struct {
		attn      int
		tag       string
		forgeRank int
		row       WorkspaceRow
	}
	var ranks []ranked
	for _, session := range order {
		agg := bySession[session]
		title := agg.title
		if title == "" {
			title = session
		}

		prCount, lead := workspaceForgeRollup(agg.prs)
		prAttn := workspacePRAttention(agg.prs)
		attnCount := prAttn
		if agg.attention {
			attnCount++
		}

		// TIME column: age since the driver's last recap (activity), else creation.
		ageSrc := agg.recapTs
		if ageSrc == "" {
			ageSrc = agg.createdTs
		}
		ageText := formatAge(now, ageSrc)
		if agg.current {
			ageText = "now"
		}
		timeCol := fmt.Sprintf("\033[38;5;103m%3s\033[0m ", ageText)

		// ATTN column: current marker, else attention count, else running/idle.
		var attnCol string
		switch {
		case agg.current:
			attnCol = "\033[1;31m❯\033[0m  "
		case attnCount > 0:
			attnCol = fmt.Sprintf("\033[33m%d⏺\033[0m ", attnCount)
		case agg.running:
			attnCol = "\033[34m⏺\033[0m  "
		default:
			attnCol = "\033[90m○\033[0m  "
		}

		// FORGE column: "NPR" in the lead-state color, else blank.
		forgeCol := "     "
		if showForge && prCount > 0 {
			_, color, ok := integration.ForgeGlyph(lead)
			if !ok {
				color = "244"
			}
			forgeCol = fmt.Sprintf("\033[38;5;%sm%dPR\033[0m ", color, prCount)
			forgeCol = padVisible(forgeCol, fmt.Sprintf("%dPR ", prCount), forgeColCells)
		}

		weight := ""
		if agg.current {
			weight = "1;"
		}
		tagLead := ""
		if pill := strings.TrimSpace(renderTagPill(agg.tag)); pill != "" {
			tagLead = pill + " "
		}
		display := fmt.Sprintf("%s%s%s%s\033[%s38;5;255m%s\033[0m",
			timeCol, attnCol, forgeCol, tagLead, weight, title)

		summary := agg.summary
		if summary == "" {
			summary = agg.recap // fall back to the driver's recap
		}
		summaryLine := formatSummaryLine(summary, summaryIndentCells())

		attnRank := 2
		switch {
		case attnCount > 0:
			attnRank = 0
		case agg.running:
			attnRank = 1
		}
		ranks = append(ranks, ranked{
			attn:      attnRank,
			tag:       agg.tag,
			forgeRank: forgeStateRank(lead),
			row:       WorkspaceRow{Session: session, Title: title, Display: display, Summary: summaryLine},
		})
	}

	sort.SliceStable(ranks, func(i, j int) bool {
		if ranks[i].attn != ranks[j].attn {
			return ranks[i].attn < ranks[j].attn
		}
		ti, tj := ranks[i].tag, ranks[j].tag
		if ti != tj {
			if ti == "" {
				return false
			}
			if tj == "" {
				return true
			}
			return ti < tj
		}
		return ranks[i].forgeRank < ranks[j].forgeRank
	})
	for _, r := range ranks {
		rows = append(rows, r.row)
	}
	return rows, nil
}

// padVisible pads a styled string so its VISIBLE width (given plainForm) hits
// `cells`, keeping columns aligned regardless of ANSI escapes. Pure.
func padVisible(styled, plainForm string, cells int) string {
	if n := cells - len([]rune(plainForm)); n > 0 {
		return styled + strings.Repeat(" ", n)
	}
	return styled
}

// formatAge renders a short relative-time suffix ("30s"/"5m"/"2h"/"3d") for a
// unix-epoch string. Empty/unparseable/zero/future → "". Pure.
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

// outerCurrent returns the (session_id, window_id) the outer workspace client is
// attached to, for the "you are here" marker.
func outerCurrent(h *tmuxhost.Client) (sid, wid string, err error) {
	out, err := h.Run("list-clients", "-F", "#{client_session}|#{session_id}|#{window_id}")
	if err != nil {
		return "", "", nil
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
