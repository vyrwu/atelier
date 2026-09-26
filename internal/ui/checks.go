package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
)

// openChecks shows a PR's CI in a window of the space: checks, jobs, steps,
// and logs, through gh-enhance when it is installed.
func (m Model) openChecks(pr core.PR) (tea.Model, tea.Cmd) {
	if m.curWS == nil || pr.Repo == "" || pr.Number == 0 {
		return m, nil
	}
	return m.openWindow(checksWindow(pr), m.curWS.Root(), checksCommand(pr), "")
}

// checksPrelude finds gh-enhance, setting $c to the way to run it or to nothing.
// It is tried as a binary first — installed from a package manager it sits on
// PATH but isn't registered with gh, so `gh enhance` fails — then as a gh
// extension. Decided by the shell that runs it, as with the diff pager.
const checksPrelude = `if command -v gh-enhance >/dev/null 2>&1; then c='gh-enhance'; ` +
	`elif gh extension list 2>/dev/null | grep -q 'gh-enhance'; then c='gh enhance'; ` +
	`else c=''; fi; `

// checksCommand opens the PR in gh-enhance, or without it falls back to gh's own
// live checks table. That has no logs, and exits when the checks finish, so the
// window waits rather than taking the result with it.
func checksCommand(pr core.PR) string {
	url := pr.URL
	if url == "" {
		url = fmt.Sprintf("https://github.com/%s/pull/%d", pr.Repo, pr.Number)
	}
	return checksPrelude + fmt.Sprintf(
		`if [ -n "$c" ]; then $c %s; else gh pr checks %d --repo %s --watch; printf '\n(press Enter to close) '; read _; fi`,
		shellQuote(url), pr.Number, shellQuote(pr.Repo))
}

// checksWindow names a PR's CI window, per repo as the diff window is.
func checksWindow(pr core.PR) string {
	if _, name, ok := strings.Cut(pr.Repo, "/"); ok && name != "" {
		return sanitizeWindow(fmt.Sprintf("ci-%s-%d", name, pr.Number))
	}
	return fmt.Sprintf("ci-%d", pr.Number)
}
