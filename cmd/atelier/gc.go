package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/gc"
	"github.com/vyrwu/atelier/internal/git"
	"github.com/vyrwu/atelier/internal/operator"
	"github.com/vyrwu/atelier/internal/tmux"
)

// flags parses args strictly: a mistyped flag must fail, not be ignored — a
// purge that silently dropped its --older-than would empty the whole Trash. ok
// is false when the command should stop: on an error, or after -h.
func flags(name string, args []string, define func(*flag.FlagSet)) (ok bool, err error) {
	fs := flag.NewFlagSet("atelier "+name, flag.ContinueOnError)
	define(fs)
	fs.SetOutput(io.Discard)
	err = fs.Parse(args)
	if err == nil && fs.NArg() > 0 {
		err = fmt.Errorf("%s: unexpected argument %q", name, fs.Arg(0))
	}
	if err == nil {
		return true, nil
	}
	fs.SetOutput(os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		fs.SetOutput(os.Stdout)
		err = nil
	}
	fs.Usage()
	return false, err
}

// ageFlag is an --older-than value: 30d, 2w, or a Go duration.
type ageFlag time.Duration

func (a *ageFlag) String() string { return time.Duration(*a).String() }
func (a *ageFlag) Set(s string) error {
	d, err := gc.ParseAge(s)
	*a = ageFlag(d)
	return err
}

// list prints the spaces, stalest first:
// atelier ls [--trash] [--older-than AGE].
func list(args []string) error {
	var age ageFlag
	var trash bool
	if ok, err := flags("ls", args, func(fs *flag.FlagSet) {
		fs.Var(&age, "older-than", "only spaces untouched for at least `AGE` (30d, 2w, 36h)")
		fs.BoolVar(&trash, "trash", false, "only spaces in the Trash")
	}); !ok {
		return err
	}
	st, err := core.Read()
	if err != nil {
		return err
	}
	now := time.Now()
	var spaces []core.Workspace
	for _, w := range st.Workspaces {
		if !w.IsMaitre() && (!trash || w.Retired) && now.Sub(gc.LastActive(w)) >= time.Duration(age) {
			spaces = append(spaces, w)
		}
	}
	sort.SliceStable(spaces, func(i, j int) bool { return gc.LastActive(spaces[i]).Before(gc.LastActive(spaces[j])) })
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SPACE\tSTATE\tIDLE\tAGE\tAGENT\tWORKTREES\tTITLE")
	for _, w := range spaces {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", w.Slug, state(w), since(now, gc.LastActive(w)),
			since(now, w.Created), agent.Of(w.Agent), git.CountWorktrees(w.Root()), clip(w.Title, 60))
	}
	return tw.Flush()
}

// state is a space's lifecycle state, or "missing" when its directory is gone
// (which `atelier gc` clears up).
func state(w core.Workspace) string {
	switch {
	case !dirExists(w.Root()):
		return "missing"
	case w.Retired:
		return "trash"
	}
	return "active"
}

