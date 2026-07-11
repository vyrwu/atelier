package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/vyrwu/atelier/internal/integration"
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
	fmt.Fprintln(out, "\x1b[1;36m  Essentials\x1b[0m")
	for _, s := range essentialShortcuts() {
		fmt.Fprintf(out, "    \x1b[1;32m%-4s\x1b[0m %s\n", s.key, s.desc)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "\x1b[1;36m  Health\x1b[0m")
	for _, d := range runDiagnostics() {
		fmt.Fprintf(out, "    %s %s\n", d.icon(), d.message)
		if d.hint != "" {
			fmt.Fprintf(out, "        \x1b[2m→ %s\x1b[0m\n", d.hint)
		}
	}
}

type shortcut struct{ key, desc string }

// essentialShortcuts is the whole atelier loop in a handful of chords,
// each explained in verbs. Deliberately curated, NOT generated from tool
// manifests: the cheatsheet teaches the core loop; it does not enumerate
// every tool's key. M-; already lists the tools, and each picker shows its
// own context keys in its footer — so no plugin/tool names belong here.
// These are the kernel's stable bindings (workspaces + tool selector +
// session), the ones a user must know to drive the tool.
func essentialShortcuts() []shortcut {
	return []shortcut{
		{"M-n", "New workspace — describe a task; the agent names the branch"},
		{"M-s", "Switch workspace — picker shows recap + git freshness"},
		{"M-r", "Recover a recently closed workspace"},
		{"M-;", "Tools — open any tool in the current workspace"},
		{"M-q", "Detach — server keeps running; reattach with `atelier`"},
		{"M-?", "This cheatsheet"},
	}
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
			ds = append(ds, diagnostic{1, fmt.Sprintf("%d tool(s) skipped", len(res.Skipped)), "fix the [tools.*] blocks in config.toml (see `atelier tools list`)"})
		}
	}

	ds = append(ds, integrationDiagnostics(integration.Active())...)

	// The github forge adapter shells out to `gh`; without it the adapter
	// degrades to a blank badge silently. Surface the missing dependency so
	// the user knows why PR badges are absent (main did this via the ghpr
	// tool's Requires; the integration isn't a discovered tool, so probe here).
	if integration.Active().Forge != nil {
		if _, err := exec.LookPath("gh"); err != nil {
			ds = append(ds, diagnostic{2, "forge: gh MISSING", "the github forge adapter needs the gh CLI — install gh + `gh auth login`, or PR badges stay blank"})
		}
	}

	return ds
}

// integrationDiagnostics reports the active kernel-capability adapters. A nil
// capability is intentional (disabled in config): AI defaults to claude, forge
// defaults off — so a missing forge is a hint, not an error. Pure: takes the
// resolved set so it's testable without touching global state.
func integrationDiagnostics(set integration.Set) []diagnostic {
	var ds []diagnostic
	if set.AI != nil {
		ds = append(ds, diagnostic{0, "AI integration: " + set.AI.Name(), ""})
	}
	if set.Forge != nil {
		ds = append(ds, diagnostic{0, "forge integration: " + set.Forge.Name(), ""})
	} else {
		ds = append(ds, diagnostic{1, "forge integration: off", `set [integrations] forge = "github" in config.toml for PR badges`})
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
