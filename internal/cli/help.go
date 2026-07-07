package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/vyrwu/atelier/internal/plugin"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// CheatsheetCommand renders the M-? popup: top-level atelier keybindings
// plus a runtime diagnostics section (consolidated cheatsheet + doctor).
//
// `atelier doctor` CLI command remains separate for scripting / non-tty use.
func CheatsheetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "cheatsheet",
		Short: "Show atelier keybindings + runtime diagnostics (the M-? popup)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			renderCheatsheet(out)
			if term.IsTerminal(int(os.Stdout.Fd())) {
				fmt.Fprintln(out, "\n\x1b[2m  any key dismisses\x1b[0m")
				_, _ = readAnyKey()
			}
			return nil
		},
	}
}

func renderCheatsheet(out io.Writer) {
	fmt.Fprintln(out, "\x1b[1;36m  Keybindings\x1b[0m")
	rows := topLevelBindings()
	for _, r := range rows {
		fmt.Fprintf(out, "    \x1b[1;32m%-6s\x1b[0m %-28s \x1b[2m(%s)\x1b[0m\n",
			r.key, r.action, r.source)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "\x1b[1;36m  Diagnostics\x1b[0m")
	for _, d := range runDiagnostics() {
		fmt.Fprintf(out, "    %s %s\n", d.icon(), d.message)
		if d.hint != "" {
			fmt.Fprintf(out, "        \x1b[2m→ %s\x1b[0m\n", d.hint)
		}
	}
}

type bindingRow struct{ key, action, source string }

func topLevelBindings() []bindingRow {
	rows := []bindingRow{
		{"M-?", "Cheatsheet (this popup)", "atelier"},
		{"M-q", "Detach (server keeps running)", "atelier"},
	}
	res, err := plugin.Discover()
	if err == nil {
		for _, p := range res.Plugins {
			for _, b := range p.Manifest.AllBindings() {
				if b.Key == "" {
					continue
				}
				action := b.Title
				if action == "" && p.Manifest.UI != nil {
					action = p.Manifest.UI.PopupTitle
				}
				if action == "" {
					action = p.Manifest.Name
				}
				rows = append(rows, bindingRow{b.Key, action, p.Manifest.Name})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	return rows
}

// diagnostic is one row in the Diagnostics section.
type diagnostic struct {
	status  int // 0 ok, 1 warn, 2 error
	message string
	hint    string
}

func (d diagnostic) icon() string {
	switch d.status {
	case 0:
		return "\x1b[32m✓\x1b[0m"
	case 1:
		return "\x1b[33m⚠\x1b[0m"
	default:
		return "\x1b[31m✗\x1b[0m"
	}
}

func runDiagnostics() []diagnostic {
	var ds []diagnostic

	h := tmuxhost.New("")
	if v, err := h.Version(); err == nil {
		ds = append(ds, diagnostic{0, v, ""})
	} else {
		ds = append(ds, diagnostic{2, "tmux MISSING", err.Error()})
	}

	if path, err := exec.LookPath("fzf"); err == nil {
		ver := fzfVersion(path)
		ds = append(ds, diagnostic{0, "fzf " + ver, ""})
	} else {
		ds = append(ds, diagnostic{2, "fzf MISSING", "install via brew/nix"})
	}

	res, perr := plugin.Discover()
	if perr != nil {
		ds = append(ds, diagnostic{2, "plugin discovery failed", perr.Error()})
	} else {
		ds = append(ds, diagnostic{0, fmt.Sprintf("%d tools discovered", len(res.Plugins)), ""})
		for _, p := range res.Plugins {
			for _, req := range p.Manifest.Requires {
				if _, err := exec.LookPath(req); err != nil {
					ds = append(ds, diagnostic{2, fmt.Sprintf("%s missing %s", p.Name, req), "install " + req})
				}
			}
		}
		if len(res.Skipped) > 0 {
			ds = append(ds, diagnostic{1, fmt.Sprintf("%d plugin(s) skipped", len(res.Skipped)), "check $PATH for atelier-* binaries"})
		}
	}

	settingsPath := filepath.Join(os.Getenv("HOME"), ".cache/atelier/claude/settings.json")
	if _, err := os.Stat(settingsPath); err == nil {
		ds = append(ds, diagnostic{0, "claude settings wired", ""})
	} else {
		ds = append(ds, diagnostic{1, "claude settings file missing", settingsPath})
	}

	return ds
}

func fzfVersion(path string) string {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "?"
	}
	fields := strings.Fields(string(out))
	if len(fields) > 0 {
		return fields[0]
	}
	return "?"
}

// readAnyKey puts stdin in raw mode and waits for a single keypress
// so the M-? popup blocks until dismissed. tmux's display-popup -E
// closes the popup as soon as the program exits; the read is what
// holds the popup open.
func readAnyKey() (int, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return 0, nil
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return 0, err
	}
	defer func() { _ = term.Restore(fd, state) }()
	buf := make([]byte, 8)
	n, _ := os.Stdin.Read(buf)
	return n, nil
}
