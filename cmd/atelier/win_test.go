package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

// A window is picked by its whole name. tmux would take "pr" for
// "pr-atelier-128", and the verb that follows may be kill.
func TestFindWindowMatchesTheWholeName(t *testing.T) {
	wins := []tmux.Window{{ID: "@1", Name: "claude"}, {ID: "@2", Name: "pr-atelier-128"}}
	if _, n := findWindow(wins, "pr"); n != 0 {
		t.Error("a prefix picked a window")
	}
	if w, n := findWindow(wins, "pr-atelier-128"); n != 1 || w.ID != "@2" {
		t.Errorf("exact name = %+v, %d", w, n)
	}
	twins := append(wins, tmux.Window{ID: "@3", Name: "claude"})
	if _, n := findWindow(twins, "claude"); n != 2 {
		t.Errorf("two windows with one name counted as %d; the caller must refuse", n)
	}
	if got := windowNames(wins); !slices.Equal(got, []string{"agent", "pr-atelier-128"}) {
		t.Errorf("names = %v; the agent's window goes by agent", got)
	}
}

// Flags may come before or after the window, as people type them.
func TestWinFlagsInAnyPosition(t *testing.T) {
	for _, args := range [][]string{{"shell", "-s", "spry-otter"}, {"--space", "spry-otter", "shell"}, {"--space=spry-otter", "shell"}} {
		space, _, pos, ok, err := winFlags("kill", args)
		if !ok || err != nil || space != "spry-otter" || !slices.Equal(pos, []string{"shell"}) {
			t.Errorf("%v: space=%q pos=%v ok=%v err=%v", args, space, pos, ok, err)
		}
	}
	for _, c := range []struct {
		verb string
		args []string
	}{
		{"kill", nil},                            // nothing to kill
		{"restart", []string{"shell", "claude"}}, // one at a time
		{"ls", []string{"shell"}},                // ls takes no window
		{"kill", []string{"--all", "shell"}},     // --all is ls's alone
	} {
		if _, _, _, ok, err := winFlags(c.verb, c.args); ok || err == nil {
			t.Errorf("win %s %v: accepted", c.verb, c.args)
		}
	}
	if _, _, _, ok, err := winFlags("ls", []string{"-a", "-s", "x"}); ok || err == nil {
		t.Error("ls took both --all and --space")
	}
	if _, all, _, ok, _ := winFlags("ls", []string{"-a"}); !ok || !all {
		t.Error("ls -a refused")
	}
}

// The default space is the one the caller is in; a named one must exist and
// be out of the Trash. Nothing guesses when neither says.
func TestSpaceFor(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "by-cwd", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATELIER_ROOT", root)
	t.Setenv("TMUX_PANE", "")
	st := &core.State{Workspaces: []core.Workspace{
		{Slug: "by-env", Session: "by-env"}, {Slug: "by-cwd", Session: "by-cwd"},
		{Slug: "binned", Session: "binned", Retired: true},
	}}
	t.Chdir(filepath.Join(root, "by-cwd", "repo"))
	t.Setenv("ATELIER_SLUG", "by-env")
	if w, err := spaceFor(st, ""); err != nil || w.Slug != "by-env" {
		t.Errorf("env and cwd disagree: got %v, %v; want the environment's", w, err)
	}
	t.Setenv("ATELIER_SLUG", "binned")
	if w, err := spaceFor(st, ""); err != nil || w.Slug != "by-cwd" {
		t.Errorf("a trashed space in the environment: got %v, %v; want the directory's", w, err)
	}
	t.Setenv("ATELIER_SLUG", "")
	t.Chdir(t.TempDir())
	if _, err := spaceFor(st, ""); err == nil {
		t.Error("outside every space: picked one")
	}
	if w, err := spaceFor(st, "by-cwd"); err != nil || w.Slug != "by-cwd" {
		t.Errorf("named: %v, %v", w, err)
	}
	for _, slug := range []string{"nope", "binned"} {
		if _, err := spaceFor(st, slug); err == nil {
			t.Errorf("--space %s: accepted", slug)
		}
	}
}

// --all shows spaces' windows only: not the splash, not the Trash.
func TestSpaceWindowsKeepsActiveSpaces(t *testing.T) {
	st := &core.State{Workspaces: []core.Workspace{{Slug: "live", Session: "live"}, {Slug: "binned", Session: "binned", Retired: true}}}
	got := spaceWindows(st, []tmux.Window{{Session: "home"}, {Session: "live", Name: "shell"}, {Session: "binned"}})
	if len(got) != 1 || got[0].Session != "live" {
		t.Errorf("windows = %+v", got)
	}
}

