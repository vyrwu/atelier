package main

import (
	"encoding/json"
	"flag"
	"os"

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
