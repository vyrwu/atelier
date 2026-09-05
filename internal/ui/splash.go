package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

// asciiLogo is the centred wordmark on the home splash.
const asciiLogo = `      _       _ _
  __ _| |_ ___| (_) ___ _ __
 / _` + "`" + ` | __/ _ \ | |/ _ \ '__|
| (_| | ||  __/ | |  __/ |
 \__,_|\__\___|_|_|\___|_|`

// splashKeys is the legend shown under the logo.
var splashKeys = [][2]string{
	{"M-n", "new workspace"},
	{"M-a", "active workspaces"},
	{"M-w", "all workspaces"},
	{"M-r", "pull requests"},
	{"M-t", "worktrees"},
}

// RunHome shows the landing splash. It runs as the home window's program so
// attaching to atelier lands on a real screen, not a bare shell.
func RunHome() error {
	m := splashModel{count: len(core.Load().Workspaces)}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type splashModel struct {
	w, h  int
	count int
}

func (m splashModel) Init() tea.Cmd { return nil }

func (m splashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			// Leave atelier without killing the session — the M-* bindings and
			// the workspaces stay live for the next attach.
			_ = tmux.DetachClient()
		case "r":
			m.count = len(core.Load().Workspaces)
		}
	}
	return m, nil
}

func (m splashModel) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	var legend strings.Builder
	for i, k := range splashKeys {
		if i > 0 {
			legend.WriteString("\n")
		}
		legend.WriteString(titleStyle.Render(fmt.Sprintf("%-4s", k[0])) + "  " + textStyle.Render(k[1]))
	}

	footer := fmt.Sprintf("%d workspace", m.count)
	if m.count != 1 {
		footer += "s"
	}
	footer += "  ·  q detach"

	block := lipgloss.JoinVertical(lipgloss.Center,
		logoStyle.Render(asciiLogo),
		"",
		dimStyle.Render("a switchboard for parallel Claude Code sessions"),
		"",
		"",
		lipgloss.NewStyle().Align(lipgloss.Left).Render(legend.String()),
		"",
		"",
		faintStyle.Render(footer),
	)
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, block)
}
