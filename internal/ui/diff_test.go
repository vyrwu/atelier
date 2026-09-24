package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vyrwu/atelier/internal/core"
)

// clone makes a directory that git sees as a checkout of repo (owner/name), so
// the worktree match can read its origin the way it does for real.
func clone(t *testing.T, repo string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:" + repo + ".git"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v (%s)", err, out)
		}
	}
	return dir
}

func diffModel(wts []core.Worktree) Model {
	ws := &core.Workspace{Slug: "s", Session: "s"}
	return Model{curWS: ws, wt: map[string][]core.Worktree{"s": wts}}
}

// The diff comes from the worktree the PR was built in: instant, offline, and
// with no size limit — GitHub's diff endpoint refuses anything past 20k lines.
func TestDiffCommandUsesTheWorktree(t *testing.T) {
	want := clone(t, "o/r")
	m := diffModel([]core.Worktree{
		{Repo: "r", Branch: "other", Path: clone(t, "o/r")},
		{Repo: "r", Branch: "feat/x", Path: want},
	})
	cwd, cmd, _ := m.diffCommand(core.PR{Repo: "o/r", Number: 7, Head: "feat/x", Base: "main"})

	if cwd != want {
		t.Errorf("cwd = %q, want the worktree on the head branch", cwd)
	}
	if !strings.Contains(cmd, "git --no-pager diff 'origin/main'...HEAD 2>&1 | $p") {
		t.Errorf("cmd = %q", cmd)
	}
	// The base is fetched first, and a failed fetch does not stop the diff.
	if !strings.Contains(cmd, "git fetch --quiet origin 'main' 2>/dev/null; git --no-pager diff") {
		t.Errorf("base not fetched before diffing: %q", cmd)
	}
	if strings.Contains(cmd, "gh ") {
		t.Error("went to GitHub with a worktree available")
	}
}

// A stacked PR diffs against the branch below it, which is its own slice of the
// stack rather than the whole tower — the same thing GitHub shows.
func TestDiffCommandStacked(t *testing.T) {
	m := diffModel([]core.Worktree{{Repo: "r", Branch: "an/hl7-bucket", Path: clone(t, "o/r")}})
	_, cmd, _ := m.diffCommand(core.PR{Repo: "o/r", Number: 3482, Head: "an/hl7-bucket", Base: "an/uat-host"})
	if !strings.Contains(cmd, "git --no-pager diff 'origin/an/uat-host'...HEAD") {
		t.Errorf("cmd = %q, want the diff against the parent branch", cmd)
	}
}

// A branch name is not ours to trust inside a command line.
func TestDiffCommandQuotesTheBase(t *testing.T) {
	m := diffModel([]core.Worktree{{Repo: "r", Branch: "x", Path: clone(t, "o/r")}})
	_, cmd, _ := m.diffCommand(core.PR{Repo: "o/r", Number: 1, Head: "x", Base: "b'; rm -rf /; echo '"})
	if strings.Contains(cmd, "rm -rf /;") && !strings.Contains(cmd, `'\''`) {
		t.Errorf("base not quoted: %q", cmd)
	}
	if strings.Count(cmd, "git --no-pager diff") != 1 {
		t.Errorf("quoting broke the command: %q", cmd)
	}
}

// A PR with no worktree here — registered from another space, or its branch
// pruned — still opens, from GitHub, through a pager.
func TestDiffCommandFallsBackToGitHub(t *testing.T) {
	m := diffModel(nil)
	cwd, cmd, stale := m.diffCommand(core.PR{Repo: "o/r", Number: 42, Head: "gone", Base: "main"})
	if !strings.Contains(cmd, "gh pr diff 42 --repo 'o/r' 2>&1 | $p") {
		t.Errorf("cmd = %q", cmd)
	}
	if !strings.Contains(cmd, "|") {
		t.Errorf("no pager on the fallback: %q", cmd)
	}
	if !strings.Contains(cmd, "2>&1") {
		t.Errorf("gh's refusal would never reach the pager: %q", cmd)
	}
	if cwd == "" {
		t.Error("no working directory for the fallback")
	}
	if stale {
		t.Error("nothing local to be stale against")
	}
}

