package dispatch

import "testing"

// TestToolCmd locks in the canonical shell-string shape every
// tmux/fzf binding expects: `atelier tools <name> <args...>`.
//
// Touchstone for the "no callsite duplication" rule — if any future
// callsite hand-rolls this format and drifts from these test cases,
// the binding fails silently at runtime.
func TestToolCmd(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args []string
		want string
	}{
		{"no args", "workspaces", nil, "atelier tools workspaces"},
		{"single subcmd", "workspaces", []string{"sessions"},
			"atelier tools workspaces sessions"},
		{"hidden subcmd with placeholder", "workspaces",
			[]string{"_delete-row", "{}"},
			"atelier tools workspaces _delete-row {}"},
		{"multiple positional args", "workspaces",
			[]string{"_bg-pull", "/repo", "main", "@1"},
			"atelier tools workspaces _bg-pull /repo main @1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToolCmd(tc.tool, tc.args...); got != tc.want {
				t.Errorf("ToolCmd(%q, %v) = %q, want %q",
					tc.tool, tc.args, got, tc.want)
			}
		})
	}
}

func TestCoreCmd(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"single subcmd", []string{"state", "restore"},
			"atelier state restore"},
		{"two-level subcmd", []string{"internal", "stamp-statusline"},
			"atelier internal stamp-statusline"},
		{"with positional", []string{"internal", "stamp-last-active", "default"},
			"atelier internal stamp-last-active default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CoreCmd(tc.args...); got != tc.want {
				t.Errorf("CoreCmd(%v) = %q, want %q",
					tc.args, got, tc.want)
			}
		})
	}
}
