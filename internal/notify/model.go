package notify

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dismissAfter is how long a toast lingers before auto-closing. Any key
// dismisses it sooner.
const dismissAfter = 2500 * time.Millisecond

type timeoutMsg struct{}

// toast is the bubbletea model: a bordered, colored box centered on screen,
// dismissed by any key or a timeout.
type toast struct {
	kind          Kind
	message       string
	width, height int
}

func newToast(kind Kind, message string) toast {
	return toast{kind: kind, message: message}
}

func (t toast) Init() tea.Cmd {
	return tea.Tick(dismissAfter, func(time.Time) tea.Msg { return timeoutMsg{} })
}

func (t toast) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case timeoutMsg:
		return t, tea.Quit
	case tea.KeyMsg:
		return t, tea.Quit
	case tea.WindowSizeMsg:
		t.width, t.height = msg.Width, msg.Height
		return t, nil
	}
	return t, nil
}

func (t toast) View() string {
	icon, color := toastStyle(t.kind)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(color)).
		Foreground(lipgloss.Color(color)).
		Padding(0, 2).
		Render(icon + "  " + t.message)
	if t.width > 0 && t.height > 0 {
		return lipgloss.Place(t.width, t.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

// toastStyle maps a Kind to its icon + 256-color accent.
func toastStyle(k Kind) (icon, color string) {
	switch k {
	case Error:
		return "✗", "203" // red
	case Success:
		return "✓", "35" // green
	default:
		return "ℹ", "111" // blue
	}
}
