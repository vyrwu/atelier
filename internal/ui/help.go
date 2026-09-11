package ui

import (
	"os/exec"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	}
	return m, nil
}

// helpKeys is the full keymap shown in Help — the one place the whole scheme is
// documented, so the individual views can carry only their own legend.
var helpKeys = [][2][2]string{
	// {left {key,label}}, {right {key,label}}
	{{"M-s", "spaces"}, {"M-a", "agent"}},
	{{"M-p", "pull requests"}, {"M-c", "command line"}},
	{{"M-w", "worktrees"}, {"M-n", "new space"}},
	{{"M-t", "trash"}, {"M-h", "help"}},
	{{"", ""}, {"M-q", "detach"}},
}

func (m Model) viewHelp() string {
	var keys strings.Builder
	for i, row := range helpKeys {
		if i > 0 {
			keys.WriteString("\n")
		}
		keys.WriteString(helpCell(row[0]) + "    " + helpCell(row[1]))
	}

	var deps strings.Builder
	deps.WriteString(dimStyle.Render("dependencies") + "\n")
	for _, d := range checkDeps() {
		mark := lipgloss.NewStyle().Foreground(cOpen).Render(iconCheck)
		ver := textStyle.Render(d.version)
		if !d.ok {
			mark = lipgloss.NewStyle().Foreground(cClosed).Render(iconCIFail)
			ver = faintStyle.Render("not found")
		}
		deps.WriteString("  " + mark + "  " + textStyle.Render(padRight(d.name, 7)) + ver + "\n")
	}

	block := lipgloss.JoinVertical(lipgloss.Center,
		logoStyle.Render(asciiLogo),
		"",
		dimStyle.Render("atelier ")+textStyle.Render(Version),
		"",
		"",
		lipgloss.NewStyle().Align(lipgloss.Left).Render(deps.String()),
		"",
		dimStyle.Render("shortcuts")+"\n"+lipgloss.NewStyle().Align(lipgloss.Left).Render(keys.String()),
		"",
		"",
		faintStyle.Render("icons need a Nerd Font  ·  esc to close"),
	)
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, block)
}

// helpCell renders one "M-x  label" keymap cell, padded so the two columns line
// up. An empty key renders as blank space of the same width.
func helpCell(kv [2]string) string {
	if kv[0] == "" {
		return padRight("", 4+16)
	}
	return titleStyle.Render(padRight(kv[0], 4)) + textStyle.Render(padRight(kv[1], 16))
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// dep is one external dependency's health.
type dep struct {
	name    string
	ok      bool
	version string
}

var versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// checkDeps probes the external tools atelier drives and returns each one's
// presence + version (the Doctor part of Help).
func checkDeps() []dep {
	probes := []struct {
		name, bin string
		args      []string
	}{
		{"git", "git", []string{"--version"}},
		{"gh", "gh", []string{"--version"}},
		{"tmux", "tmux", []string{"-V"}},
		{"claude", "claude", []string{"--version"}},
	}
	out := make([]dep, 0, len(probes))
	for _, p := range probes {
		if _, err := exec.LookPath(p.bin); err != nil {
			out = append(out, dep{name: p.name, ok: false})
			continue
		}
		v := ""
		if b, err := exec.Command(p.bin, p.args...).Output(); err == nil {
			line := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)[0]
			if m := versionRe.FindString(line); m != "" {
				v = m
			} else {
				v = line
			}
		}
		out = append(out, dep{name: p.name, ok: true, version: v})
	}
	return out
}
