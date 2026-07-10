package plugin

import (
	"fmt"
	"sync"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/toolmain"
)

// builtinEntry is one compiled-in tool: its manifest plus the constructor
// that adds its subcommands to a cobra root.
type builtinEntry struct {
	manifest *manifest.Manifest
	add      func(root *cobra.Command)
}

var (
	regMu     sync.Mutex
	builtins  []builtinEntry
	regByName = map[string]bool{}
)

// RegisterBuiltin adds a compiled-in tool to the registry. Called from
// internal/tools/all at init time, once per tool. Idempotent by name so a
// double import can't duplicate a tool. Panics on a nil or unnamed
// manifest / nil constructor — those are programmer errors, not runtime
// conditions.
func RegisterBuiltin(m *manifest.Manifest, add func(root *cobra.Command)) {
	if m == nil || m.Name == "" {
		panic("plugin: RegisterBuiltin requires a named manifest")
	}
	if add == nil {
		panic(fmt.Sprintf("plugin: RegisterBuiltin(%s) requires a command constructor", m.Name))
	}
	regMu.Lock()
	defer regMu.Unlock()
	if regByName[m.Name] {
		return
	}
	regByName[m.Name] = true
	builtins = append(builtins, builtinEntry{manifest: m, add: add})
}

// builtinList returns a snapshot of registered built-ins.
func builtinList() []builtinEntry {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]builtinEntry, len(builtins))
	copy(out, builtins)
	return out
}

// Dispatch runs the tool for `atelier tools <name> <args...>`. It does not
// return on cancel or error — it os.Exit()s via toolmain — because this
// atelier process is frequently the target of an fzf become() /
// display-popup -E whose parent reads its exit status.
//
// For a built-in, toolmain.Dispatch builds the tool's cobra tree and
// finishes it. For a launcher, runLauncher opens the configured command
// (which syscall.Exec's on success and only returns on error); the error
// goes through the SAME toolmain.Finish tail so a mis-configured launcher
// pauses on its error inside the popup instead of flashing and vanishing.
func (p *Plugin) Dispatch(args []string) error {
	if p.add != nil {
		toolmain.Dispatch(p.Manifest, p.add, args)
		return nil
	}
	toolmain.Finish(p.Name, p.runLauncher(args))
	return nil
}

func errShadowsBuiltin(name string) error {
	return fmt.Errorf("launcher name %q shadows a built-in tool; ignored", name)
}

func errReservedName(name string) error {
	return fmt.Errorf("launcher name %q is reserved (the core binary name); ignored", name)
}
