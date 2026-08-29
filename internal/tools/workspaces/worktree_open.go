package workspaces

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/fzfstyle"
	"github.com/vyrwu/atelier/internal/notify"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// WorktreeOpenCommand implements `atelier tools workspaces worktree-open <cmd…>`:
// pick one of the active workspace's worktrees, then run <cmd> INSIDE it. A
// workspace is an intent that can span several repos/worktrees, and a per-repo
// git TUI (lazygit) needs a real checkout — so the tool always resolves WHICH
// worktree first via a picker, rather than opening at the (symlink-only)
// workspace root. Runs in the current popup pty and execs the command in place;
// so a fresh pick happens every open (this is why lazygit is a popup="none"
// launcher, not a persistent workspace-scoped session).
//
//	0 worktrees → nothing to open (toast + exit)
//	1 worktree  → open there directly (no pointless one-item picker)
//	≥2          → fzf picker
func WorktreeOpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "worktree-open <cmd> [args...]",
		Short:  "Pick a worktree in the active workspace, then run a command inside it",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			h := tmuxhost.New(socket)
			dir, ok := pickWorktreeDir(h)
			if !ok {
				return fzf.ErrCancelled
			}
			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = "/bin/sh"
			}
			full := fmt.Sprintf("cd %q && exec %s", dir, strings.Join(args, " "))
			return syscall.Exec(shell, []string{shell, "-c", full}, os.Environ())
		},
	}
	// Stop parsing at the first positional so flags meant for <cmd> aren't
	// swallowed as ours (e.g. `worktree-open lazygit -ucd ...`).
	c.Flags().SetInterspersed(false)
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// pickWorktreeDir resolves the active workspace and returns the real worktree
// directory to open in (see WorktreeOpenCommand for the 0/1/≥2 rule). Returns
// ok=false when cancelled or the workspace has no worktrees.
func pickWorktreeDir(h *tmuxhost.Client) (string, bool) {
	ctx, err := popup.ResolveParentContext(h)
	if err != nil {
		notify.Show(notify.Error, "could not resolve the active workspace")
		return "", false
	}
	name, _ := h.DisplayMessageAt(ctx.SessionID, "#{session_name}")
	name = strings.TrimSpace(name)

	st, _ := statestore.Load()
	var wts []statestore.Worktree
	if st != nil {
		if ws := st.FindWorkspace(name); ws != nil {
			wts = ws.Worktrees
		}
	}
	switch len(wts) {
	case 0:
		notify.Show(notify.Info, "no worktrees in this workspace yet")
		return "", false
	case 1:
		return wts[0].Path, true
	}

	lines := buildWorktreeLines(wts)
	picked, err := fzf.Pick(lines,
		fzfstyle.Args("枝 ", "Select Worktree", "140",
			fzfstyle.WithDelimiter("\t"),
			fzfstyle.WithNth("1"),
		)...)
	if err != nil {
		return "", false
	}
	return worktreePathFromPicked(picked), picked != ""
}

// buildWorktreeLines renders one fzf line per worktree: "<repo>  <branch>" for
// display (field 1, searched) + the real worktree path as a trailing
// tab-delimited field (field 2, survives --ansi for lookup). Pure.
func buildWorktreeLines(wts []statestore.Worktree) []string {
	lines := make([]string, 0, len(wts))
	for _, wt := range wts {
		label := fmt.Sprintf("%s  %s", wt.Repo, wt.Branch)
		lines = append(lines, fmt.Sprintf("%s\t%s", label, wt.Path))
	}
	return lines
}

// worktreePathFromPicked extracts the trailing tab-delimited path field from a
// picked worktree line. Pure.
func worktreePathFromPicked(picked string) string {
	if i := strings.LastIndexByte(picked, '\t'); i >= 0 {
		return picked[i+1:]
	}
	return picked
}
