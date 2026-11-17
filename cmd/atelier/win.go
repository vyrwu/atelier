package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

const usageText = `atelier — a workshop for parallel coding agents

  atelier                               start atelier, or attach to it
  atelier install                       install the agents' hooks and the key bindings
  atelier create "<task>" [--agent A]   start a space for a task
  atelier swap <space> [--to A]         hand a space to another agent
  atelier win ls|restart|kill           a space's windows: list, restart, close
  atelier ls                            list spaces, stalest first
  atelier purge                         delete spaces untouched for a while
  atelier gc                            sweep up what deleted spaces left behind
  atelier version

win, ls, purge, and gc take -h. Inside atelier the keys are on the splash (M-h).
`

const winUsage = `Usage:
  atelier win ls      [-s SPACE | -a]       list a space's windows, or every space's
  atelier win restart <window> [-s SPACE]   kill what a window runs and start it afresh
  atelier win kill    <window> [-s SPACE]   close a window

SPACE defaults to the space you are in. A window goes by the name ls shows;
"agent" is the space's agent, "shell" its command line (M-c).
`

// win is `atelier win`: the verbs a person runs, or else the form the keys
// call, `win <session> <target> [window]` (winNav) — which a server still
// holding an older binary's bindings calls too, so it keeps its shape and is
// what re-syncs them.
func win(args []string) error {
	switch verb := arg(args, 0); verb {
	case "ls", "restart", "kill":
		return winAdmin(verb, args[1:])
	case "", "help", "-h", "--help":
		fmt.Print(winUsage)
		return nil
	}
	switch arg(args, 1) {
	case "agent", "shell", "diff", "checks":
		refreshBindings()
		return winNav(arg(args, 0), arg(args, 1), arg(args, 2))
	}
	fmt.Fprint(os.Stderr, winUsage)
	return fmt.Errorf("win: unknown command %q", arg(args, 0))
}

// winFlags parses a verb's arguments, flags in any position. ok is false when
// the command should stop: on an error, or after -h.
func winFlags(verb string, args []string) (space string, all bool, pos []string, ok bool, err error) {
	fs := flag.NewFlagSet("atelier win "+verb, flag.ContinueOnError)
	fs.StringVar(&space, "space", "", "")
	fs.StringVar(&space, "s", "", "")
	if verb == "ls" {
		fs.BoolVar(&all, "all", false, "")
		fs.BoolVar(&all, "a", false, "")
	}
	fs.SetOutput(io.Discard)
	for err == nil {
		if err = fs.Parse(args); err != nil || fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Print(winUsage)
		return "", false, nil, false, nil
	case err == nil && verb == "ls" && len(pos) > 0:
		err = fmt.Errorf("win ls: unexpected argument %q", pos[0])
	case err == nil && verb == "ls" && all && space != "":
		err = errors.New("win ls: --all and --space exclude each other")
	case err == nil && verb != "ls" && len(pos) != 1:
		err = fmt.Errorf("win %s: name one window (`atelier win ls` lists them)", verb)
	}
	if err != nil {
		fmt.Fprint(os.Stderr, winUsage)
	}
	return space, all, pos, err == nil, err
}