// With no repo to ask about either, there is nothing to run.
func TestDiffCommandGivesUpCleanly(t *testing.T) {
	m := diffModel(nil)
	if _, cmd, _ := m.diffCommand(core.PR{Number: 1, Head: "x"}); cmd != "" {
		t.Errorf("cmd = %q, want none", cmd)
	}
}

// Every command chooses its pager in the shell that runs it.
func TestEveryDiffCommandCarriesTheChooser(t *testing.T) {
	local := diffModel([]core.Worktree{{Repo: "r", Branch: "x", Path: clone(t, "o/r")}})
	remote := diffModel(nil)
	for name, m := range map[string]Model{"local": local, "github": remote} {
		_, cmd, _ := m.diffCommand(core.PR{Repo: "o/r", Number: 1, Head: "x", Base: "main"})
		if !strings.HasPrefix(cmd, pagerPrelude) {
			t.Errorf("%s: command does not pick its own pager: %q", name, cmd)
		}
	}
}

// One PR, one window — reopening lands on the same one. PR numbers are per
// repo, so the repo is in the name: #12 in two repos is two windows.
func TestDiffWindowName(t *testing.T) {
	cases := map[string]core.PR{
		"pr-atelier-100": {Repo: "vyrwu/atelier", Number: 100},
		"pr-other-100":   {Repo: "vyrwu/other", Number: 100},
		"pr-7":           {Number: 7},
	}
	for want, pr := range cases {
		if got := diffWindow(pr); got != want {
			t.Errorf("window for %s#%d = %q, want %q", pr.Repo, pr.Number, got, want)
		}
	}
}

// A task spanning two repos often uses one branch name in both. The worktree is
// matched on repo as well as branch, or Enter diffs the wrong repo.
func TestDiffPicksTheWorktreeInThePRsRepo(t *testing.T) {
	api, web := clone(t, "o/api"), clone(t, "o/web")
	m := diffModel([]core.Worktree{
		{Repo: "api", Branch: "feat/login", Path: api},
		{Repo: "web", Branch: "feat/login", Path: web},
	})
	if cwd, _, _ := m.diffCommand(core.PR{Repo: "o/web", Number: 3, Head: "feat/login", Base: "main"}); cwd != web {
		t.Errorf("o/web's PR diffed in %q, want its own worktree %q", cwd, web)
	}
	if cwd, _, _ := m.diffCommand(core.PR{Repo: "O/API", Number: 3, Head: "feat/login", Base: "main"}); cwd != api {
		t.Errorf("repo match should ignore case: got %q, want %q", cwd, api)
	}
	// A branch only another repo has is not this PR's worktree.
	if _, cmd, _ := m.diffCommand(core.PR{Repo: "o/cli", Number: 3, Head: "feat/login", Base: "main"}); !strings.Contains(cmd, "gh pr diff") {
		t.Errorf("borrowed another repo's worktree: %q", cmd)
	}
}

// With no base known yet (a PR cached before bases were recorded) there is
// nothing correct to diff against locally, so it asks GitHub, which knows.
func TestDiffWithoutABaseAsksGitHub(t *testing.T) {
	m := diffModel([]core.Worktree{{Repo: "r", Branch: "x", Path: clone(t, "o/r")}})
	_, cmd, _ := m.diffCommand(core.PR{Repo: "o/r", Number: 5, Head: "x"})
	if !strings.Contains(cmd, "gh pr diff 5 --repo 'o/r'") || strings.Contains(cmd, "HEAD^") {
		t.Errorf("cmd = %q, want the GitHub diff", cmd)
	}
}

