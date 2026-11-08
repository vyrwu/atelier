package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

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
