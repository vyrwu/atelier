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
)

var (
	frameStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cFaint).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	hintStyle  = lipgloss.NewStyle().Foreground(cDim)
	dimStyle   = lipgloss.NewStyle().Foreground(cDim)
	faintStyle = lipgloss.NewStyle().Foreground(cFaint)
	textStyle  = lipgloss.NewStyle().Foreground(cText)
	logoStyle  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)

	selStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	selBar   = lipgloss.NewStyle().Foreground(cAccent) // the ▌ selection marker

	repoStyle   = lipgloss.NewStyle().Foreground(cDim)
	branchStyle = lipgloss.NewStyle().Foreground(cWorking)
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

// prBadge renders a PR's state as a short colored token, e.g. "#42 ✓".
func prBadge(state core.PRState) string {
	var c lipgloss.Color
	switch state {
	case core.PROpen:
		c = cWorking
	case core.PRDraft:
		c = cIdle
	case core.PRMerged:
		c = cAccent
	default: // closed
		c = cGone
	}
	return lipgloss.NewStyle().Foreground(c).Render(string(state))
}

func ciGlyph(ci core.CIState) string {
	switch ci {
	case core.CIPass:
		return lipgloss.NewStyle().Foreground(cWorking).Render("✓")
	case core.CIFail:
		return lipgloss.NewStyle().Foreground(cBlocked).Render("✗")
	case core.CIPending:
		return lipgloss.NewStyle().Foreground(cIdle).Render("•")
	default:
		return " "
	}
}

func reviewGlyph(r core.Review) string {
	switch r {
	case core.ReviewApproved:
		return lipgloss.NewStyle().Foreground(cWorking).Render("✓")
	case core.ReviewChanges:
		return lipgloss.NewStyle().Foreground(cBlocked).Render("±")
	case core.ReviewRequired:
		return lipgloss.NewStyle().Foreground(cIdle).Render("?")
	default:
		return " "
	}
}
