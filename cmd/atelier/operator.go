package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/mattn/go-isatty"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/operator"
)

func spacesCmd(args []string) error {
	var trash, asJSON bool
	if ok, err := flags("spaces", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&trash, "trash", false, "show retired spaces")
		fs.BoolVar(&asJSON, "json", false, "print JSON")
	}); !ok {
		return err
	}
	spaces, err := operator.List(trash)
	if err != nil {
		return err
	}
	return printSpaces(spaces, asJSON)
}

func printSpaces(spaces []operator.Space, asJSON bool) error {
	if !asJSON {
		return operator.FormatTable(os.Stdout, spaces)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(spaces)
}

func readCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: atelier read <space> [--lines N]")
	}
	lines := operator.DefaultLines
	if ok, err := flags("read", args[1:], func(fs *flag.FlagSet) { fs.IntVar(&lines, "lines", lines, "scrollback lines") }); !ok {
		return err
	}
	screen, err := operator.Read(args[0], lines)
	if err != nil {
		return err
	}
	fmt.Println(screen)
	return nil
}

func sendCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: atelier send <space> [text] [--key K] [--no-enter] [-- text]")
	}
	slug := args[0]
	args = args[1:]
	text, args, err := splitSendText(args)
	if err != nil {
		return err
	}
	var keys []string
	var noEnter bool
	if ok, err := flags("send", args, func(fs *flag.FlagSet) {
		fs.Func("key", "named key (repeatable)", func(s string) error { keys = append(keys, s); return nil })
		fs.BoolVar(&noEnter, "no-enter", false, "do not submit the text")
	}); !ok {
		return err
	}
	return operator.Send(slug, text, keys, !noEnter)
}

func waitCmd(args []string) error {
	var timeout time.Duration
	var asJSON bool
	if ok, err := flags("wait", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&timeout, "timeout", 0, "maximum wait (default 5m, Grok 50s)")
		fs.BoolVar(&asJSON, "json", false, "print JSON")
	}); !ok {
		return err
	}
	spaces, err := operator.Wait(context.Background(), timeout)
	if err != nil {
		return err
	}
	return printSpaces(spaces, asJSON)
}

// A Maître caller always goes through the human's tmux client, regardless of
// command-line bypass flags or whether the agent itself asks for approvals.
func destroyGate(fromMaitre, yes, tty bool, in io.Reader, maitre func(string) error) func(string) error {
	return func(prompt string) error {
		if fromMaitre {
			return maitre(prompt)
		}
		if yes {
			return nil
		}
		if !tty {
			return fmt.Errorf("not a terminal: pass --yes to delete, or --dry-run to look")
		}
		ok, err := confirm(in, prompt+" [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("cancelled")
		}
		return nil
	}
}

func deleteCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: atelier delete <space> [--yes]")
	}
	var yes bool
	if ok, err := flags("delete", args[1:], func(fs *flag.FlagSet) { fs.BoolVar(&yes, "yes", false, "don't ask (except in Maître)") }); !ok {
		return err
	}
	if err := refuseEnclosingSpace("delete", args[0]); err != nil {
		return err
	}
	return operator.Delete(args[0], destroyGate(operator.FromMaitre(), yes, isatty.IsTerminal(os.Stdin.Fd()), os.Stdin, confirmMaitreDestroy))
}

func lifecycleCmd(verb string, args []string) error {
	n := 1
	if verb == "rename" {
		n = 2
	}
	if len(args) != n {
		return fmt.Errorf("usage: atelier %s <space>%s", verb, map[bool]string{true: " <title>"}[n == 2])
	}
	switch verb {
	case "rename":
		return operator.Rename(args[0], args[1])
	case "retire":
		if err := refuseEnclosingSpace(verb, args[0]); err != nil {
			return err
		}
		return operator.Retire(args[0])
	case "restore":
		return operator.Restore(args[0])
	case "focus":
		return operator.Focus(args[0])
	}
	return fmt.Errorf("unknown operator command %q", verb)
}

// Killing the CLI's own session can deliver SIGHUP before it finishes disk
// cleanup or persists retirement. The caller must run from another session.
func refuseEnclosingSpace(verb, slug string) error {
	state, err := core.Read()
	if err != nil {
		return err
	}
	if slices.Contains(enclosingSpaces(state), slug) {
		return fmt.Errorf("cannot %s enclosing space %q; run from Home or another space", verb, slug)
	}
	return nil
}

func confirmMaitreDestroy(prompt string) error {
	return operator.ConfirmDestroy(context.Background(), prompt)
}

// A delimiter lets text beginning with -- stay literal, while flags before it
// still control submission and menu keys. The prompt is one shell argument.
func splitSendText(args []string) (string, []string, error) {
	text := ""
	hasText := len(args) > 0 && !strings.HasPrefix(args[0], "--")
	if hasText {
		text = args[0]
		args = args[1:]
	}
	if i := slices.Index(args, "--"); i >= 0 {
		if hasText || len(args[i+1:]) != 1 {
			return "", nil, fmt.Errorf("send expects one text argument after --")
		}
		text = args[i+1]
		args = args[:i]
	}
	return text, args, nil
}
