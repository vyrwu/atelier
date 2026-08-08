package state

import "testing"

func hasCode(vs []Violation, c ViolationCode) bool {
	for _, v := range vs {
		if v.Code == c {
			return true
		}
	}
	return false
}

func TestValidate_Clean(t *testing.T) {
	top := &Topology{
		Sessions:   []Session{{ID: "$1", Name: "ws", Kind: KindWorkspace}},
		Windows:    []Window{{SessionID: "$1", WindowID: "@2"}},
		Clients:    []ClientRef{{Name: "/dev/ttys0", Session: "ws", SessionID: "$1", WindowID: "@2", TTY: "/dev/ttys0", Kind: ClientWorkspace}},
		OuterPtr:   Outer{Session: "$1", Window: "@2", Client: "/dev/ttys0"},
		LiveSidWid: map[string]bool{"1_2": true},
	}
	if vs := Validate(top); len(vs) != 0 {
		t.Fatalf("clean topology has violations: %+v", vs)
	}
}

func TestValidate_OuterIsLauncher(t *testing.T) {
	top := &Topology{
		Sessions: []Session{{ID: "$0", Name: "default", Kind: KindLauncher}, {ID: "$1", Name: "ws", Kind: KindWorkspace}},
		OuterPtr: Outer{Session: "$0"},
	}
	if !hasCode(Validate(top), VOuterIsLauncher) {
		t.Error("expected VOuterIsLauncher")
	}
}

func TestValidate_OuterStaleAndPopup(t *testing.T) {
	stale := &Topology{OuterPtr: Outer{Session: "$9"}}
	if !hasCode(Validate(stale), VOuterStale) {
		t.Error("expected VOuterStale for unknown session id")
	}
	popup := &Topology{
		Sessions: []Session{{ID: "$2", Name: "_atelier_claude_1_2", Kind: KindPopup}},
		OuterPtr: Outer{Session: "$2"},
	}
	if !hasCode(Validate(popup), VOuterIsPopup) {
		t.Error("expected VOuterIsPopup")
	}
}

func TestValidate_OuterClientDetached(t *testing.T) {
	top := &Topology{
		Sessions: []Session{{ID: "$1", Name: "ws", Kind: KindWorkspace}},
		OuterPtr: Outer{Session: "$1", Client: "/dev/gone"},
	}
	if !hasCode(Validate(top), VOuterClientDetached) {
		t.Error("expected VOuterClientDetached")
	}
}

func TestValidate_MultipleClientsPerTTY(t *testing.T) {
	top := &Topology{Clients: []ClientRef{
		{Name: "a", TTY: "/dev/ttys0"},
		{Name: "b", TTY: "/dev/ttys0"},
	}}
	if !hasCode(Validate(top), VMultipleClientsPerTTY) {
		t.Error("expected VMultipleClientsPerTTY")
	}
}

func TestValidate_OrphanPopup(t *testing.T) {
	claude, _ := ParsePopup("_atelier_claude_9_9")
	top := &Topology{
		Sessions:   []Session{{ID: "$2", Name: "_atelier_claude_9_9", Kind: KindPopup, Popup: claude}},
		LiveSidWid: map[string]bool{"1_2": true}, // parent 9_9 not live
	}
	if !hasCode(Validate(top), VOrphanPopup) {
		t.Error("expected VOrphanPopup")
	}
}

func countCode(vs []Violation, c ViolationCode) int {
	n := 0
	for _, v := range vs {
		if v.Code == c {
			n++
		}
	}
	return n
}

func TestValidate_MisroutedAttention(t *testing.T) {
	top := &Topology{
		Sessions: []Session{
			{ID: "$1", Name: "vyrwu/atelier", Kind: KindWorkspace},
			{ID: "$2", Name: "_atelier_claude_1_2", Kind: KindPopup},
		},
		Windows: []Window{
			{SessionID: "$2", WindowID: "@5", Attention: true},                              // on a popup window → misrouted
			{SessionID: "$1", WindowID: "@9", Attention: true},                              // workspace but non-listable → phantom
			{SessionID: "$1", WindowID: "@2", Attention: true, RepoPath: "/r"},              // listable + attention → legit, no violation
			{SessionID: "$1", WindowID: "@3", WorkspaceKind: "multi-repo", Attention: true}, // listable via kind → legit
		},
	}
	if n := countCode(Validate(top), VMisroutedAttention); n != 2 {
		t.Fatalf("want 2 misrouted-attention, got %d: %+v", n, Validate(top))
	}
}

func TestValidate_DeadWorktree(t *testing.T) {
	top := &Topology{
		Sessions: []Session{
			{ID: "$1", Name: "vyrwu/atelier", Kind: KindWorkspace},
			{ID: "$2", Name: "_atelier_claude_1_2", Kind: KindPopup},
		},
		Windows: []Window{
			{SessionID: "$1", WindowID: "@2", RepoPath: "/r", PaneCwd: "/gone", PaneCwdLive: false}, // dead → violation
			{SessionID: "$1", WindowID: "@3", RepoPath: "/r", PaneCwd: "/live", PaneCwdLive: true},  // live → ok
			{SessionID: "$1", WindowID: "@4", RepoPath: "", PaneCwd: "/gone", PaneCwdLive: false},   // no repo (multi-repo/raw) → skip
			{SessionID: "$2", WindowID: "@5", RepoPath: "/r", PaneCwd: "/gone", PaneCwdLive: false}, // popup session → skip
		},
	}
	if n := countCode(Validate(top), VDeadWorktree); n != 1 {
		t.Fatalf("want 1 dead-worktree, got %d: %+v", n, Validate(top))
	}
}

func TestValidate_HookArmedAtRest(t *testing.T) {
	// `show-hooks -g` prints an armed hook with an index; the unset slot is
	// a bare name. Only the indexed (armed) form is a violation.
	armed := &Topology{GlobalHooks: `client-detached[0] run-shell -b "…" ; set-hook -ug client-detached`}
	if !hasCode(Validate(armed), VHookArmedAtRest) {
		t.Error("expected VHookArmedAtRest for an armed (indexed) hook")
	}
	// The bare, unset slot that show-hooks lists on every server is NOT armed.
	bare := &Topology{GlobalHooks: "after-select-window[0] x\nclient-detached\nclient-focus-in\n"}
	if hasCode(Validate(bare), VHookArmedAtRest) {
		t.Error("bare (unset) client-detached slot must not be flagged")
	}
	// Not flagged mid-flight, while a popup client is still attached.
	midflight := &Topology{
		GlobalHooks: "client-detached[0] foo",
		Clients:     []ClientRef{{Name: "p", Kind: ClientPopup}},
	}
	if hasCode(Validate(midflight), VHookArmedAtRest) {
		t.Error("hook must not be flagged while a popup client is attached")
	}
}