func TestWinTable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Unix(1_000_000, 0)
	wins := []tmux.Window{
		{Session: "live", Name: "claude", Active: true, Command: "claude", Path: home + "/ateliers/live", Activity: now},
		{Session: "live", Name: "shell", Command: "ssh", Path: home + "-other", Activity: now.Add(-42 * time.Minute)},
		{Session: "live", Name: "pr-atelier-128", Dead: true, Command: "less", Path: "/", Activity: now.Add(-3 * time.Hour)},
	}
	var b bytes.Buffer
	if err := winTable(&b, wins, true, now); err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(b.String()), "\n")
	want := [][]string{
		{"SPACE", "WINDOW", "RUNNING", "QUIET", "DIR"},
		{"live", "agent", "*", "claude", "0m", "~/ateliers/live"},
		{"live", "shell", "ssh", "42m", home + "-other"}, // a sibling of home is not under it
		{"live", "pr-atelier-128", "(exited)", "3h", "/"},
	}
	for i, w := range want {
		if i >= len(rows) || !slices.Equal(strings.Fields(rows[i]), w) {
			t.Errorf("row %d = %q; want %v", i, rows, w)
		}
	}
	b.Reset()
	_ = winTable(&b, wins[:1], false, now)
	if strings.Contains(b.String(), "SPACE") {
		t.Error("one space's table names the space on every row")
	}
}

// A process that shrugs off the hangup is the one a hung window is made of.
// Past the grace it is asked to terminate, and killed if it won't; one that
// has left is not waited on.
func TestReapEscalates(t *testing.T) {
	const grace = 200 * time.Millisecond
	for _, c := range []struct {
		traps string
		want  syscall.Signal
	}{
		{`trap "" HUP`, syscall.SIGTERM},
		{`trap "" HUP TERM`, syscall.SIGKILL},
	} {
		p := exec.Command("sh", "-c", c.traps+`; echo ready; while :; do sleep 1; done`)
		out, err := p.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Start(); err != nil {
			t.Fatal(err)
		}
		// Not before the traps are set: a signal that lands first ends it early.
		if _, err := bufio.NewReader(out).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		gone := make(chan struct{})
		go func() { _ = p.Wait(); close(gone) }()
		start := time.Now()
		reap([]int{p.Process.Pid}, grace)
		select {
		case <-gone:
		case <-time.After(5 * time.Second):
			_ = p.Process.Kill()
			t.Fatalf("%s: still running after reap", c.traps)
		}
		if ws, ok := p.ProcessState.Sys().(syscall.WaitStatus); !ok || ws.Signal() != c.want {
			t.Errorf("%s: ended by %v; want %v", c.traps, p.ProcessState, c.want)
		}
		if d := time.Since(start); d < grace {
			t.Errorf("%s: signalled after %v, inside the grace", c.traps, d)
		}
		start = time.Now()
		reap([]int{p.Process.Pid}, 5*time.Second) // already gone
		if d := time.Since(start); d > time.Second {
			t.Errorf("waited %v on a process that had left", d)
		}
	}
}

// A pane's processes are its own group and its terminal's foreground job —
// not a job it sent to the background, not this process, and nothing at all
// when the terminal is no longer the pane's.
func TestPickProcs(t *testing.T) {
	ps := `  100   100   300
  101   100   300
  200   200   300
  300   300   300
  301   300   300
  400   300   300
`
	// 100: the pane's shell; 101: its child; 200: a background job;
	// 300/301: the foreground job; 400: this process, in it.
	if got := pickProcs(ps, 100, 400); !slices.Equal(got, []int{100, 101, 300, 301}) {
		t.Errorf("procs = %v", got)
	}
	if got := pickProcs(ps, 999, 400); got != nil {
		t.Errorf("terminal not the pane's, yet procs = %v", got)
	}
	if got := pickProcs("  1  1  0\n  50  1  0\n", 50, 400); !slices.Equal(got, []int{50}) {
		t.Errorf("init made it in: %v", got)
	}
}

// A mistyped verb fails; it is not taken for a session name and waved through.
func TestWinRejectsUnknownVerbs(t *testing.T) {
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devnull.Close() }()
	stderr := os.Stderr
	os.Stderr = devnull
	defer func() { os.Stderr = stderr }()
	for _, args := range [][]string{{"list"}, {"list", "shell"}, {"spry-otter"}, {"spry-otter", "logs"}} {
		if err := win(args); err == nil {
			t.Errorf("win %v: accepted", args)
		}
	}
}
