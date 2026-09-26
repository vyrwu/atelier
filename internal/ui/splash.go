package ui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

// asciiLogo is the wordmark on the home splash and the README's header. Both
// rows are the same width, so centring keeps the letters stacked.
const asciiLogo = "▄▀█ ▀█▀ █▀▀ █   █ █▀▀ █▀█\n" +
	"█▀█  █  ██▄ █▄▄ █ ██▄ █▀▄"

// keyGroup is one column of the splash's keymap.
type keyGroup struct {
	title string
	keys  [][2]string // {key, label}
}

// splashKeys is the whole keymap — the splash is the one place it is written
// down, so the individual views carry only their own legend. Grouped by where
// the keys work.
var splashKeys = []keyGroup{
	{"anywhere", [][2]string{
		{"M-n", "new space"}, {"M-s", "spaces"}, {"M-t", "trash"}, {"M-h", "home"}, {"M-q", "detach"},
	}},
	{"in a space", [][2]string{
		{"M-a", "agent"}, {"M-c", "command line"}, {"M-p", "pull requests"}, {"M-w", "worktrees"},
	}},
}

// RunHome shows the landing splash: the wordmark, which external tools are
// present (the Doctor), and the keymap. It runs as the home window's program so
// attaching to atelier lands on a real screen, not a bare shell, and M-h brings
// you back to it from anywhere.
func RunHome() error {
	m := splashModel{count: len(core.Load().Workspaces), deps: checkDeps()}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type splashModel struct {
	w, h  int
	count int
	deps  []dep
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
			m.deps = checkDeps()
		}
	}
	return m, nil
}

func (m splashModel) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	// The version sits under the wordmark, and takes the name back when the
	// wordmark doesn't fit.
	logo := []string{logoStyle.Render(asciiLogo), "", dimStyle.Render(Version), ""}
	name := []string{dimStyle.Render("atelier ") + textStyle.Render(Version), ""}
	head := []string{dimStyle.Render("a workshop for parallel Claude Code agents"), "", ""}
	keys := []string{keysView(), "", ""}
	deps := []string{m.depsView(), "", ""}
	footer := []string{faintStyle.Render(plural(m.count, "workspace") + "  ·  q detach")}

	// On a short terminal the wordmark goes first, then the dependency check, so
	// the keymap stays on screen instead of the top being cut off.
	var block string
	for _, parts := range [][][]string{
		{logo, head, keys, deps, footer},
		{name, head, keys, deps, footer},
		{name, head, keys, footer},
		{keys[:1]},
	} {
		block = lipgloss.JoinVertical(lipgloss.Center, slices.Concat(parts...)...)
		if lipgloss.Height(block) <= m.h {
			break
		}
	}
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, block)
}

// keysView lays the keymap out as columns, each headed by where its keys work.
func keysView() string {
	cols := make([]string, len(splashKeys))
	for i, g := range splashKeys {
		lines := []string{faintStyle.Render(g.title)}
		for _, kv := range g.keys {
			lines = append(lines, titleStyle.Render(kv[0])+"  "+textStyle.Render(kv[1]))
		}
		col := lipgloss.NewStyle()
		if i < len(splashKeys)-1 {
			col = col.PaddingRight(6)
		}
		cols[i] = col.Render(strings.Join(lines, "\n"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cols...)
}

// depsView is the dependency check: what atelier needs on one line, the
// optional viewers it hands PRs to on the next. A missing requirement is red, a
// missing option only faint.
func (m splashModel) depsView() string {
	var need, opt []string
	for _, d := range m.deps {
		var s string
		switch {
		case d.ok:
			s = lipgloss.NewStyle().Foreground(cOpen).Render(iconCheck) + " " + textStyle.Render(d.name)
			if d.version != "" {
				s += " " + dimStyle.Render(majorMinor(d.version))
			}
		case d.optional:
			s = faintStyle.Render(iconCIFail + " " + d.name + " not found")
		default:
			s = lipgloss.NewStyle().Foreground(cClosed).Render(iconCIFail + " " + d.name + " not found")
		}
		if d.optional {
			opt = append(opt, s)
		} else {
			need = append(need, s)
		}
	}
	var lines []string
	for _, parts := range [][]string{need, opt} {
		if line := strings.Join(parts, "   "); visibleWidth(line) <= m.w {
			lines = append(lines, line)
		} else {
			lines = append(lines, parts...)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Center, lines...)
}

// majorMinor trims a version to what matters at a glance: "2.50.1" → "2.50".
func majorMinor(v string) string {
	if parts := strings.SplitN(v, ".", 3); len(parts) == 3 {
		return parts[0] + "." + parts[1]
	}
	return v
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// dep is one external dependency's health.
type dep struct {
	name     string
	ok       bool
	version  string
	optional bool
}

var versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// checkDeps probes the external tools atelier drives and returns each one's
// presence + version (the Doctor part of the splash).
func checkDeps() []dep {
	probes := []struct {
		name     string
		args     []string
		optional bool
	}{
		{"git", []string{"--version"}, false},
		{"gh", []string{"--version"}, false},
		{"tmux", []string{"-V"}, false},
		{"claude", []string{"--version"}, false},
		{"diffnav", []string{"--version"}, true},
		{"gh-enhance", []string{"--version"}, true},
	}
	out := make([]dep, 0, len(probes))
	for _, p := range probes {
		if !found(p.name) {
			out = append(out, dep{name: p.name, optional: p.optional})
			continue
		}
		v := ""
		if b, err := exec.Command(p.name, p.args...).Output(); err == nil {
			v = versionRe.FindString(string(b))
		}
		if path, err := exec.LookPath(p.name); v == "" && err == nil {
			if real, err := filepath.EvalSymlinks(path); err == nil {
				v = pathVersion(p.name, real)
			}
		}
		out = append(out, dep{name: p.name, ok: true, version: v, optional: p.optional})
	}
	return out
}

// pathVersion reads a version off where a package manager installed the tool —
// distro builds of the viewers print "version devel", but the path still names
// it: /nix/store/…-diffnav-0.11.0/bin, …/Cellar/diffnav/0.11.0/bin.
func pathVersion(name, path string) string {
	re := regexp.MustCompile(regexp.QuoteMeta(name) + `[-/]v?(\d+\.\d+(?:\.\d+)?)/`)
	if m := re.FindStringSubmatch(path); m != nil {
		return m[1]
	}
	return ""
}

// found reports whether bin can be run. gh-enhance also counts as a gh
// extension, since checksPrelude runs it either way.
func found(bin string) bool {
	if _, err := exec.LookPath(bin); err == nil {
		return true
	}
	if bin != "gh-enhance" {
		return false
	}
	b, _ := exec.Command("gh", "extension", "list").Output()
	return strings.Contains(string(b), "gh-enhance")
}
