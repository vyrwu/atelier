package state

import (
	"strings"
	"testing"
)

func TestSweepOrphanPopups_KillsOrphanAndClearsChain(t *testing.T) {
	h := newFakeHost()
	h.sessions = []string{"vyrwu/atelier", "_atelier_claude_9_9"} // parent 9_9 is dead
	h.windows = []string{"$1 @2"}                                 // only 1_2 live
	h.globals[OptOuterPane] = "%5"
	h.globals[OptOuterSession] = "$1"

	if err := SweepOrphanPopups(h); err != nil {
		t.Fatalf("SweepOrphanPopups: %v", err)
	}
	if len(h.killed) != 1 || h.killed[0] != "_atelier_claude_9_9" {
		t.Errorf("killed = %v, want [_atelier_claude_9_9]", h.killed)
	}
	// With the orphan gone and no popups left, the chain is cleared.
	if _, ok := h.globals[OptOuterPane]; ok {
		t.Error("chain should be cleared once no popups remain")
	}
	if _, ok := h.globals[OptOuterSession]; ok {
		t.Error("outer session should be cleared once no popups remain")
	}
}

func TestSweepOrphanPopups_KeepsChainWhilePopupsLive(t *testing.T) {
	h := newFakeHost()
	h.sessions = []string{"vyrwu/atelier", "_atelier_claude_1_2"} // parent live
	h.windows = []string{"$1 @2"}
	h.globals[OptOuterPane] = "%5"

	if err := SweepOrphanPopups(h); err != nil {
		t.Fatalf("SweepOrphanPopups: %v", err)
	}
	if len(h.killed) != 0 {
		t.Errorf("live popup should not be killed; killed=%v", h.killed)
	}
	if _, ok := h.globals[OptOuterPane]; !ok {
		t.Error("chain must be kept while a popup is live")
	}
}

func TestReconcile_FixRepairsOrphanAndBadOuter(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$0|default", "$1|vyrwu/atelier", "$2|_atelier_claude_9_9")
	h.windows = []string{"$1 @2", "$0 @0"} // 9_9 parent dead
	h.setClients(clientLine("/dev/ttys001", "vyrwu/atelier", "$1", "@2", "/dev/ttys001"))
	h.globals[OptOuterSession] = "$0" // outer stamped on launcher — the bug
	h.globals[OptOuterPane] = "%9"

	results, err := Reconcile(h, true)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var sawOrphan, sawLauncher bool
	for _, r := range results {
		switch r.Code {
		case VOrphanPopup:
			sawOrphan = true
			if !r.Repaired {
				t.Error("orphan popup should be repaired")
			}
		case VOuterIsLauncher:
			sawLauncher = true
			if !r.Repaired {
				t.Error("launcher outer should be repaired")
			}
		}
	}
	if !sawOrphan || !sawLauncher {
		t.Fatalf("expected orphan + launcher violations, got %+v", results)
	}
	if contains(h.killed, "_atelier_claude_9_9") == false {
		t.Errorf("orphan not killed: %v", h.killed)
	}
	if _, ok := h.globals[OptOuterSession]; ok {
		t.Error("bad outer pointer should be cleared")
	}
}

func TestReconcile_DryRunReportsWithoutRepair(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$0|default", "$1|vyrwu/atelier")
	h.windows = []string{"$1 @2", "$0 @0"}
	h.setClients(clientLine("/dev/ttys001", "vyrwu/atelier", "$1", "@2", "/dev/ttys001"))
	h.globals[OptOuterSession] = "$0"

	results, err := Reconcile(h, false)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected the launcher-outer violation to be reported")
	}
	for _, r := range results {
		if r.Repaired {
			t.Errorf("dry run must not repair: %+v", r)
		}
	}
	if _, ok := h.globals[OptOuterSession]; !ok {
		t.Error("dry run must not clear the outer pointer")
	}
}

func TestReconcile_RepairsHookArmedAtRest(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$1|vyrwu/atelier")
	h.windows = []string{"$1 @2"}
	h.setClients(clientLine("/dev/ttys001", "vyrwu/atelier", "$1", "@2", "/dev/ttys001")) // no popup client
	h.runOut["show-hooks"] = "client-detached[0] run-shell -b foo ; set-hook -ug client-detached"

	results, err := Reconcile(h, true)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var seen bool
	for _, r := range results {
		if r.Code == VHookArmedAtRest {
			seen = true
			if !r.Repaired {
				t.Error("armed hook should be repaired")
			}
		}
	}
	if !seen {
		t.Fatalf("expected VHookArmedAtRest, got %+v", results)
	}
}

