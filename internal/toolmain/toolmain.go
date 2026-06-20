// Package toolmain is the shared bootstrap for cmd/atelier-* tool binaries.
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

// Run is the entry point a tool's main() delegates to.
// Handles:
//   - --atelier-manifest: emit JSON manifest, exit 0
//   - cobra root setup with the right Use/Short
//   - error visibility: when stderr is a TTY (the common case when running
//     inside a tmux popup) and the command errored, print the error and
//     wait for keypress so the user can read it before the popup closes.
func Run(m *manifest.Manifest, addCmds func(root *cobra.Command)) {
	if len(os.Args) >= 2 && os.Args[1] == manifest.Sentinel {
		data, err := m.AsJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error emitting manifest: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		return
	}

	root := &cobra.Command{
		Use:           "atelier-" + m.Name,
		Short:         m.Description,
		SilenceUsage:  true,
		SilenceErrors: true, // we print errors ourselves with a pause
	}
	addCmds(root)
	debuglog.Logf("toolmain: start %s argv=%v", m.Name, os.Args[1:])
	err := root.Execute()
	// Cancellation: propagate up the fzf become() chain by exiting 130.
	// When fzf become()s into another atelier-<tool>, the parent fzf reads
	// THIS process's exit code. Exit 0 (cobra default for nil err) would
	// look like a regular pick with empty output to the parent, leading
	// downstream tools to misinterpret it. Exit 130 = fzf's "cancelled"
	// status — parent's fzf.Pick returns ErrCancelled, chain unwinds.
	if errors.Is(err, fzf.ErrCancelled) {
		debuglog.Logf("toolmain: exit cancelled (130) %s", m.Name)
		os.Exit(130)
	}
	if err != nil {
		debuglog.LogErr(fmt.Sprintf("toolmain: %s", m.Name), err)
		showErrorAndPause(err)
		os.Exit(1)
	}
	debuglog.Logf("toolmain: exit ok %s", m.Name)
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
