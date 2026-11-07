package tmux

import "testing"

func TestServerAbsent(t *testing.T) {
	for _, message := range []string{
		"no server running on /tmp/tmux-1/test\n",
		"error connecting to /tmp/tmux-1/test (No such file or directory)\n",
		"error connecting to /tmp/tmux-1/test (Connection refused)\n",
	} {
		if !serverAbsent(message) {
			t.Errorf("absent server not recognized: %q", message)
		}
	}
	for _, message := range []string{"", "permission denied", "error connecting to /tmp/test (Permission denied)", "server exited unexpectedly", "context deadline exceeded"} {
		if serverAbsent(message) {
			t.Errorf("query failure treated as absence: %q", message)
		}
	}
}