// purge deletes spaces untouched for a while, behind a confirm:
// atelier purge [--older-than AGE] [--all] [--force] [--dry-run] [--yes].
func purge(args []string) error {
	ctx := context.Background()
	if operator.FromMaitre() {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	var age ageFlag
	var p gc.Policy
	var dry, yes bool
	if ok, err := flags("purge", args, func(fs *flag.FlagSet) {
		fs.Var(&age, "older-than", "only spaces untouched for at least `AGE` (30d, 2w, 36h)")
		fs.BoolVar(&p.Active, "all", false, "active spaces too, not only the Trash")
		fs.BoolVar(&p.Force, "force", false, "spaces with running sessions or unsaved work too")
		fs.BoolVar(&dry, "dry-run", false, "list what would be deleted, delete nothing")
		fs.BoolVar(&yes, "yes", false, "don't ask")
		fs.BoolVar(&yes, "y", false, "don't ask")
	}); !ok {
		return err
	}
	if operator.FromMaitre() {
		p.Force = false
	}
	if !operator.FromMaitre() && !dry && !yes && !isatty.IsTerminal(os.Stdin.Fd()) { // /dev/null is a character device too
		return fmt.Errorf("not a terminal: pass --yes to purge, or --dry-run to look")
	}
	p.Age = time.Duration(age)
	st, err := core.Read()
	if err != nil {
		return err
	}
	p.Keep = currentSpaces(st)
	plan, err := p.Plan(st, time.Now())
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var doomed, kept []gc.Candidate
	for _, c := range plan {
		if (c.Running || len(c.Unsaved) > 0) && !p.Force {
			kept = append(kept, c)
		} else {
			doomed = append(doomed, c)
		}
	}
	if len(kept) > 0 {
		hint := "--force deletes them too"
		if operator.FromMaitre() {
			hint = "retire running spaces and save their work first"
		}
		fmt.Printf("Keeping %d with running sessions or unsaved work (%s):\n", len(kept), hint)
		for _, c := range kept {
			if c.Running {
				c.Unsaved = append(c.Unsaved, "session is running; retire first")
			}
			fmt.Printf("  %s  %s\n", c.Slug, strings.Join(c.Unsaved, ", "))
		}
	}
	if len(doomed) == 0 {
		fmt.Println("Nothing to purge.")
		return nil
	}
	fmt.Printf("Deleting %d space(s), with their sessions, worktrees, and directories:\n", len(doomed))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, c := range doomed {
		row := []string{"  " + c.Slug, state(c.Workspace), "idle " + days(c.Idle), clip(c.Title, 50)}
		if len(c.Unsaved) > 0 {
			row = append(row, "loses "+strings.Join(c.Unsaved, ", "))
		}
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
	if dry {
		return nil
	}
	slugs := []string{}
	for _, c := range doomed[:min(5, len(doomed))] {
		slugs = append(slugs, c.Slug)
	}
	if len(doomed) > 5 {
		slugs = append(slugs, "…")
	}
	prompt := fmt.Sprintf("Purge %d spaces: %s?", len(doomed), strings.Join(slugs, ", "))
	maitreConfirm := func(prompt string) error { return operator.ConfirmDestroy(ctx, prompt) }
	if err := destroyGate(operator.FromMaitre(), yes, isatty.IsTerminal(os.Stdin.Fd()), os.Stdin, maitreConfirm)(prompt); err != nil {
		return err
	}
	failed := 0
	for _, c := range doomed {
		skipped, err := p.Purge(ctx, c.Slug, time.Now())
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			failed++
			fmt.Printf("%s: %v\n", c.Slug, err)
		case skipped != "":
			fmt.Printf("kept %s: %s\n", c.Slug, skipped)
		default:
			fmt.Printf("deleted %s\n", c.Slug)
		}
	}
	agent.RefreshAttention() // a deleted session may have been blocked
	if failed > 0 {
		return fmt.Errorf("%d space(s) failed to delete", failed)
	}
	return nil
}

// Protect the enclosing space and cwd independently: a shell in A can cd to B.
func currentSpaces(st *core.State) []string {
	return append(enclosingSpaces(st), cwdSpace())
}

// Session identity survives cd; plain tmux shells may only have TMUX_PANE.
func enclosingSpaces(st *core.State) []string {
	spaces := []string{os.Getenv("ATELIER_SLUG")}
	if w := st.FindBySession(os.Getenv("ATELIER_SESSION")); w != nil {
		spaces = append(spaces, w.Slug)
	}
	if pane := os.Getenv("TMUX_PANE"); pane != "" && insideOwnServer() {
		if out, err := tmux.Run("display-message", "-t", pane, "-p", "#{session_name}"); err == nil {
			if w := st.FindBySession(strings.TrimSpace(string(out))); w != nil {
				spaces = append(spaces, w.Slug)
			}
		}
	}
	return spaces
}

func cwdSpace() string {
	// A regular terminal can cd into a space without any atelier environment.
	// Resolve both paths, since the configured root may be a symlink.
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return ""
	}
	root, err := filepath.EvalSymlinks(core.AteliersRoot())
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, cwd)
	if err == nil {
		slug, _, _ := strings.Cut(rel, string(filepath.Separator))
		if slug != "." && slug != ".." {
			return slug
		}
	}
	return ""
}

// confirm asks a yes/no question on the terminal.
func confirm(in io.Reader, prompt string) (bool, error) {
	fmt.Print(prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}

// collect sweeps up what spaces leave behind: atelier gc [--dry-run].
func collect(args []string) error {
	var dry bool
	if ok, err := flags("gc", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&dry, "dry-run", false, "report what would be collected, change nothing")
	}); !ok {
		return err
	}
	r, err := gc.Sweep(dry, time.Now())
	say := func(done, would string, items []string) {
		if len(items) == 0 {
			return
		}
		if dry {
			done = would
		}
		fmt.Printf("%s (%d):\n  %s\n", done, len(items), strings.Join(items, "\n  "))
	}
	say("Dropped records whose directory is gone", "Would drop records whose directory is gone", r.Records)
	say("Put space directories with no record in the Trash", "Would put space directories with no record in the Trash", r.Adopted)
	say("Recovered running spaces as active", "Would recover running spaces as active", r.ActiveAdopted)
	say("Stopped sessions still running for a space in the Trash", "Would stop sessions still running for a space in the Trash", r.Sessions)
	say("Dropped Claude project entries for deleted directories", "Would drop Claude project entries for deleted directories", r.Projects)
	say("Pruned source repositories' records of deleted worktrees", "Would prune source repositories' records of deleted worktrees", r.Worktrees)
	if r.Cache > 0 {
		if dry {
			fmt.Printf("Would remove %d stale cache file(s)\n", r.Cache)
		} else {
			fmt.Printf("Removed %d stale cache file(s)\n", r.Cache)
		}
	}
	if r.Empty() && err == nil {
		fmt.Println("Nothing to collect.")
	}
	if !dry {
		agent.RefreshAttention()
	}
	return err
}

// since is the time from t to now as "3m", "5h", or "12d"; "—" for no time.
func since(now, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return days(now.Sub(t))
}

func days(d time.Duration) string { return operator.FormatAge(d) }

// clip cuts s to n runes.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