func TestReconcile_RepairsDetachedOuterClient(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$1|vyrwu/atelier")
	h.windows = []string{"$1 @2"}
	h.setClients(clientLine("/dev/ttys001", "vyrwu/atelier", "$1", "@2", "/dev/ttys001"))
	h.globals[OptOuterSession] = "$1"
	h.globals[OptOuterWindow] = "@2"
	h.globals[OptOuterClient] = "/dev/GONE" // points at a detached client

	results, err := Reconcile(h, true)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var seen bool
	for _, r := range results {
		if r.Code == VOuterClientDetached {
			seen = true
			if !r.Repaired {
				t.Error("detached outer-client hint should be repaired")
			}
		}
	}
	if !seen {
		t.Fatalf("expected VOuterClientDetached, got %+v", results)
	}
	if _, ok := h.globals[OptOuterClient]; ok {
		t.Error("detached outer-client hint should be unset by repair")
	}
}

func TestReconcile_ClearsMisroutedAttentionOnPopup(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$1|vyrwu/atelier", "$7|_atelier_claude_1_2")
	// Parent $1@2 is live (popup not orphan); the popup's own window @14 wrongly
	// carries attention.
	h.runOut["list-windows"] = strings.Join([]string{
		windowLine("$1", "@2", "1", "feat/x", "slug", "", "/repo", "1", "0", "", "", "", ""),
		windowLine("$7", "@14", "1", "cd", "", "", "", "0", "1", "", "", "", ""),
	}, "\n")
	h.setClients()

	results, err := Reconcile(h, true)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var seen bool
	for _, r := range results {
		if r.Code == VMisroutedAttention {
			seen = true
			if !r.Repaired {
				t.Error("misrouted attention on a popup window should be repaired")
			}
		}
	}
	if !seen {
		t.Fatalf("expected VMisroutedAttention, got %+v", results)
	}
}

// TestReconcileLoop_RepairsSafeButSkipsRacyHook guards the C3 fix: the
// continuous heartbeat auto-repairs the loop-safe violations (here, misrouted
// popup attention) but must NOT disarm a client-detached hook — armed
// transiently by OpenOnOuter, it would race an in-flight popup open. The
// human-invoked Reconcile still fixes it (TestReconcile_RepairsHookArmedAtRest).
func TestReconcileLoop_RepairsSafeButSkipsRacyHook(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$1|vyrwu/atelier", "$7|_atelier_claude_1_2")
	h.runOut["list-windows"] = strings.Join([]string{
		windowLine("$1", "@2", "1", "feat/x", "slug", "", "/repo", "1", "0", "", "", "", ""),
		windowLine("$7", "@14", "1", "cd", "", "", "", "0", "1", "", "", "", ""), // popup window carries attention
	}, "\n")
	h.setClients() // no popup client → VHookArmedAtRest predicate holds
	h.runOut["show-hooks"] = "client-detached[0] run-shell -b foo ; set-hook -ug client-detached"

	results, err := ReconcileLoop(h)
	if err != nil {
		t.Fatalf("ReconcileLoop: %v", err)
	}
	var attn, hook bool
	for _, r := range results {
		switch r.Code {
		case VMisroutedAttention:
			attn = true
			if !r.Repaired {
				t.Error("loop must repair misrouted popup attention (loop-safe)")
			}
		case VHookArmedAtRest:
			hook = true
			if r.Repaired {
				t.Error("loop must NOT disarm client-detached — it can race an in-flight popup open")
			}
		}
	}
	if !attn || !hook {
		t.Fatalf("expected both violations surfaced; attn=%v hook=%v results=%+v", attn, hook, results)
	}
}

func TestReconcile_CleanServerNoHookFalsePositive(t *testing.T) {
	h := newFakeHost()
	h.setSessionsWithIDs("$1|vyrwu/atelier")
	h.windows = []string{"$1 @2"}
	h.setClients(clientLine("/dev/ttys001", "vyrwu/atelier", "$1", "@2", "/dev/ttys001"))
	// show-hooks -g lists the UNSET client-detached slot as a bare line.
	h.runOut["show-hooks"] = "after-select-window[0] foo\nclient-detached\nclient-focus-in\n"

	results, err := Reconcile(h, false)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for _, r := range results {
		if r.Code == VHookArmedAtRest {
			t.Fatalf("bare (unset) client-detached slot must not flag VHookArmedAtRest; got %+v", results)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
