package workspace

import "testing"

// TestListable locks the single inclusion predicate shared by the M-s picker
// and the status-line attention rollup. In the intent-workspace model the
// marker is the session's @workspace_id (resolved at window scope via tmux
// option inheritance): a window with it belongs to a real workspace; a window
// without it is a raw shell or a spent popup and must be invisible to both.
func TestListable(t *testing.T) {
	for _, tc := range []struct {
		name        string
		workspaceID string
		want        bool
	}{
		{"has @workspace_id", "vyrwu/atelier", true},
		{"empty — raw window or spent popup", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Listable(tc.workspaceID); got != tc.want {
				t.Errorf("Listable(%q) = %v, want %v", tc.workspaceID, got, tc.want)
			}
		})
	}
}