// winAdmin lists a space's windows, or restarts or closes one of them.
func winAdmin(verb string, args []string) error {
	space, all, pos, ok, err := winFlags(verb, args)
	if !ok {
		return err
	}
	st, err := core.Read()
	if err != nil {
		return err
	}
	if all {
		wins, err := tmux.Windows("")
		if err != nil {
			return err
		}
		return winTable(os.Stdout, spaceWindows(st, wins), true, time.Now())
	}
	ws, err := spaceFor(st, space)
	if err != nil {
		return err
	}
	name, isAgent := arg(pos, 0), false
	if name == "agent" || name == agent.Window {
		name, isAgent = agent.Window, true
	}
	unlock := func() {}
	if isAgent && verb != "ls" {
		// A swap runs in the agent's window; stopping it halfway would drop the
		// handoff the incoming agent was to start with.
		free, ok, err := core.TryLockFile(swapLock(ws.Slug))
		if err != nil {
			return err
		}
		if !ok || swapReserved(ws.Slug) {
			if ok {
				free()
			}
			return fmt.Errorf("%s is swapping agents, or its agent is being restarted; try again shortly", ws.Slug)
		}
		unlock = free
		// A swap may have finished since the state was read: act on the agent
		// the space has now, in the window as it is now.
		slug := ws.Slug
		if st, err = core.Read(); err != nil {
			unlock()
			return err
		}
		if ws = st.Find(slug); ws == nil || ws.Retired {
			unlock()
			return fmt.Errorf("%s is no longer active", slug)
		}
	}
	defer func() { unlock() }()
	var wins []tmux.Window
	// Restarting the agent starts it if its window, or the whole session, is
	// gone, as M-a does — inside a running atelier, not a bare server of its own.
	switch revive := isAgent && verb == "restart"; {
	case tmux.HasSession(ws.Session):
		if wins, err = tmux.Windows(ws.Session); err != nil {
			return err
		}
	case !revive:
		return fmt.Errorf("%s has no session running; open it from M-s to start one", ws.Slug)
	case !tmux.HasSession(tmux.HomeSession):
		return errors.New("atelier isn't running; start it with `atelier`")
	}
	if verb == "ls" {
		return winTable(os.Stdout, wins, false, time.Now())
	}
	w, n := findWindow(wins, name)
	switch {
	case n > 1:
		return fmt.Errorf("%s has %d windows named %q; exit the extra one from inside it", ws.Slug, n, pos[0])
	case n == 0 && (!isAgent || verb != "restart"):
		return fmt.Errorf("no window %q in %s (it has: %s)", pos[0], ws.Slug, strings.Join(windowNames(wins), ", "))
	}
	// What the pane runs now, to see off afterwards. A dead pane's processes
	// are gone, and its terminal may be someone else's by now.
	var procs []int
	if n == 1 && !w.Dead {
		procs = paneProcs(w)
	}
	// Run from inside the window itself, this process is on the terminal being
	// closed and may be in what is being ended (a pipe to a reader there); it
	// still has to finish.
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE, syscall.SIGTERM)

	done := "restarted"
	switch {
	case verb == "kill":
		done = "closed"
		if len(wins) == 1 {
			// Its last window takes the session with it: keep whoever is in it
			// attached. The space stays active, and opening it starts it again.
			_ = ensureHome()
			tmux.MoveClients(ws.Session, tmux.HomeSession)
		}
		if err = tmux.KillWindow(w.ID); err == nil && isAgent {
			settle(ws.Session)
		}
	case isAgent:
		// Cleared first: the new agent's own hooks report from here on.
		settle(ws.Session)
		// Not the window's own command, which may be a handoff long finished:
		// the agent, resuming its conversation here. Like a swap, this replaces
		// the old agent in one step; one that ignores the hangup is seen off
		// below.
		k := agent.Of(ws.Agent)
		err = agent.RunAgent(*ws, k, k.ResumeCmd(agent.LatestSession(k, ws.Root(), ws.Created), ""))
		// Starting the agent selects its window; a restart shouldn't move you.
		if at := activeWindow(wins); err == nil && at != "" && at != w.ID {
			_ = tmux.SelectWindow(ws.Session, at)
		}
	default:
		err = tmux.RestartWindow(w.ID)
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s %s in %s\n", done, pos[0], ws.Slug)
	unlock() // a swap needn't wait out the grace
	unlock = func() {}
	reap(procs, reapGrace)
	return nil
}

// settle clears an ended agent's status. Status is the session's, and an agent
// killed outright fires no hook: a blocked one would badge the space until
// the next agent spoke.
func settle(session string) {
	_ = agent.SetStatus(session, core.StatusIdle)
	_ = tmux.SetStatusMarker(session, agent.StatusMarker(core.StatusIdle))
	agent.RefreshAttention()
}

// spaceFor is the space a win verb acts on: the one named, else the one the
// caller is in.
func spaceFor(st *core.State, slug string) (*core.Workspace, error) {
	if slug != "" {
		w := st.Find(slug)
		switch {
		case w == nil:
			return nil, fmt.Errorf("no space %q (`atelier ls` lists them)", slug)
		case w.Retired:
			return nil, fmt.Errorf("%s is in the trash; restore it first", slug)
		}
		return w, nil
	}
	for _, s := range currentSpaces(st) {
		if w := st.Find(s); w != nil && !w.Retired {
			return w, nil
		}
	}
	return nil, errors.New("not in a space: pass --space <slug> (`atelier ls` lists them)")
}

