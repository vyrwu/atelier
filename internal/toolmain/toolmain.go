// Package toolmain runs a built-in tool's command tree in-process.
//
// Every `atelier tools <name> <args...>` invocation — whether typed, or
// fired by a tmux binding, or exec'd by an fzf become() up a picker chain
// — lands here via plugin dispatch. There are no separate atelier-<name>
// binaries anymore; the core knows its built-in tools at compile time and
// dispatches to them within the same process.
package toolmain

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/manifest"
)

// Dispatch builds a built-in tool's cobra root, runs it with args, and
// translates the outcome into a process exit code. It does NOT return on
// cancel or error — it os.Exit()s — because this atelier process is
// frequently itself the target of an fzf become() / display-popup -E,
// and the parent fzf up the chain reads THIS process's exit status:
//
//   - fzf.ErrCancelled -> exit 130 (fzf's cancel status; the parent's
//     fzf.Pick returns ErrCancelled and the become() chain unwinds
//     cleanly instead of misreading an empty pick as a real selection).
//   - any other error  -> print it (and, when stderr is a TTY inside a
//     popup, pause for a keypress so the user can read it before the
//     popup closes) then exit 1.
//   - nil              -> return, so the caller's process exits 0.
func Dispatch(m *manifest.Manifest, addCmds func(root *cobra.Command), args []string) {
	root := &cobra.Command{
		Use:           "atelier tools " + m.Name,
		Short:         m.Description,
		SilenceUsage:  true,
		SilenceErrors: true, // we print errors ourselves with a pause
	}
	addCmds(root)
	root.SetArgs(args)
	debuglog.Logf("toolmain: dispatch %s args=%v", m.Name, args)
	Finish(m.Name, root.Execute())
}

// Finish translates a tool's terminal error into a process exit code and
// does NOT return except on success. Shared by built-in dispatch (above)
// and launcher dispatch (plugin.Plugin.Dispatch) so BOTH get identical
// behavior: cancel → exit 130 (unwinds the fzf become() chain), any other
// error → print it and, on a TTY inside a popup, pause for a keypress so
// the message is readable before the popup closes → exit 1, success →
// return so the caller's process exits 0.
func Finish(name string, err error) {
	if errors.Is(err, fzf.ErrCancelled) {
		debuglog.Logf("toolmain: exit cancelled (130) %s", name)
		os.Exit(130)
	}
	if err != nil {
		debuglog.LogErr(fmt.Sprintf("toolmain: %s", name), err)
		showErrorAndPause(err)
		os.Exit(1)
	}
	debuglog.Logf("toolmain: exit ok %s", name)
}

// showErrorAndPause prints the error and, when stderr is a TTY, waits
// for a keypress so the user sees the message before the popup closes.
func showErrorAndPause(err error) {
	fmt.Fprintf(os.Stderr, "\n\033[31mError:\033[0m %v\n", err)
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	fmt.Fprint(os.Stderr, "\nPress any key to close.")
	// Put the terminal in raw mode briefly so a single keypress is enough
	// (no need for Enter).
	old, err2 := term.MakeRaw(int(os.Stdin.Fd()))
	if err2 != nil {
		return
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), old) }()
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
}
