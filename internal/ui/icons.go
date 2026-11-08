package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
)

// Octicon glyphs — GitHub's own icon set, via Nerd Font codepoints (Octicons
// block, nf-oct-*). The forge-related views (Spaces counts, Pull Requests,
// Worktrees) use these per the design, so a Nerd Font is required in the
// terminal. Codepoints are verified against the installed font; each carries its
// nf-oct-* name so a mis-rendered glyph is a one-line fix, never a redesign.
const (
	iconPROpen         = "" // nf-oct-git_pull_request (U+F407)
	iconPRDraft        = "" // nf-oct-git_pull_request_draft (U+F4DD)
	iconPRMerged       = "" // nf-oct-git_merge (U+F419)
	iconPRClosed       = "" // nf-oct-git_pull_request_closed (U+F4DC)
	iconPRQueued       = "" // nf-oct-git_merge_queue (U+F4DB)
	iconWorktree       = "" // nf-oct-git_branch (U+F418)
	iconTrash          = "" // nf-oct-trash (U+F48E)
	iconComment        = "" // nf-oct-comment (U+F41F)
	iconBehind         = "" // nf-oct-arrow_down (U+F433)
	iconAhead          = "" // nf-oct-arrow_up (U+F431)
	iconCheck          = "" // nf-oct-check (U+F42E)
	iconCIPass         = "" // nf-oct-check_circle_fill (U+F4A4)
	iconCIFail         = "" // nf-oct-x_circle_fill (U+F530)
	iconCIPending      = "" // nf-oct-dot_fill (U+F444)
	iconReviewApproved = "" // nf-oct-verified (U+F4A1)
	iconReviewChanges  = "" // nf-oct-comment_discussion (U+F442)
	iconReviewPending  = "" // nf-oct-eye (U+F441)
)

// Agent glyphs mark which agent a space runs. Claude and Codex use Codicons
// (Nerd Fonts 3.5.0+); Grok uses the Atelier Logos fallback font.
const (
	glyphClaude = "\uec82"     // nf-cod-claude (U+EC82)
	glyphGrok   = "\U00100001" // Atelier Logos: Grok, supplementary PUA-B
	glyphCodex  = "\uec81"     // nf-cod-openai (U+EC81)
)

// agentColor is agent k's brand colour.
func agentColor(k agent.Kind) lipgloss.Color {
	switch k {
	case agent.Codex:
		return cCodex
	case agent.Grok:
		return cText
	}
	return cClaude
}

// agentGlyph is agent k's icon in its brand colour.
func agentGlyph(k agent.Kind) string {
	g := glyphClaude
	switch k {
	case agent.Codex:
		g = glyphCodex
	case agent.Grok:
		g = glyphGrok
	}
	return lipgloss.NewStyle().Foreground(agentColor(k)).Render(g)
}

// countBadge renders "N<icon>" in colour c, or "" when n<=0 (a zero count is
// left off the row rather than shown as noise).
func countBadge(n int, icon string, c lipgloss.Color) string {
	if n <= 0 {
		return ""
	}
	// The count is right-aligned in a fixed field: a two-digit badge is a cell
	// wider than a one-digit one, and since the badges are a right-aligned block,
	// that shifted every icon to its left and the columns sawtoothed down the
	// list. A three-digit count widens its own row rather than being cut.
	return lipgloss.NewStyle().Foreground(c).Render(fmt.Sprintf("%*d%s", countWidth, n, icon))
}

// countWidth is the fixed cell a badge's number occupies.
const countWidth = 2

// prStateIcon is the GitHub Octicon for a PR state.
func prStateIcon(s core.PRState) string {
	switch s {
	case core.PROpen:
		return iconPROpen
	case core.PRDraft:
		return iconPRDraft
	case core.PRMerged:
		return iconPRMerged
	default: // closed
		return iconPRClosed
	}
}

// prStateColor is GitHub's colour for a PR state — green open, grey draft,
// purple merged, red closed.
func prStateColor(s core.PRState) lipgloss.Color {
	switch s {
	case core.PROpen:
		return cOpen
	case core.PRDraft:
		return cDraft
	case core.PRMerged:
		return cMerged
	default: // closed
		return cClosed
	}
}

// prGlyph is a PR row's coloured Octicon: its state, or — while it waits in the
// merge queue — the queue's own, in GitHub's amber.
func prGlyph(pr core.PR) string {
	if pr.Queued {
		return lipgloss.NewStyle().Foreground(cPending).Render(iconPRQueued)
	}
	return prStateGlyph(pr.State)
}

// prStateGlyph is the coloured PR-state Octicon.
func prStateGlyph(s core.PRState) string {
	return lipgloss.NewStyle().Foreground(prStateColor(s)).Render(prStateIcon(s))
}
