package workspaces

import (
	"strings"
	"testing"
)

func TestParseSortMode_RoundTripAndDefault(t *testing.T) {
	for _, m := range sortModeOrder {
		if got := parseSortMode(m.String()); got != m {
			t.Errorf("parseSortMode(%q) = %v, want %v", m.String(), got, m)
		}
	}
	// Unknown / empty (unset global) falls back to the Attention default.
	for _, s := range []string{"", "  ", "bogus", "Attention"} {
		if got := parseSortMode(s); got != sortAttention {
			t.Errorf("parseSortMode(%q) = %v, want sortAttention", s, got)
		}
	}
}

func TestSortMode_NextCyclesAndWraps(t *testing.T) {
	m := sortAttention
	seen := map[sortMode]bool{}
	for range sortModeOrder {
		if seen[m] {
			t.Fatalf("next() revisited %v before covering all modes", m)
		}
		seen[m] = true
		m = m.next()
	}
	if m != sortAttention {
		t.Errorf("next() did not wrap back to the first mode; landed on %v", m)
	}
	if len(seen) != len(sortModeOrder) {
		t.Errorf("cycle covered %d modes, want %d", len(seen), len(sortModeOrder))
	}
}

// fixtures for sortEntries — identified by their (unique) window name.
func sortFixture() []sessionEntry {
	return []sessionEntry{
		{window: "feat/x", session: "repo-a", isAttn: true, isDefault: false, createdTs: 100, forgeRank: 4},
		{window: "main", session: "repo-a", isAttn: false, isDefault: true, createdTs: 50, forgeRank: 4},
		{window: "feat/y", session: "repo-b", isAttn: true, isDefault: false, createdTs: 200, tag: "client", forgeRank: 4},
		{window: "feat/z", session: "repo-b", isAttn: false, isDefault: false, createdTs: 300, tag: "client", forgeRank: 0},
	}
}

func windowOrder(entries []sessionEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.window
	}
	return out
}

func TestSortEntries_ByMode(t *testing.T) {
	cases := []struct {
		mode sortMode
		want []string
	}{
		// Attention first; within a group non-default before default, then
		// newest-created first.
		{sortAttention, []string{"feat/y", "feat/x", "feat/z", "main"}},
		// Oldest created first (GC surface).
		{sortAge, []string{"main", "feat/x", "feat/y", "feat/z"}},
		// Grouped by repo; non-default before the repo's default branch.
		{sortRepo, []string{"feat/x", "main", "feat/y", "feat/z"}},
		// Tagged first (by tag), untagged last.
		{sortTag, []string{"feat/y", "feat/z", "feat/x", "main"}},
		// PR state: open (rank 0) first, rest by newest-created.
		{sortForge, []string{"feat/z", "feat/y", "feat/x", "main"}},
	}
	for _, tc := range cases {
		t.Run(tc.mode.String(), func(t *testing.T) {
			entries := sortFixture()
			sortEntries(entries, tc.mode)
			got := windowOrder(entries)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("sortEntries(%v) order = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}

// Unknown creation time (createdTs 0) sinks to the bottom under Age sort
// rather than floating to the top as a spurious "oldest".
func TestSortEntries_Age_UnknownSinks(t *testing.T) {
	entries := []sessionEntry{
		{window: "unknown", session: "r", createdTs: 0},
		{window: "old", session: "r", createdTs: 10},
		{window: "new", session: "r", createdTs: 99},
	}
	sortEntries(entries, sortAge)
	if got := windowOrder(entries); strings.Join(got, ",") != "old,new,unknown" {
		t.Errorf("Age order = %v, want [old new unknown]", got)
	}
}

func TestSessionFooter_ShowsModeAndForge(t *testing.T) {
	f := sessionFooter(sortAge, false)
	if !strings.Contains(f, "Sort: Age") {
		t.Errorf("footer missing active mode label; got %q", f)
	}
	if !strings.Contains(f, "\033[33m") {
		t.Errorf("footer missing yellow SGR for the sort legend; got %q", f)
	}
	if !strings.Contains(f, "Tab · sort") {
		t.Errorf("footer missing Tab hint; got %q", f)
	}
	if strings.Contains(f, "M-o") {
		t.Errorf("footer should omit the open-PR hint when no forge is active; got %q", f)
	}
	if !strings.Contains(sessionFooter(sortAttention, true), "M-o") {
		t.Errorf("footer should include the open-PR hint when a forge is active")
	}
}
