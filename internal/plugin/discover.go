// Package plugin discovers atelier tools on PATH.
//
// A tool is any executable named `atelier-<name>` on PATH that responds to
// the manifest.Sentinel flag with valid manifest JSON. The core uses
// discovery to populate the `atelier tools` dispatcher, the `atelier init`
// binding aggregator, and `atelier doctor` requirement checks.
//
// Discovery never errors on an individual tool's failure: failed tools are
// returned in Skipped so the core can show a warning without blocking
// healthy tools.
package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/vyrwu/atelier/internal/manifest"
)

// Process-scoped cache of the default-PATH discovery. Discover() is
// called from 7+ places per command (help, doctor, tools list,
// popup, toolselector, initgen.Render, etc.); each call would
// otherwise fork ONE subprocess per discovered binary (to read its
// --atelier-manifest). For 9 plugins × 7 callsites = up to 63 forks
// per command. Memoize since discovery is idempotent within a
// process lifetime: PATH doesn't change, plugin binaries don't
// change.
//
// The cache is keyed on (), not on pathDirs — explicit-PATH calls
// (used only by tests passing tempdirs) bypass the cache entirely.
var (
	discoverOnce   sync.Once
	discoverResult *DiscoveryResult
	discoverErr    error
)

// Prefix is the binary-name prefix atelier scans for.
const Prefix = "atelier-"

// SelfBinary is the core's own binary name; never reported as a plugin even
// if found on PATH.
const SelfBinary = "atelier"

// Plugin is a discovered tool, paired with its loaded manifest.
type Plugin struct {
	Name     string             // tool name (e.g. "lazygit"), without prefix
	Binary   string             // absolute path to the binary
	Manifest *manifest.Manifest // loaded manifest
}

// DiscoveryResult is the outcome of a Discover() call.
type DiscoveryResult struct {
	Plugins []Plugin
	Skipped map[string]error // absolute binary path → why it was skipped
}

// Discover scans the directories given (defaults to $PATH) for atelier-*
// binaries and loads their manifests. Earlier directories take precedence
// on name conflict — matches the standard PATH lookup semantics.
//
// When called with no args, results are memoized for the lifetime of
// the process. Tests that need fresh discovery should pass explicit
// pathDirs (they bypass the cache).
func Discover(pathDirs ...string) (*DiscoveryResult, error) {
	if len(pathDirs) == 0 {
		discoverOnce.Do(func() {
			discoverResult, discoverErr = discoverFresh(
				filepath.SplitList(os.Getenv("PATH")))
		})
		return discoverResult, discoverErr
	}
	return discoverFresh(pathDirs)
}

// discoverFresh is the un-memoized implementation. Called by Discover
// (via sync.Once for default-PATH) and directly by tests passing
// explicit pathDirs.
func discoverFresh(pathDirs []string) (*DiscoveryResult, error) {
	res := &DiscoveryResult{Skipped: map[string]error{}}
	seen := map[string]bool{}

	for _, dir := range pathDirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // missing PATH entries are normal
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, Prefix) {
				continue
			}
			toolName := strings.TrimPrefix(name, Prefix)
			if toolName == "" || toolName == SelfBinary {
				continue
			}
			if seen[toolName] {
				continue // earlier PATH entry wins
			}
			seen[toolName] = true

			full := filepath.Join(dir, name)
			if !isExecutable(full) {
				continue
			}

			m, err := manifest.FromBinary(full)
			if err != nil {
				res.Skipped[full] = err
				continue
			}
			if m.Name == "" {
				m.Name = toolName
			}
			res.Plugins = append(res.Plugins, Plugin{
				Name:     toolName,
				Binary:   full,
				Manifest: m,
			})
		}
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

// CheckRequirements returns the list of binaries declared by all plugins'
// manifests that are missing from PATH. Used by `atelier doctor`.
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

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}
