// Package plugin is atelier's tool registry.
//
// A tool is one of two things:
//
//   - A BUILT-IN: a package under internal/tools/* that registers a
//     manifest + command-tree constructor via RegisterBuiltin (wired in
//     internal/tools/all). Built-ins are compiled into the single atelier
//     binary and dispatched in-process — the core knows them at compile
//     time. There is no subprocess manifest protocol and no PATH scan.
//
//   - A LAUNCHER: a `[tools.<name>]` block in the user's config.toml that
//     names ANY command to run in a popup. atelier launches it and owns
//     the window/state; the command needn't be an atelier binary. This is
//     how a user extends atelier without writing Go — e.g. wrap k9s with
//     AWS auth in a `aws-vault-k9s` script and register it as a launcher.
//
// Discover() merges both into one list. Every consumer — the `atelier
// tools` dispatcher, `atelier init` binding generation, the tool
// selector, `atelier doctor` — reads that merged view and stays agnostic
// to whether a tool is built-in or a config launcher.
package plugin

import (
	"os/exec"
	"sort"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
)

// SelfBinary is the core's own binary name.
const SelfBinary = "atelier"

// Plugin is a registered tool paired with its manifest.
type Plugin struct {
	Name     string             // tool name (e.g. "lazygit")
	Manifest *manifest.Manifest // its manifest

	// add is the built-in command-tree constructor. nil for launchers.
	add func(root *cobra.Command)
	// launch is the shell command a launcher runs. "" for built-ins.
	launch string
}

// IsBuiltin reports whether this plugin is a compiled-in tool (as opposed
// to a config-declared launcher).
func (p *Plugin) IsBuiltin() bool { return p.add != nil }

// DiscoveryResult is the outcome of a Discover() call.
type DiscoveryResult struct {
	Plugins []Plugin
	Skipped map[string]error // source (config key) → why it was skipped
}

// Discover returns every registered tool: built-ins from the static
// registry plus launchers parsed from the user's config.toml. Cheap —
// reading a slice and one small TOML file — so it is intentionally NOT
// memoized (unlike the old fork-per-binary PATH scan, which had to be).
//
// Built-ins always win a name collision: a launcher that reuses a
// built-in's name is dropped into Skipped rather than shadowing it.
func Discover() (*DiscoveryResult, error) {
	res := &DiscoveryResult{Skipped: map[string]error{}}

	seen := map[string]bool{}
	for _, b := range builtinList() {
		res.Plugins = append(res.Plugins, Plugin{
			Name:     b.manifest.Name,
			Manifest: b.manifest,
			add:      b.add,
		})
		seen[b.manifest.Name] = true
	}

	launchers, skipped := loadLaunchers()
	for k, v := range skipped {
		res.Skipped[k] = v
	}
	for _, lp := range launchers {
		if lp.Name == SelfBinary {
			res.Skipped["[tools."+lp.Name+"]"] = errReservedName(lp.Name)
			continue
		}
		if seen[lp.Name] {
			res.Skipped["[tools."+lp.Name+"]"] = errShadowsBuiltin(lp.Name)
			continue
		}
		seen[lp.Name] = true
		res.Plugins = append(res.Plugins, lp)
	}

	sort.Slice(res.Plugins, func(i, j int) bool {
		return res.Plugins[i].Name < res.Plugins[j].Name
	})
	return res, nil
}

// FindByName returns the plugin with the given name, or (nil, false).
func (r *DiscoveryResult) FindByName(name string) (*Plugin, bool) {
	for i := range r.Plugins {
		if r.Plugins[i].Name == name {
			return &r.Plugins[i], true
		}
	}
	return nil, false
}

// CheckRequirements returns, per tool, the binaries its manifest declares
// (Requires) that are missing from PATH. Used by `atelier doctor`.
func (r *DiscoveryResult) CheckRequirements() map[string][]string {
	out := map[string][]string{}
	for _, p := range r.Plugins {
		var missing []string
		for _, req := range p.Manifest.Requires {
			if _, err := exec.LookPath(req); err != nil {
				missing = append(missing, req)
			}
		}
		if len(missing) > 0 {
			out[p.Name] = missing
		}
	}
	return out
}
