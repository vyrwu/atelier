package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestRenderCheatsheet_IncludesQuit locks in that the M-q exit
// binding shows up in the M-? popup so users can discover it without
// reading source. FR-5.3: M-q now detaches; the label reflects that.
func TestRenderCheatsheet_IncludesQuit(t *testing.T) {
	var buf bytes.Buffer
	renderCheatsheet(&buf)
	if !strings.Contains(buf.String(), "M-q") {
		t.Errorf("cheatsheet missing M-q row. full output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Detach") {
		t.Errorf("cheatsheet missing 'Detach' label for M-q. full output:\n%s", buf.String())
	}
}

// TestFormatFreshnessIcon covers the FR-7 status-line freshness
// rendering. Each case pins one branch of the icon-choice logic;
// regressions here would either silently hide sync state or
// misrender it on the user's status bar.
func TestFormatFreshnessIcon(t *testing.T) {
	cases := []struct {
		name        string
		behind      string
		ahead       string
		pullError   string
		freshnessTs string
		repoPath    string
		want        string // exact match (incl. leading space + tmux color codes)
		wantEmpty   bool
	}{
		{
			name:      "non-git session → empty",
			repoPath:  "",
			wantEmpty: true,
		},
		{
			name:        "git session, pull pending → empty",
			repoPath:    "/r",
			freshnessTs: "",
			wantEmpty:   true,
		},
		{
			name:      "pull error wins over everything else; emits short message",
			pullError: "fetch failed",
			repoPath:  "/r",
			want:      " #[fg=red]⚠ fetch failed#[default]",
		},
		{
			name:      "long pull error truncated with ellipsis",
			pullError: "this is a very long error message that should be clamped to fit on the status bar",
			repoPath:  "/r",
			want:      " #[fg=red]⚠ this is a very long error mes…#[default]",
		},
		{
			name:        "in-sync → dim ✓",
			behind:      "0",
			ahead:       "0",
			freshnessTs: "1729094400",
			repoPath:    "/r",
			want:        " #[fg=green]✔#[default]",
		},
		{
			name:        "behind only → red ↓N (needs pull)",
			behind:      "3",
			ahead:       "0",
			freshnessTs: "1729094400",
			repoPath:    "/r",
			want:        " #[fg=red]↓3#[default]",
		},
		{
			name:        "ahead only → yellow ↑N",
			behind:      "0",
			ahead:       "2",
			freshnessTs: "1729094400",
			repoPath:    "/r",
			want:        " #[fg=yellow]↑2#[default]",
		},
		{
			name:        "diverged → red ↓N↑M (behind dominates)",
			behind:      "3",
			ahead:       "2",
			freshnessTs: "1729094400",
			repoPath:    "/r",
			want:        " #[fg=red]↓3↑2#[default]",
		},
		{
			name:        "garbage counts treated as zero",
			behind:      "nan",
			ahead:       "",
			freshnessTs: "1",
			repoPath:    "/r",
			want:        " #[fg=green]✔#[default]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatFreshnessIcon(tc.behind, tc.ahead, tc.pullError, tc.freshnessTs, tc.repoPath)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("formatFreshnessIcon(%q,%q,%q,%q,%q) = %q, want %q",
					tc.behind, tc.ahead, tc.pullError, tc.freshnessTs, tc.repoPath,
					got, tc.want)
			}
		})
	}
}

// TestFormatFreshnessIcon_PadsLeadingSpace verifies the icon always
// starts with a space when non-empty. Without this, the freshness
// segment would kiss the window name in the status bar.
func TestFormatFreshnessIcon_PadsLeadingSpace(t *testing.T) {
	for name, args := range map[string][5]string{
		"error":    {"", "", "x", "", "/r"},
		"in-sync":  {"0", "0", "", "1", "/r"},
		"behind":   {"3", "0", "", "1", "/r"},
		"ahead":    {"0", "3", "", "1", "/r"},
		"diverged": {"3", "1", "", "1", "/r"},
	} {
		t.Run(name, func(t *testing.T) {
			got := formatFreshnessIcon(args[0], args[1], args[2], args[3], args[4])
			if got == "" {
				t.Fatalf("expected non-empty, got empty")
			}
			if !strings.HasPrefix(got, " ") {
				t.Errorf("expected leading space, got %q", got)
			}
		})
	}
}
