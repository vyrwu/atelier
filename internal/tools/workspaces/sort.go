package workspaces

import (
	"math"
	"sort"
	"strings"

	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// The M-a (Active Workspaces) picker sorts by a user-cycled mode rather
// than one fixed priority key. Tab cycles the mode; the choice persists as
// a tmux global (OptSortMode) so it's sticky across opens within a server
// lifetime. Attention is the default — "what wants my eyes" — with Age
// (oldest first, a GC signal), Repo, Tag, and Forge as the alternatives.
//
// Discoverability, not cleverness: the old scheme folded attention,
// agent-presence, default-branch, and forge state into a single opaque 0-8
// priority. Collapsing that (agents are everywhere now; forge is a badge,
// not a ranking) leaves attention as the one meaningful default axis — thin
// enough that a fixed sort is arbitrary, which is exactly why the mode is
// the user's to pick.

type sortMode int

const (
	sortAttention sortMode = iota // attention first (default)
	sortAge                       // oldest first — surfaces GC candidates
	sortRepo                      // grouped by repo (session) name
	sortTag                       // tagged first by tag; untagged last
	sortForge                     // PR state: open→draft→merged→closed→none
)

// OptSortMode is the tmux global that persists the picker's active sort
// across opens (sticky within a tmux server lifetime, not across restarts —
// a session preference, not durable workspace state).
const OptSortMode = "@ms_sort"

// sortModeOrder is the Tab cycle order.
var sortModeOrder = []sortMode{sortAttention, sortAge, sortRepo, sortTag, sortForge}

// String is the persisted token written to the tmux global.
func (m sortMode) String() string {
	switch m {
	case sortAge:
		return "age"
	case sortRepo:
		return "repo"
	case sortTag:
		return "tag"
	case sortForge:
		return "forge"
	default:
		return "attention"
	}
}

// label is the human-facing name shown in the footer legend.
func (m sortMode) label() string {
	switch m {
	case sortAge:
		return "Age"
	case sortRepo:
		return "Repo"
	case sortTag:
		return "Tag"
	case sortForge:
		return "Forge"
	default:
		return "Attention"
	}
}

// parseSortMode maps a persisted token back to a mode; anything unknown
// (including the empty string for an unset global) is the Attention default.
func parseSortMode(s string) sortMode {
	switch strings.TrimSpace(s) {
	case "age":
		return sortAge
	case "repo":
		return sortRepo
	case "tag":
		return sortTag
	case "forge":
		return sortForge
	default:
		return sortAttention
	}
}

// next returns the following mode in the Tab cycle, wrapping around.
func (m sortMode) next() sortMode {
	for i, mm := range sortModeOrder {
		if mm == m {
			return sortModeOrder[(i+1)%len(sortModeOrder)]
		}
	}
	return sortAttention
}

func readSortMode(h *tmuxhost.Client) sortMode {
	v, _ := h.ShowGlobalOption(OptSortMode)
	return parseSortMode(v)
}

func writeSortMode(h *tmuxhost.Client, m sortMode) {
	_ = h.SetGlobalOption(OptSortMode, m.String())
}

// sessionFooter renders the M-a picker's footer: the active sort mode as a
// yellow "Sort: <mode>" legend on the left (fzf renders footer ANSI under
// --ansi), then the keybinding hints. forge=true appends the M-o open-PR
// hint. Pure — used both for the initial footer and the Tab change-footer.
func sessionFooter(mode sortMode, forge bool) string {
	f := "\033[33mSort: " + mode.label() + "\033[0m  |  Tab · sort" +
		"  |  M-x · delete  |  M-t · tag  |  M-n · creator" +
		"  |  M-r · history  |  M-; · tools  |  M-u · clone url"
	if forge {
		f += "  |  M-o · open PR"
	}
	return f
}

// sessionEntry is one picker row plus the raw signals the sort modes read.
// Kept separate from SessionRow (the render payload) so sortEntries stays a
// pure function over the signals, independently testable without tmux.
type sessionEntry struct {
	row       SessionRow
	isAttn    bool
	isDefault bool // default-branch window of a repo (sinks within a group)
	createdTs int64
	session   string
	window    string
	tag       string
	forgeRank int
}

// sortEntries orders the picker rows in place per the active mode. Every
// mode ends with a (session, window) tiebreak so the result is fully
// deterministic regardless of tmux's list-windows order — tests depend on
// this. Pure.
func sortEntries(entries []sessionEntry, mode sortMode) {
	sort.SliceStable(entries, func(i, j int) bool {
		return lessBy(entries[i], entries[j], mode)
	})
}

func lessBy(a, b sessionEntry, mode sortMode) bool {
	switch mode {
	case sortAge:
		// Oldest first so GC candidates surface at the top. Unknown age
		// (createdTs 0) sorts last, not first.
		if ka, kb := ageKey(a.createdTs), ageKey(b.createdTs); ka != kb {
			return ka < kb
		}
	case sortRepo:
		if a.session != b.session {
			return a.session < b.session
		}
		if a.isDefault != b.isDefault {
			return !a.isDefault // non-default before the repo's default branch
		}
	case sortTag:
		if at, bt := a.tag != "", b.tag != ""; at != bt {
			return at // tagged before untagged
		}
		if a.tag != b.tag {
			return a.tag < b.tag
		}
	case sortForge:
		if a.forgeRank != b.forgeRank {
			return a.forgeRank < b.forgeRank
		}
		if a.createdTs != b.createdTs {
			return a.createdTs > b.createdTs // newer first within a PR state
		}
	default: // sortAttention
		if a.isAttn != b.isAttn {
			return a.isAttn // attention first
		}
		if a.isDefault != b.isDefault {
			return !a.isDefault // non-default before default within a group
		}
		if a.createdTs != b.createdTs {
			return a.createdTs > b.createdTs // newer first
		}
	}
	if a.session != b.session {
		return a.session < b.session
	}
	return a.window < b.window
}

// ageKey maps a creation timestamp to its Age-sort key: unknown (0) becomes
// +inf so it sinks to the bottom under ascending (oldest-first) order.
func ageKey(ts int64) int64 {
	if ts <= 0 {
		return math.MaxInt64
	}
	return ts
}
