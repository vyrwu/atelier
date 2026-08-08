package state

import (
	"strings"
	"testing"
)

func TestCaptureTopology_CapturesWindowCapability(t *testing.T) {
	liveDir := t.TempDir() // exists on disk
	h := newFakeHost()
	h.setSessionsWithIDs("$1|vyrwu/atelier")
	h.runOut["list-windows"] = strings.Join([]string{
		windowLine("$1", "@2", "feat/x", "/repo", "auto", "1", "did a thing", "mytag", "open", liveDir),
		windowLine("$1", "@3", "gone", "/repo", "", "0", "", "", "", "/no/such/dir/xyzzy"),
	}, "\n")
	h.setClients()

	top, err := CaptureTopology(h)
	if err != nil {
		t.Fatalf("CaptureTopology: %v", err)
	}
	if len(top.Windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(top.Windows))
	}
	w0 := top.Windows[0]
	if w0.Name != "feat/x" || w0.RepoPath != "/repo" || w0.WorkspaceKind != "auto" ||
		!w0.Attention || w0.Recap != "did a thing" || w0.Tag != "mytag" || w0.ForgeState != "open" {
		t.Errorf("capability parse wrong: %+v", w0)
	}
	if !w0.PaneCwdLive {
		t.Error("existing pane cwd should be live")
	}
	if top.Windows[1].PaneCwdLive {
		t.Error("missing pane cwd should be dead")
	}
}

func TestCaptureTopology_RecapWithPipeSurvives(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$1|vyrwu/atelier")
	// A recap containing '|' must not shift the fixed fields (why winSep is \x1f).
	h.runOut["list-windows"] = windowLine("$1", "@2", "feat/x", "/repo", "auto", "1", "fixed A | improved B", "", "", "")
	h.setClients()
	top, err := CaptureTopology(h)
	if err != nil {
		t.Fatalf("CaptureTopology: %v", err)
	}
	w := top.Windows[0]
	if w.RepoPath != "/repo" || w.WorkspaceKind != "auto" || !w.Attention || w.Recap != "fixed A | improved B" {
		t.Errorf("pipe in recap corrupted fields: %+v", w)
	}
}

func TestCaptureTopology_ClassifiesSessionsAndClients(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs(
		"$0|default",
		"$1|vyrwu/atelier",
		"$2|_atelier_claude_1_2",
	)
	h.windows = []string{"$1 @2", "$0 @0"}
	h.setClients(
		clientLine("/dev/ttys000", "default", "$0", "@0", "/dev/ttys000"),
		clientLine("/dev/ttys001", "vyrwu/atelier", "$1", "@2", "/dev/ttys001"),
		clientLine("/dev/ttys002", "_atelier_claude_1_2", "$2", "@5", "/dev/ttys002"),
	)

	top, err := CaptureTopology(h)
	if err != nil {
		t.Fatalf("CaptureTopology: %v", err)
	}

	if got := len(top.Sessions); got != 3 {
		t.Fatalf("sessions = %d, want 3", got)
	}
	if s, _ := top.SessionByName("default"); s.Kind != KindLauncher {
		t.Errorf("default kind = %v, want launcher", s.Kind)
	}
	if s, _ := top.SessionByName("_atelier_claude_1_2"); s.Kind != KindPopup || s.Popup.Tool != "claude" {
		t.Errorf("popup session parsed wrong: %+v", s)
	}

	// The launcher client is neither outer nor inner — the core filter.
	outers := top.OuterClients()
	if len(outers) != 1 || outers[0].Session != "vyrwu/atelier" {
		t.Errorf("OuterClients = %+v, want just the workspace client", outers)
	}
	inners := top.InnerClients()
	if len(inners) != 1 || inners[0].Session != "_atelier_claude_1_2" {
		t.Errorf("InnerClients = %+v, want just the popup client", inners)
	}
}

func TestClassifyClients_UnderscoreSessionIsNonOuter(t *testing.T) {
	h := newFakeHost()
	h.setClients(
		clientLine("/dev/a", "vyrwu/atelier", "$1", "@2", "/dev/a"),
		clientLine("/dev/b", "_scratch", "$5", "@6", "/dev/b"), // lone underscore, NOT a recognized popup
		clientLine("/dev/c", "default", "$0", "@0", "/dev/c"),
	)
	cs, err := ClassifyClients(h)
	if err != nil {
		t.Fatalf("ClassifyClients: %v", err)
	}
	want := map[string]ClientKind{"/dev/a": ClientWorkspace, "/dev/b": ClientPopup, "/dev/c": ClientLauncher}
	for _, c := range cs {
		if want[c.Name] != c.Kind {
			t.Errorf("client %s (session %s) kind = %v, want %v", c.Name, c.Session, c.Kind, want[c.Name])
		}
	}
	// The stray underscore session must not be an outer candidate.
	top := &Topology{Clients: cs}
	for _, o := range top.OuterClients() {
		if o.Session == "_scratch" {
			t.Error("_scratch must not be offered as an outer client")
		}
	}
}

func TestClassifyClients_DropsMalformedLines(t *testing.T) {
	h := newFakeHost()
	h.setClients(
		clientLine("/dev/a", "vyrwu/atelier", "$1", "@2", "/dev/a"),
		"garbage-no-pipes",
		"a|b|c", // too few fields
		"",
	)
	cs, err := ClassifyClients(h)
	if err != nil {
		t.Fatalf("ClassifyClients: %v", err)
	}
	if len(cs) != 1 || cs[0].Name != "/dev/a" {
		t.Fatalf("malformed lines not dropped: %+v", cs)
	}
}

func TestTopology_PopupParentLiveness(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$1|vyrwu/atelier", "$2|_atelier_claude_1_2", "$3|_atelier_lazygit_9_9")
	h.windows = []string{"$1 @2"} // only $1@2 is live
	h.setClients()

	top, err := CaptureTopology(h)
	if err != nil {
		t.Fatalf("CaptureTopology: %v", err)
	}
	live, _ := ParsePopup("_atelier_claude_1_2")  // parent $1@2 → live
	dead, _ := ParsePopup("_atelier_lazygit_9_9") // parent $9@9 → dead
	if !top.PopupParentLive(live) {
		t.Error("claude popup parent ($1@2) should be live")
	}
	if top.PopupParentLive(dead) {
		t.Error("lazygit popup parent ($9@9) should be dead (orphan)")
	}
}