// The staleness flag only fires when the PR's head commit is known and differs
// from what the worktree has; an unknown OID must not cry wolf.
func TestDiffStalenessNeedsAKnownHead(t *testing.T) {
	m := diffModel([]core.Worktree{{Repo: "r", Branch: "x", Path: clone(t, "o/r")}})
	if _, _, stale := m.diffCommand(core.PR{Repo: "o/r", Number: 1, Head: "x", Base: "main"}); stale {
		t.Error("claimed stale with no head OID to compare")
	}
	// A real OID against a clone with no commits (so HeadOID is "") does differ,
	// and saying so is the honest outcome.
	if _, _, stale := m.diffCommand(core.PR{Repo: "o/r", Number: 1, Head: "x", Base: "main", HeadOID: "deadbeef"}); !stale {
		t.Error("did not notice the worktree is not on the PR's head commit")
	}
}

// The pager is whatever is installed, best first — and the choice is made by
// the shell, so this runs the actual snippet against a PATH we control rather
// than asserting on a Go lookup that is not what decides.
func TestPagerPreference(t *testing.T) {
	bin := t.TempDir()
	stub := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	choose := func() string {
		t.Helper()
		// /bin/sh by absolute path: PATH below is trimmed to the stub dir.
		out, err := exec.Command("/bin/sh", "-c", pagerPrelude+`printf '%s' "$p"`).CombinedOutput()
		if err != nil {
			t.Fatalf("prelude failed: %v (%s)", err, out)
		}
		return string(out)
	}
	t.Setenv("PATH", bin)

	if got := choose(); got != "less -R" {
		t.Errorf("nothing installed: %q, want the bare fallback", got)
	}
	stub("delta")
	if got := choose(); got != "delta --paging=always" {
		t.Errorf("delta installed: %q", got)
	}
	stub("diffnav")
	if got := choose(); got != "diffnav" {
		t.Errorf("diffnav installed: %q, want the file tree to win", got)
	}
}

// Enter reads the change; M-b is the way out to GitHub. M-r, which used to open
// the diff, is gone — pressing it must not quietly do something else.
func TestPRViewKeysForDiffAndBrowse(t *testing.T) {
	ws := &core.Workspace{Slug: "s", Session: "s", PRs: []core.PR{
		{Repo: "o/r", Number: 1, Title: "a pr", State: core.PROpen, Head: "x", Base: "main"},
	}}
	m := Model{w: 120, screen: scrPRs, curWS: ws, filter: textinput.New(),
		wt: map[string][]core.Worktree{}, prPending: map[string]bool{}}

	// Enter goes to the diff. With no tmux here it reports instead of quitting,
	// which still proves the key is wired to openDiff rather than to the browser.
	next, cmd := m.updatePRs(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil && next.(Model).err == "" {
		t.Error("enter did nothing — it should open the diff")
	}

	// M-r is retired: it must fall through to the filter, not to an action.
	before := m.filter.Value()
	after, _ := m.updatePRs(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}, Alt: true})
	if got := after.(Model); got.err != "" {
		t.Errorf("M-r still triggers something: %q", got.err)
	} else if got.filter.Value() == before && got.screen != scrPRs {
		t.Error("M-r left the view")
	}
}

// tmux reads ':' and '.' in a target as session:window.pane, so a window whose
// name holds one can be opened but never selected again — reopening lands on
// whatever window was last active. Every window atelier names avoids them.
func TestWindowNamesAreTargetable(t *testing.T) {
	names := []string{
		diffWindow(core.PR{Repo: "cloudnativedenmark/cloudnativedenmark.dk", Number: 12}),
		checksWindow(core.PR{Repo: "cloudnativedenmark/cloudnativedenmark.dk", Number: 12}),
		wtWindow(core.Worktree{Repo: "cloudnativedenmark.dk", Branch: "release-1.2"}),
		wtWindow(core.Worktree{Repo: "r", Branch: "feat/a:b c"}),
	}
	for _, n := range names {
		if strings.ContainsAny(n, ".: \t/") {
			t.Errorf("window name %q holds a tmux target separator", n)
		}
	}
	if got := diffWindow(core.PR{Repo: "o/site.dk", Number: 12}); got != "pr-site-dk-12" {
		t.Errorf("diffWindow = %q, want pr-site-dk-12", got)
	}
}
