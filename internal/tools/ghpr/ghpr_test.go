package ghpr

import (
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		state   string
		isDraft bool
		want    string
	}{
		{"OPEN", false, "open"},
		{"open", false, "open"},
		{"OPEN", true, "draft"},
		{"MERGED", false, "merged"},
		{"MERGED", true, "merged"}, // merged wins over draft
		{"CLOSED", false, "closed"},
		{"CLOSED", true, "closed"},
		{" open ", false, "open"}, // trimmed
		{"", false, "open"},       // unexpected → treat as open
	}
	for _, c := range cases {
		if got := classify(c.state, c.isDraft); got != c.want {
			t.Errorf("classify(%q, %v) = %q, want %q", c.state, c.isDraft, got, c.want)
		}
	}
}

func TestRenderBadge(t *testing.T) {
	// Each known state renders a non-empty, leading-space, ANSI-wrapped
	// token; colors are distinct per state so the states are visually
	// distinguishable even with the same glyph.
	seen := map[string]string{}
	for _, state := range []string{"open", "draft", "merged", "closed"} {
		got := renderBadge(state)
		if !strings.HasPrefix(got, " \033[38;5;") || !strings.HasSuffix(got, "\033[0m") {
			t.Errorf("renderBadge(%q) = %q, want leading space + ANSI color wrap", state, got)
		}
		if prev, ok := seen[got]; ok {
			t.Errorf("renderBadge(%q) collides with %q (both %q)", state, prev, got)
		}
		seen[got] = state
	}
	if got := renderBadge("nonsense"); got != "" {
		t.Errorf("renderBadge(unknown) = %q, want empty", got)
	}
}

func TestParseRow(t *testing.T) {
	cases := []struct {
		row             string
		session, window string
		ok              bool
	}{
		{"vyrwu/demo\tfeat/x\t  ❯vyrwu/demo/feat/x", "vyrwu/demo", "feat/x", true},
		{"s\tw", "s", "w", true},
		{"only-one-field", "", "", false},
		{"\tw", "", "", false}, // empty session
		{"s\t", "", "", false}, // empty window
		{"", "", "", false},
	}
	for _, c := range cases {
		s, w, ok := parseRow(c.row)
		if s != c.session || w != c.window || ok != c.ok {
			t.Errorf("parseRow(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.row, s, w, ok, c.session, c.window, c.ok)
		}
	}
}

func TestFresh(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		ts   string
		want bool
	}{
		{"1000000", true},                      // just now
		{"999900", true},                       // 100s ago < TTL
		{strings.TrimSpace("  999900 "), true}, // trimmed
		{"999999", true},                       // 1s ago
		{"999699", false},                      // 301s ago > TTL
		{"", false},                            // missing
		{"nope", false},                        // unparseable
		{"0", false},                           // zero
		{"-5", false},                          // negative
	}
	for _, c := range cases {
		if got := fresh(now, c.ts); got != c.want {
			t.Errorf("fresh(now, %q) = %v, want %v", c.ts, got, c.want)
		}
	}
}
