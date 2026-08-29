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
			{SessionID: "$2", WindowID: "@5", Attention: true},                                     // on a popup window → misrouted
			{SessionID: "$1", WindowID: "@9", Attention: true},                                     // workspace but non-listable (no @workspace_id) → phantom
			{SessionID: "$1", WindowID: "@2", Attention: true, WorkspaceID: "slug"},                // listable + attention → legit, no violation
			{SessionID: "$1", WindowID: "@3", Attention: true, WorkspaceID: "other", Driver: true}, // listable driver + attention → legit
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
			{SessionID: "$1", WindowID: "@2", PaneCwd: "/gone", PaneCwdLive: false}, // dead → violation
			{SessionID: "$1", WindowID: "@3", PaneCwd: "/live", PaneCwdLive: true},  // live → ok
			{SessionID: "$1", WindowID: "@4", PaneCwd: "", PaneCwdLive: true},       // empty cwd → skip
			{SessionID: "$2", WindowID: "@5", PaneCwd: "/gone", PaneCwdLive: false}, // popup session → skip
		},
	}
	if n := countCode(Validate(top), VDeadWorktree); n != 1 {
		t.Fatalf("want 1 dead-worktree, got %d: %+v", n, Validate(top))
	}
}

func TestValidate_WorkspaceRootMissing(t *testing.T) {
	live := t.TempDir() // exists on disk
	top := &Topology{
		Sessions: []Session{
			{ID: "$1", Name: "vyrwu/atelier", Kind: KindWorkspace},
			{ID: "$2", Name: "wawa/infra", Kind: KindWorkspace},
			{ID: "$3", Name: "_atelier_claude_1_2", Kind: KindPopup},
		},
		Windows: []Window{
			// driver whose root dir is gone → report-only violation
			{SessionID: "$1", WindowID: "@2", WorkspaceID: "slug1", Driver: true, Root: "/no/such/atelier/root/xyzzy"},
			// driver whose root exists → ok
			{SessionID: "$2", WindowID: "@3", WorkspaceID: "slug2", Driver: true, Root: live},
			// non-driver window with a missing root → skipped (only driver windows checked)
			{SessionID: "$1", WindowID: "@4", WorkspaceID: "slug1", Driver: false, Root: "/also/gone"},
			// popup driver-looking window → skipped (not a workspace session)
			{SessionID: "$3", WindowID: "@5", WorkspaceID: "slug3", Driver: true, Root: "/popup/gone"},
		},
	}
	if n := countCode(Validate(top), VWorkspaceRootMissing); n != 1 {
		t.Fatalf("want 1 workspace-root-missing, got %d: %+v", n, Validate(top))
	}
	// It is report-only, not fixable.
	for _, v := range Validate(top) {
		if v.Code == VWorkspaceRootMissing && v.Fixable {
			t.Error("workspace-root-missing must be report-only (not fixable)")
		}
	}
}

func TestValidate_MultipleDrivers(t *testing.T) {
	top := &Topology{
		Sessions: []Session{
			{ID: "$1", Name: "vyrwu/atelier", Kind: KindWorkspace},
			{ID: "$2", Name: "wawa/infra", Kind: KindWorkspace},
		},
		Windows: []Window{
			// $1 has two driver windows → violation (a workspace has one agent)
			{SessionID: "$1", WindowID: "@2", WorkspaceID: "slug1", Driver: true},
			{SessionID: "$1", WindowID: "@3", WorkspaceID: "slug1", Driver: true},
			// a non-driver inspection shell in the same session does NOT count
			{SessionID: "$1", WindowID: "@4", WorkspaceID: "slug1", Driver: false},
			// $2 has a single driver → ok
			{SessionID: "$2", WindowID: "@5", WorkspaceID: "slug2", Driver: true},
		},
	}
	if n := countCode(Validate(top), VMultipleDrivers); n != 1 {
		t.Fatalf("want 1 multiple-drivers, got %d: %+v", n, Validate(top))
	}
	// Report-only: which extra driver to demote is a human judgment call.
	for _, v := range Validate(top) {
		if v.Code == VMultipleDrivers {
			if v.Fixable {
				t.Error("multiple-drivers must be report-only (not fixable)")
			}
			if v.Subject != "$1" {
				t.Errorf("multiple-drivers subject = %q, want the offending session $1", v.Subject)
			}
		}
	}
}

func TestValidate_SingleDriverNoViolation(t *testing.T) {
	top := &Topology{
		Sessions: []Session{{ID: "$1", Name: "vyrwu/atelier", Kind: KindWorkspace}},
		Windows: []Window{
			{SessionID: "$1", WindowID: "@2", WorkspaceID: "slug", Driver: true},
			{SessionID: "$1", WindowID: "@3", WorkspaceID: "slug", Driver: false}, // inspection shell
		},
	}
	if n := countCode(Validate(top), VMultipleDrivers); n != 0 {
		t.Fatalf("single-driver workspace must not flag multiple-drivers, got %d: %+v", n, Validate(top))
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
