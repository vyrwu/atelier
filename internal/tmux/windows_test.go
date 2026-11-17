package tmux

import (
	"testing"
	"time"
)

func TestParseWindows(t *testing.T) {
	out := "spry-otter\t@3\tshell\t0\t0\t4242\t1700000000\tssh\t/dev/ttys004\t/tmp/a\tb\n" +
		"spry-otter\t@1\tclaude\t1\t1\t17\t\tclaude\t/dev/ttys001\t/home/me\n" +
		"garbage\n"
	got := parseWindows(out)
	if len(got) != 2 {
		t.Fatalf("parsed %d windows from %q", len(got), out)
	}
	want := Window{Session: "spry-otter", ID: "@3", Name: "shell", PID: 4242,
		Activity: time.Unix(1700000000, 0), Command: "ssh", TTY: "/dev/ttys004", Path: "/tmp/a\tb"} // a tab in the path stays in the path
	if got[0] != want {
		t.Errorf("window = %+v\n   want %+v", got[0], want)
	}
	if w := got[1]; !w.Active || !w.Dead || !w.Activity.IsZero() {
		t.Errorf("flags or a missing time misread: %+v", w)
	}
}