// spaceWindows keeps the windows of active spaces — not the splash's, nor a
// session someone made by hand — labelled by their space.
func spaceWindows(st *core.State, wins []tmux.Window) []tmux.Window {
	var out []tmux.Window
	for _, w := range wins {
		if ws := st.FindBySession(w.Session); ws != nil && !ws.Retired {
			w.Session = ws.Slug
			out = append(out, w)
		}
	}
	return out
}

// findWindow is the window with exactly this name, and how many have it.
// tmux itself would also take a prefix or a pattern, which is no way to pick
// what to kill; nor is the first of two with one name.
func findWindow(wins []tmux.Window, name string) (w tmux.Window, n int) {
	for _, c := range wins {
		if c.Name == name {
			if n++; n == 1 {
				w = c
			}
		}
	}
	return w, n
}

func activeWindow(wins []tmux.Window) string {
	for _, w := range wins {
		if w.Active {
			return w.ID
		}
	}
	return ""
}

// windowName is a window's name as a person knows it: the agent's window is
// "claude" whichever agent it runs.
func windowName(w tmux.Window) string {
	if w.Name == agent.Window {
		return "agent"
	}
	return w.Name
}

func windowNames(wins []tmux.Window) []string {
	names := make([]string, len(wins))
	for i, w := range wins {
		names[i] = windowName(w)
	}
	return names
}

// winTable prints windows, one a row; `*` marks the one each session shows.
// QUIET is how long since a window printed anything — long, for a hung command.
func winTable(out io.Writer, wins []tmux.Window, spaces bool, now time.Time) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	head := "WINDOW\tRUNNING\tQUIET\tDIR"
	if spaces {
		head = "SPACE\t" + head
	}
	_, _ = fmt.Fprintln(tw, head)
	home, _ := os.UserHomeDir()
	for _, w := range wins {
		name, running, dir := windowName(w), w.Command, w.Path
		if w.Active {
			name += " *"
		}
		if w.Dead {
			running = "(exited)"
		}
		if rest, ok := strings.CutPrefix(dir, home); ok && home != "" && (rest == "" || rest[0] == '/') {
			dir = "~" + rest
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s", name, running, since(now, w.Activity), dir)
		if spaces {
			row = w.Session + "\t" + row
		}
		_, _ = fmt.Fprintln(tw, row)
	}
	return tw.Flush()
}

// paneProcs is what a pane runs: its own process group — its command and
// what that started — and the group in the foreground of its terminal, a
// shell's job, which leads a group of its own and is so often the very thing
// that hung. A job sent to the background, or started with nohup, is left be,
// as tmux itself would leave it.
func paneProcs(w tmux.Window) []int {
	// #nosec G204 -- Fixed ps invocation; the terminal name comes from tmux.
	out, err := exec.Command("ps", "-o", "pid=,pgid=,tpgid=", "-t", strings.TrimPrefix(w.TTY, "/dev/")).Output()
	if err != nil {
		return []int{w.PID}
	}
	return pickProcs(string(out), w.PID, os.Getpid())
}

// pickProcs reads `ps -o pid=,pgid=,tpgid=` for a pane's terminal. Without the
// pane's own process on it, the terminal has passed to another pane since the
// window was listed, and nothing on it is the pane's.
func pickProcs(ps string, pane, self int) []int {
	type proc struct{ pid, pgid int }
	var procs []proc
	groups := map[int]bool{}
	for _, ln := range strings.Split(ps, "\n") {
		f := strings.Fields(ln)
		if len(f) != 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		pgid, err2 := strconv.Atoi(f[1])
		tpgid, err3 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		if pid == pane {
			groups[pgid] = true
			if tpgid > 0 {
				groups[tpgid] = true
			}
		}
		procs = append(procs, proc{pid, pgid})
	}
	var pids []int
	for _, p := range procs {
		if groups[p.pgid] && p.pid > 1 && p.pid != self {
			pids = append(pids, p.pid)
		}
	}
	return pids
}

const reapGrace = 2 * time.Second

// reap makes sure what a pane ran is gone once tmux has hung up on it, which a
// wedged process can ignore. Each gets the grace to leave by itself — an agent
// has a transcript to finish, a shell its history — then is asked to
// terminate, and is killed if it is still there half a grace later.
func reap(pids []int, grace time.Duration) {
	alive := func() []int {
		var left []int
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				left = append(left, pid)
			}
		}
		return left
	}
	wait := func(d time.Duration) []int {
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if len(alive()) == 0 {
				return nil
			}
		}
		return alive()
	}
	for _, pid := range wait(grace) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	for _, pid := range wait(grace / 2) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
