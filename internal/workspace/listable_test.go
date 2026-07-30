package workspace

import "testing"

// TestListable locks the single inclusion predicate shared by the M-s picker
// and the status-line attention rollup. A window is listable when it has a
// repo path (worktree kind) OR an AI workspace-kind (repo-less multi-repo);
// with neither it is a raw tmux window or a spent popup and must be invisible
// to both.
func TestListable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		repoPath string
		kind     string
		want     bool
	}{
		{"worktree has repo path", "/home/u/code/repo", "", true},
		{"multi-repo has only kind", "", "multi-repo", true},
		{"both set", "/home/u/code/repo", "multi-repo", true},
		{"neither — raw window or spent popup", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Listable(tc.repoPath, tc.kind); got != tc.want {
				t.Errorf("Listable(%q, %q) = %v, want %v", tc.repoPath, tc.kind, got, tc.want)
			}
		})
	}
}
