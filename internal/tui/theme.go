package tui

import (
	"hash/fnv"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

// The atelier TUI palette (Dracula). Each picker keeps its OWN accent — the
// per-surface identity the fzf pickers had (M-s red, M-n green, M-r purple,
// M-u yellow, M-; orange, k8s blue, pg teal, …). Color otherwise carries
// meaning (status dots, tag pills, errors).
const (
	ColBg      = lipgloss.Color("#282a36") // background
	ColCurrent = lipgloss.Color("#44475a") // selection / current line
	ColFg      = lipgloss.Color("#f8f8f2") // primary text
	ColSubtle  = lipgloss.Color("#6272a4") // comments / secondary text
	ColCyan    = lipgloss.Color("#8be9fd")
	ColGreen   = lipgloss.Color("#50fa7b")
	ColOrange  = lipgloss.Color("#ffb86c")
	ColPink    = lipgloss.Color("#ff79c6")
	ColPurple  = lipgloss.Color("#bd93f9")
	ColRed     = lipgloss.Color("#ff5555")
	ColYellow  = lipgloss.Color("#f1fa8c")
	ColBlue    = lipgloss.Color("#8be9fd") // k8s (dracula has no pure blue; cyan reads as blue)
	ColTeal    = lipgloss.Color("#5af7c8") // pg
)

// Accent-independent text roles, shared by every surface.
var (
	SubtleStyle = lipgloss.NewStyle().Foreground(ColSubtle)
	BoldStyle   = lipgloss.NewStyle().Foreground(ColFg).Bold(true)
	ErrorStyle  = lipgloss.NewStyle().Foreground(ColRed)
	// SpinnerStyle colors the shared Loader spinner (not tied to any picker).
	SpinnerStyle = lipgloss.NewStyle().Foreground(ColPurple)
)

// Theme carries a picker's accent color. Everything accent-tinted — the title
// chip, filter prompt, selection bar/highlight — derives from it, so a picker
// gets its identity by choosing a Theme.
type Theme struct{ Accent lipgloss.Color }

// NewTheme builds a Theme for an arbitrary accent (used by k8s/aws/pg).
func NewTheme(c lipgloss.Color) Theme { return Theme{Accent: c} }

// Per-picker themes — the accents the fzf pickers used.
func SelectorTheme() Theme { return Theme{Accent: ColOrange} } // M-; ⌘
func SessionsTheme() Theme { return Theme{Accent: ColRed} }    // M-s 栽
func CreatorTheme() Theme  { return Theme{Accent: ColGreen} }  // M-n 製
func RecoverTheme() Theme  { return Theme{Accent: ColPurple} } // M-r 復
func CloneTheme() Theme    { return Theme{Accent: ColYellow} } // M-u 複
func TagTheme() Theme      { return Theme{Accent: ColCyan} }   // M-t 宛

// AccentStyle is the picker's accent as a foreground style.
func (t Theme) AccentStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(t.Accent) }

// Title is the accent title chip (dark text on the accent, padded).
func (t Theme) Title() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ColBg).Background(t.Accent).Bold(true).Padding(0, 1)
}

// Delegate returns the themed two-line default delegate (bold title + subtle
// description) with an accent left-border selection bar.
func (t Theme) Delegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.SetSpacing(0) // denser — more rows fit in a popup
	s := &d.Styles
	s.SelectedTitle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(t.Accent).
		Foreground(t.Accent).Bold(true).
		Padding(0, 0, 0, 1)
	s.SelectedDesc = s.SelectedTitle.Foreground(ColSubtle).Bold(false)
	s.NormalTitle = lipgloss.NewStyle().Foreground(ColFg).Padding(0, 0, 0, 2)
	s.NormalDesc = lipgloss.NewStyle().Foreground(ColSubtle).Padding(0, 0, 0, 2)
	s.DimmedTitle = lipgloss.NewStyle().Foreground(ColSubtle).Padding(0, 0, 0, 2)
	s.DimmedDesc = lipgloss.NewStyle().Foreground(ColSubtle).Padding(0, 0, 0, 2)
	s.FilterMatch = lipgloss.NewStyle().Foreground(t.Accent).Underline(true)
	return d
}

// StyleListChrome applies the picker's accent to a list.Model's own chrome
// (title chip, filter prompt, help).
func (t Theme) StyleListChrome(m *list.Model) {
	a := t.AccentStyle()
	m.Styles.Title = t.Title()
	m.Styles.FilterPrompt = a
	m.Styles.FilterCursor = a
	m.Styles.StatusBar = SubtleStyle
	m.Styles.NoItems = SubtleStyle.Italic(true)
	m.FilterInput.PromptStyle = a
	m.FilterInput.Cursor.Style = a
	m.Help.Styles.ShortKey = a
	m.Help.Styles.FullKey = a
}

// tagPalette: distinct hues for tag pills; a tag's color is a stable hash of
// its name (order-independent, cross-machine).
var tagPalette = []lipgloss.Color{
	ColRed, ColOrange, ColYellow, ColGreen, ColCyan, ColPurple, ColPink,
}

// TagPill renders a tag as a rounded chip in its stable hashed color. "" empty.
func TagPill(tag string) string {
	if tag == "" {
		return ""
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(tag))
	c := tagPalette[h.Sum32()%uint32(len(tagPalette))]
	return lipgloss.NewStyle().Foreground(ColBg).Background(c).Padding(0, 1).Render(tag)
}
