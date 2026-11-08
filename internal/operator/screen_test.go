package operator

import "testing"

func TestValidKey(t *testing.T) {
	for _, k := range []string{"Enter", "Escape", "Up", "Down", "Tab", "1", "2", "3", "4", "5", "6", "7", "8", "9", "C-c"} {
		if !ValidKey(k) {
			t.Error(k)
		}
	}
	for _, k := range []string{"", "0", "C-d", "enter", "Enter;kill-server", "F1"} {
		if ValidKey(k) {
			t.Error(k)
		}
	}
}
func TestTrimScreen(t *testing.T) {
	if got := trimScreen(" one  \n\n two\t\n \n\n"); got != " one\n\n two" {
		t.Fatalf("%q", got)
	}
}
