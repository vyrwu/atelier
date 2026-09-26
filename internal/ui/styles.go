package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/core"
)

// The palette — toned and quiet: a soft slate-blue accent, a neutral grey scale,
// and a single warm attention colour. Only "blocked" is an attention colour;
// everything else stays quiet (FR-B1: the badge means "something needs you").
var (
	cAccent  = lipgloss.Color("110") // soft slate blue — atelier's accent
	cBlocked = lipgloss.Color("173") // muted terracotta — the single attention colour
	cWorking = lipgloss.Color("73")  // muted teal — agent working
	cIdle    = lipgloss.Color("245") // grey
	cGone    = lipgloss.Color("240") // dim
	cText    = lipgloss.Color("252")
	cDim     = lipgloss.Color("245")
	cFaint   = lipgloss.Color("240")

	// Forge palette — GitHub's own PR/CI/review colours for the Octicon glyphs:
	// green open, grey draft, purple merged, red closed; green pass / red fail /
	// yellow pending.
	cOpen     = lipgloss.Color("71")  // green
	cDraft    = lipgloss.Color("245") // grey
	cMerged   = lipgloss.Color("141") // purple
	cClosed   = lipgloss.Color("167") // red
	cPending  = lipgloss.Color("179") // amber/yellow
	cWorktree = lipgloss.Color("136") // dark yellow — worktree counts (vs grey draft PRs)
)

var (
	frameStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cFaint).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	noteStyle  = lipgloss.NewStyle().Foreground(cOpen) // positive transient feedback
	hintStyle  = lipgloss.NewStyle().Foreground(cDim)
	dimStyle   = lipgloss.NewStyle().Foreground(cDim)
	faintStyle = lipgloss.NewStyle().Foreground(cFaint)
	textStyle  = lipgloss.NewStyle().Foreground(cText)
	logoStyle  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)

	selStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	selBar   = lipgloss.NewStyle().Foreground(cAccent) // the ▌ selection marker

	repoStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("103")) // muted slate — repo section headers
	branchStyle = lipgloss.NewStyle().Foreground(cWorking)

	// trashTimeStyle is the distinct yellow of the Trash "time since close" column.
	trashTimeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))
)

// lipglossWarn styles an inline warning (amber).
func lipglossWarn(s string) string {
	return lipgloss.NewStyle().Foreground(cBlocked).Render(s)
}

// statusGlyph is the one-rune status marker for a workspace row, colored only
// when it wants the eye.
func statusGlyph(st core.AgentStatus) string {
	switch st {
	case core.StatusBlocked:
		return lipgloss.NewStyle().Foreground(cBlocked).Bold(true).Render("●")
	case core.StatusWorking:
		return lipgloss.NewStyle().Foreground(cWorking).Render("◐")
	case core.StatusIdle:
		return lipgloss.NewStyle().Foreground(cIdle).Render("○")
	default: // gone
		return lipgloss.NewStyle().Foreground(cGone).Render("·")
	}
}

// ciGlyph is a coloured Octicon for CI: green check-circle pass, red x-circle
// fail, yellow dot pending; nothing when there are no checks.
func ciGlyph(ci core.CIState) string {
	switch ci {
	case core.CIPass:
		return lipgloss.NewStyle().Foreground(cOpen).Render(iconCIPass)
	case core.CIFail:
		return lipgloss.NewStyle().Foreground(cClosed).Render(iconCIFail)
	case core.CIPending:
		return lipgloss.NewStyle().Foreground(cPending).Render(iconCIPending)
	default:
		return ""
	}
}

// reviewGlyph is an Octicon for the review decision: green verified approved,
// red discussion changes-requested, yellow eye review-pending; nothing when none
// requested.
func reviewGlyph(r core.Review) string {
	switch r {
	case core.ReviewApproved:
		return lipgloss.NewStyle().Foreground(cOpen).Render(iconReviewApproved)
	case core.ReviewChanges:
		return lipgloss.NewStyle().Foreground(cClosed).Render(iconReviewChanges)
	case core.ReviewRequired:
		return lipgloss.NewStyle().Foreground(cPending).Render(iconReviewPending)
	default:
		return ""
	}
}
