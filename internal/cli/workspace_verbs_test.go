package cli

import "testing"

// TestParsePRURL locks the PR-URL parser used by `atelier pr register` and the
// MCP pr_register tool: it must accept the canonical https form, the ssh form,
// and the .git-suffixed clone form, extracting (owner/repo, number). Malformed
// input returns ok=false so the caller can surface a clear error rather than
// register a bogus PR.
func TestParsePRURL(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		wantRepo   string
		wantNumber int
		wantOK     bool
	}{
		{
			name:       "canonical https pull URL",
			url:        "https://github.com/vyrwu/atelier/pull/123",
			wantRepo:   "vyrwu/atelier",
			wantNumber: 123,
			wantOK:     true,
		},
		{
			name:       "bare github.com host, no scheme",
			url:        "github.com/o/r/pull/7",
			wantRepo:   "o/r",
			wantNumber: 7,
			wantOK:     true,
		},
		{
			name:       "ssh clone form with .git suffix",
			url:        "git@github.com:owner/repo.git/pull/42",
			wantRepo:   "owner/repo",
			wantNumber: 42,
			wantOK:     true,
		},
		{
			name:       "https with .git suffix",
			url:        "https://github.com/owner/repo.git/pull/9",
			wantRepo:   "owner/repo",
			wantNumber: 9,
			wantOK:     true,
		},
		{
			name:       "pulls (plural) path segment accepted",
			url:        "https://github.com/o/r/pulls/3",
			wantRepo:   "o/r",
			wantNumber: 3,
			wantOK:     true,
		},
		{
			name:       "extra path/query after number still parses",
			url:        "https://github.com/o/r/pull/55/files",
			wantRepo:   "o/r",
			wantNumber: 55,
			wantOK:     true,
		},
		{
			name:   "issue URL (not a pull) rejected",
			url:    "https://github.com/o/r/issues/1",
			wantOK: false,
		},
		{
			name:   "missing number rejected",
			url:    "https://github.com/o/r/pull/",
			wantOK: false,
		},
		{
			name:   "non-github host rejected",
			url:    "https://gitlab.com/o/r/pull/1",
			wantOK: false,
		},
		{
			name:   "empty string rejected",
			url:    "",
			wantOK: false,
		},
		{
			name:   "garbage rejected",
			url:    "not a url at all",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, number, ok := parsePRURL(tc.url)
			if ok != tc.wantOK {
				t.Fatalf("parsePRURL(%q) ok = %v, want %v", tc.url, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if repo != tc.wantRepo {
				t.Errorf("parsePRURL(%q) repo = %q, want %q", tc.url, repo, tc.wantRepo)
			}
			if number != tc.wantNumber {
				t.Errorf("parsePRURL(%q) number = %d, want %d", tc.url, number, tc.wantNumber)
			}
		})
	}
}
