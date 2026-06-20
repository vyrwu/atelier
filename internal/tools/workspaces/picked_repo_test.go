package workspaces

import "testing"

// TestInterpretPickedRepo locks in the empty-pick guard added to
// PickCommand. Previously a become() chain ending in `abort` upstream
// would return ("", nil) from fzf.Pick, and PickCommand would call
// runWorkspaceName(repo="", ...) — opening the name picker on a
// phantom empty repo that needed an explicit M-n to dismiss.
func TestInterpretPickedRepo(t *testing.T) {
	cases := []struct {
		name        string
		picked      string
		wantRepo    string
		wantCancel  bool
	}{
		{"empty string", "", "", true},
		{"whitespace only", "   \t  ", "", true},
		{"newline only", "\n", "", true},
		{"repo name alone", "atelier", "atelier", false},
		{"repo with metadata after tab", "atelier\tmain", "atelier", false},
		{"repo with multiple tabs", "atelier\tmain\textra", "atelier", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, cancelled := interpretPickedRepo(tc.picked)
			if repo != tc.wantRepo || cancelled != tc.wantCancel {
				t.Errorf("interpretPickedRepo(%q) = (%q, %v), want (%q, %v)",
					tc.picked, repo, cancelled, tc.wantRepo, tc.wantCancel)
			}
		})
	}
}
