package workspaces

import (
	"fmt"
	"testing"
	"time"
)

// TestFormatAge locks in the relative-time helper used for the picker's
// "last used" suffix (and previously the recap age): short forms
// (s/m/h/d), empty-on-bogus-input so the picker doesn't render
// confusing "· 0s" entries.
func TestFormatAge(t *testing.T) {
	now := time.Unix(2_000_000_000, 0) // fixed reference
	ts := func(secsAgo int) string {
		return fmt.Sprintf("%d", now.Unix()-int64(secsAgo))
	}
	cases := []struct {
		name string
		ts   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"non-numeric", "yesterday", ""},
		{"zero", "0", ""},
		{"future timestamp", fmt.Sprintf("%d", now.Unix()+60), ""},
		{"30 seconds", ts(30), "30s"},
		{"just below minute", ts(59), "59s"},
		{"one minute", ts(60), "1m"},
		{"5 minutes", ts(300), "5m"},
		{"just below hour", ts(3599), "59m"},
		{"one hour", ts(3600), "1h"},
		{"2 hours", ts(7200), "2h"},
		{"23 hours", ts(23 * 3600), "23h"},
		{"one day", ts(86400), "1d"},
		{"3 days", ts(3 * 86400), "3d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAge(now, tc.ts); got != tc.want {
				t.Errorf("formatAge(%q) = %q, want %q", tc.ts, got, tc.want)
			}
		})
	}
}
