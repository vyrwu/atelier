package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/mattn/go-isatty"
	"io"
	"os"
	"strings"
	"time"

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
		return fmt.Errorf("usage: atelier send <space> [text] [--key K] [--no-enter]")
	}
	slug := args[0]
	args = args[1:]
	text := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		text = args[0]
		args = args[1:]
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
	spaces, err := operator.Wait(timeout)
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
	return operator.Delete(args[0], destroyGate(operator.FromMaitre(), yes, isatty.IsTerminal(os.Stdin.Fd()), os.Stdin, operator.ConfirmDestroy))
}
