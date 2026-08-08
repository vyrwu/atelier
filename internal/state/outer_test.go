package state

import (
	"errors"
	"testing"
)

// dmTarget seeds the fake's DisplayMessageAt answers for a session/window.
func (f *fakeHost) dmTarget(target, name, sid, wid, pid string) {
	f.dm[target] = map[string]string{
		"#{session_name}": name,
		"#{session_id}":   sid,
		"#{window_id}":    wid,
		"#{pane_id}":      pid,
	}
}

func TestSetOuter_StampsWorkspace(t *testing.T) {
	h := newFakeHost()
	h.dmTarget("vyrwu/atelier", "vyrwu/atelier", "$1", "@2", "%3")
	if err := SetOuter(h, "=vyrwu/atelier", ""); err != nil {
		t.Fatalf("SetOuter: %v", err)
	}
	if h.globals[OptOuterSession] != "$1" || h.globals[OptOuterWindow] != "@2" || h.globals[OptOuterPane] != "%3" {
		t.Errorf("globals = %+v, want session=$1 window=@2 pane=%%3", h.globals)
	}
}

func TestSetOuter_RefusesLauncher(t *testing.T) {
	h := newFakeHost()
	h.dmTarget("default", "default", "$0", "@0", "%0")
	err := SetOuter(h, "=default", "")
	if !errors.Is(err, ErrOuterNotWorkspace) {
		t.Fatalf("err = %v, want ErrOuterNotWorkspace", err)
	}
	if len(h.globals) != 0 {
		t.Errorf("no globals should be written, got %+v", h.globals)
	}
}

func TestSetOuter_RefusesPopup(t *testing.T) {
	h := newFakeHost()
	h.dmTarget("_atelier_claude_1_2", "_atelier_claude_1_2", "$2", "@5", "%6")
	if err := SetOuter(h, "_atelier_claude_1_2", ""); !errors.Is(err, ErrOuterNotWorkspace) {
		t.Fatalf("err = %v, want ErrOuterNotWorkspace", err)
	}
}

func TestSetOuter_RefusesUnderscoreSession(t *testing.T) {
	h := newFakeHost()
	h.dmTarget("_scratch", "_scratch", "$5", "@6", "%7")
	if err := SetOuter(h, "_scratch", ""); !errors.Is(err, ErrOuterNotWorkspace) {
		t.Fatalf("err = %v, want ErrOuterNotWorkspace for a lone-underscore session", err)
	}
	if len(h.globals) != 0 {
		t.Errorf("no globals should be written, got %+v", h.globals)
	}
}

func TestSetOuter_RefusesStale(t *testing.T) {
	h := newFakeHost() // no dm targets → DisplayMessageAt errors
	if err := SetOuter(h, "=gone", ""); !errors.Is(err, ErrOuterStale) {
		t.Fatalf("err = %v, want ErrOuterStale", err)
	}
}

func TestOuterHint(t *testing.T) {
	cases := []struct {
		name      string
		outerSess string
		sessName  string // DisplayMessageAt session_name; "" => unresolvable/stale
		wantOK    bool
	}{
		{"valid workspace", "$1", "vyrwu/atelier", true},
		{"launcher", "$0", "default", false},
		{"popup", "$2", "_atelier_claude_1_2", false},
		{"stale (unresolvable id)", "$9", "", false},
		{"empty pointer", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newFakeHost()
			if c.outerSess != "" {
				h.globals[OptOuterSession] = c.outerSess
				h.globals[OptOuterWindow] = "@2"
				h.globals[OptOuterPane] = "%3"
				if c.sessName != "" {
					h.dm[c.outerSess] = map[string]string{"#{session_name}": c.sessName}
				}
			}
			sess, win, pane, ok := OuterHint(h)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ok && (sess != c.outerSess || win != "@2" || pane != "%3") {
				t.Errorf("valid hint returned (%q,%q,%q), want ($..,@2,%%3)", sess, win, pane)
			}
			if !ok && (sess != "" || win != "" || pane != "") {
				t.Errorf("invalid hint must return empties, got (%q,%q,%q)", sess, win, pane)
			}
		})
	}
}

func TestResolveOuter_ValidNoCorrection(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$1|vyrwu/atelier")
	h.windows = []string{"$1 @2"}
	h.setClients(clientLine("/dev/ttys001", "vyrwu/atelier", "$1", "@2", "/dev/ttys001"))
	h.globals[OptOuterSession] = "$1"
	h.globals[OptOuterWindow] = "@2"

	top, _ := CaptureTopology(h)
	got, corrected := ResolveOuter(top)
	if corrected {
		t.Errorf("valid outer should not be corrected; got %+v", got)
	}
}

func TestResolveOuter_SelfCorrectsBadOuter(t *testing.T) {
	h := newFakeHost()
	// Outer pointer stamped onto the launcher — the bug. A workspace client
	// is attached, so ResolveOuter must self-correct to it.
	h.setSessionsWithIDs("$0|default", "$1|vyrwu/atelier")
	h.windows = []string{"$1 @2", "$0 @0"}
	h.setClients(clientLine("/dev/ttys001", "vyrwu/atelier", "$1", "@2", "/dev/ttys001"))
	h.globals[OptOuterSession] = "$0" // launcher

	top, _ := CaptureTopology(h)
	got, corrected := ResolveOuter(top)
	if !corrected {
		t.Fatal("launcher outer must be corrected")
	}
	if got.Session != "$1" || got.Window != "@2" {
		t.Errorf("corrected outer = %+v, want session=$1 window=@2", got)
	}
}
