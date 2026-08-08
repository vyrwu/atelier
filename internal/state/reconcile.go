package state

import (
	"encoding/json"
	"strings"

	"github.com/vyrwu/atelier/internal/debuglog"
)

// SweepOrphanPopups kills popup-backing sessions whose parent window is gone,
// then clears the outer chain if no popups remain. Idempotent and hook-safe:
// it is the body behind the window-unlinked / session-closed hooks and the
// `atelier popup cleanup` CLI, so it stays LEAN — sessions + windows only, no
// client/hook sweep on this hot path.
func SweepOrphanPopups(h Host) error {
	sessions, err := h.ListSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return nil
	}

	windows, err := h.ListWindows()
	if err != nil {
		return err
	}
	live := make(map[string]bool, len(windows))
	for _, line := range windows {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			live[Digits(fields[0])+"_"+Digits(fields[1])] = true
		}
	}

	for _, name := range sessions {
		info, ok := ParsePopup(name)
		if !ok {
			continue
		}
		if !live[info.SidDigit+"_"+info.WidDigit] {
			_ = h.KillSession(name)
		}
	}

	remaining, _ := h.ListSessions()
	for _, n := range remaining {
		if IsPopupSession(n) {
			return nil // popups still live; keep the chain
		}
	}
	return ClearChain(h)
}

// ReconcileResult is a validated violation plus whether Reconcile repaired it.
type ReconcileResult struct {
	Violation
	Repaired bool
}

// Reconcile captures the full topology, validates it, and (when fix is set)
// repairs every fixable violation. It operates purely on live tmux runtime
// state — it does NOT touch the persisted statestore cache (that is
// workspace.SyncCache's separate job). Returns one result per violation.
//
// This is the on-demand recovery path (`atelier reconcile --fix`), invoked by
// a human who knows no popup is mid-open. A continuous background loop must use
// ReconcileLoop instead — see loopSafeFix.
func Reconcile(h Host, fix bool) ([]ReconcileResult, error) {
	return reconcileWith(h, func(v Violation) bool { return fix && v.Fixable })
}

// loopSafeFix is the subset of fixable violations a CONTINUOUS background loop
// may auto-repair: those whose repair can't corrupt a legitimate, in-flight
// popup/client operation on a bare-snapshot false positive.
//
// Deliberately EXCLUDED (repair only from the human-invoked Reconcile):
//   - VHookArmedAtRest — a `client-detached` hook is armed transiently by
//     OpenOnOuter between detaching the inner popup clients and the deferred
//     open firing. In that sub-second window InnerClients()==0 AND the hook is
//     armed — exactly VHookArmedAtRest's predicate. A 45s heartbeat that lands
//     there would disarm the hook before it fires, and the user's popup would
//     silently never open.
//   - VOuterClientDetached — clears the @atelier_outer_client hint; losing it
//     in the sub-second window while a popup opens could misroute it to the
//     wrong terminal (the exact bug the hint exists to prevent).
//
// The included repairs are idempotent and don't depend on "no popup mid-open":
// kill an orphan popup, clear a stray popup attention flag, drop an outer
// pointer that references a dead/launcher/popup session (re-derived on the next
// M-; / LandOuter).
var loopSafeFix = map[ViolationCode]bool{
	VOrphanPopup:        true,
	VMisroutedAttention: true,
	VOuterIsLauncher:    true,
	VOuterIsPopup:       true,
	VOuterStale:         true,
}

// ReconcileLoop is Reconcile for the continuous background heartbeat: it
// repairs only the loop-safe subset (loopSafeFix). Report-only and racy-repair
// violations are still surfaced in the results (Repaired=false) so callers can
// log them, but are never auto-fixed on a timer.
func ReconcileLoop(h Host) ([]ReconcileResult, error) {
	return reconcileWith(h, func(v Violation) bool { return v.Fixable && loopSafeFix[v.Code] })
}

// reconcileWith is the shared core: capture, validate, log, and repair every
// violation for which shouldFix reports true.
func reconcileWith(h Host, shouldFix func(Violation) bool) ([]ReconcileResult, error) {
	top, err := CaptureTopology(h)
	if err != nil {
		return nil, err
	}
	violations := Validate(top)
	logTopologyRecord(top, violations)
	results := make([]ReconcileResult, 0, len(violations))
	for _, v := range violations {
		r := ReconcileResult{Violation: v}
		if shouldFix(v) && repair(h, top, v) == nil {
			r.Repaired = true
		}
		results = append(results, r)
	}
	return results, nil
}

// logTopologyRecord appends a compact, one-line JSON record of the captured
// topology (counts + outer pointer) and its violations to the debug log — the
// cheap, diff-able transition trace for post-hoc "what did state look like and
// what was wrong" analysis. GlobalHooks (multi-line) is deliberately omitted
// so the record stays one line. Best-effort; debuglog never errors.
func logTopologyRecord(t *Topology, violations []Violation) {
	rec := struct {
		Sessions   int         `json:"sessions"`
		Windows    int         `json:"windows"`
		Clients    int         `json:"clients"`
		Outer      Outer       `json:"outer"`
		Violations []Violation `json:"violations"`
	}{len(t.Sessions), len(t.Windows), len(t.Clients), t.OuterPtr, violations}
	if b, err := json.Marshal(rec); err == nil {
		debuglog.Logf("reconcile_topology %s", b)
	}
}

// repair applies the fix for one fixable violation.
func repair(h Host, top *Topology, v Violation) error {
	switch v.Code {
	case VOrphanPopup:
		return h.KillSession(v.Subject)
	case VOuterIsLauncher, VOuterIsPopup, VOuterStale:
		// Drop the bad pointer; the next M-; / LandOuter re-derives it, and
		// Capture already falls back to the current pane when it is empty.
		return ClearChain(h)
	case VMisroutedAttention:
		// Only the popup sub-case is Fixable (see Validate): clear the stray
		// @needs_attention off the popup-backing window. Literal option name —
		// state can't import workspace (which owns OptAttention) without a cycle.
		_, err := h.Run("set-window-option", "-t", v.Subject, "-u", "@needs_attention")
		return err
	case VOuterClientDetached:
		return h.UnsetGlobalOption(OptOuterClient)
	case VHookArmedAtRest:
		_, err := h.Run("set-hook", "-ug", "client-detached")
		return err
	default:
		return nil
	}
}
