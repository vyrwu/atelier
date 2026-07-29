package workspace

import "testing"

// TestShellReturnHookAction locks in the one-shot client-detached hook
// body ReturnToOuterShell installs: select the recorded outer window,
// then unset the hook so it fires exactly once. Critically it must target
// the passed window id (a real @-id from @atelier_outer_window), never the
// literal ":1" that used to yank the user to a different workspace.
func TestShellReturnHookAction(t *testing.T) {
	got := shellReturnHookAction("@42")
	want := "select-window -t @42 ; set-hook -ug client-detached"
	if got != want {
		t.Errorf("shellReturnHookAction(@42) = %q, want %q", got, want)
	}
	if got == "select-window -t :1 ; set-hook -ug client-detached" {
		t.Error("hook still targets window index :1 — the switch-workspace bug")
	}
}
