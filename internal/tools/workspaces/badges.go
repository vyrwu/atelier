package workspaces

import (
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/plugin"
)

// badgeSpec is a resolved badge contribution from a discovered tool's
// manifest. It's the picker-side view of manifest.Badge, flattened with
// the owning tool's name + binary so the picker can render the badge and
// spawn the tool's refresh without knowing what the badge means.
type badgeSpec struct {
	tool   string // tool name, e.g. "ghpr"
	binary string // absolute path to the tool binary
	manifest.Badge
}

// discoverBadges returns every tool-declared badge provider, ordered by
// Badge.Order then tool name. Discovery failures are non-fatal: a missing
// or broken tool simply contributes no badge. Returns nil in test sockets
// so the fzf-integration tests don't fork tool binaries.
func discoverBadges() []badgeSpec {
	if strings.HasPrefix(os.Getenv("ATELIER_TMUX_SOCKET"), "atelier-test-") {
		return nil
	}
	res, err := plugin.Discover()
	if err != nil || res == nil {
		debuglog.LogErr("workspaces.discoverBadges", err)
		return nil
	}
	var specs []badgeSpec
	for _, p := range res.Plugins {
		if p.Manifest == nil || p.Manifest.Badge == nil || p.Manifest.Badge.Option == "" {
			continue
		}
		specs = append(specs, badgeSpec{tool: p.Name, binary: p.Binary, Badge: *p.Manifest.Badge})
	}
	sort.SliceStable(specs, func(i, j int) bool {
		if specs[i].Order != specs[j].Order {
			return specs[i].Order < specs[j].Order
		}
		return specs[i].tool < specs[j].tool
	})
	return specs
}

// badgeOptionKeys returns the ordered tmux window-option keys the picker
// must read for the given providers (for the list-windows -F format).
func badgeOptionKeys(specs []badgeSpec) []string {
	keys := make([]string, 0, len(specs))
	for _, s := range specs {
		keys = append(keys, s.Option)
	}
	return keys
}

// badgeSort is a resolved row-sort contribution: a window option to read
// and a value→rank lookup. Values not in the order (and empty/unset) rank
// last, so rows without the provider's signal sink below those with it.
type badgeSort struct {
	option string
	rank   map[string]int
	last   int // rank for unlisted/empty values
}

// badgeSorts returns the sort contributions of providers that declare a
// SortOption + SortOrder, in provider order.
func badgeSorts(specs []badgeSpec) []badgeSort {
	var out []badgeSort
	for _, s := range specs {
		if s.SortOption == "" || len(s.SortOrder) == 0 {
			continue
		}
		m := make(map[string]int, len(s.SortOrder))
		for i, v := range s.SortOrder {
			m[v] = i
		}
		out = append(out, badgeSort{option: s.SortOption, rank: m, last: len(s.SortOrder)})
	}
	return out
}

// rankOf returns the sort rank of a value (lower = earlier).
func (b badgeSort) rankOf(v string) int {
	if r, ok := b.rank[strings.TrimSpace(v)]; ok {
		return r
	}
	return b.last
}

// sortOptionKeys returns just the option keys of the sort contributions.
func sortOptionKeys(sorts []badgeSort) []string {
	keys := make([]string, 0, len(sorts))
	for _, s := range sorts {
		keys = append(keys, s.option)
	}
	return keys
}

// spawnBadgeRefreshes pokes each provider that declares a Refresh
// subcommand, detached and best-effort. The tool owns its own staleness
// throttling; the picker just fires once per open. Mirrors the detached
// spawn discipline of workspace.SpawnBgPull (own process group so the
// child survives the popup pty closing).
func spawnBadgeRefreshes(specs []badgeSpec) {
	for _, s := range specs {
		if s.Refresh == "" || s.binary == "" {
			continue
		}
		cmd := exec.Command(s.binary, s.Refresh)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			debuglog.LogErr("workspaces.spawnBadgeRefreshes "+s.tool, err)
			continue
		}
		pid := cmd.Process.Pid
		_ = cmd.Process.Release()
		debuglog.Logf("workspaces.spawnBadgeRefreshes: %s %s pid=%d", s.tool, s.Refresh, pid)
	}
}

// keyToFzf translates an atelier canonical key ("M-o", "C-o") to fzf's
// --bind syntax ("alt-o", "ctrl-o"). Unknown forms pass through unchanged.
// Pure helper — unit-tested.
func keyToFzf(key string) string {
	switch {
	case strings.HasPrefix(key, "M-"):
		return "alt-" + strings.TrimPrefix(key, "M-")
	case strings.HasPrefix(key, "C-"):
		return "ctrl-" + strings.TrimPrefix(key, "C-")
	default:
		return key
	}
}
