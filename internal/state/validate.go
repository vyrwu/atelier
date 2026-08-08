package state

import (
	"fmt"
	"strconv"
	"strings"
)

// Severity ranks a violation by user impact.
type Severity int

const (
	SevWarn  Severity = iota // drift that self-heals or degrades gracefully
	SevError                 // will strand the user or misroute a popup
)

func (s Severity) String() string {
	if s == SevError {
		return "ERROR"
	}
	return "WARN"
}

// MarshalJSON encodes Severity as its string form ("WARN"/"ERROR") so JSON
// consumers — including the reconcile_topology debug record — never see the
// raw enum int.
func (s Severity) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(s.String())), nil
}

// ViolationCode names an invariant that failed.
type ViolationCode string

const (
	VOuterIsLauncher       ViolationCode = "outer_is_launcher"
	VOuterIsPopup          ViolationCode = "outer_is_popup"
	VOuterStale            ViolationCode = "outer_stale"
	VOuterClientDetached   ViolationCode = "outer_client_detached"
	VMultipleClientsPerTTY ViolationCode = "multiple_clients_per_tty"
	VOrphanPopup           ViolationCode = "orphan_popup"
	VHookArmedAtRest       ViolationCode = "hook_armed_at_rest"
	VMisroutedAttention    ViolationCode = "misrouted_attention"
	VDeadWorktree          ViolationCode = "dead_worktree"
)

// Violation is a single failed invariant over a Topology.
type Violation struct {
	Code     ViolationCode
	Severity Severity
	Subject  string // the offending entity: session/client/tty name or sid_wid
	Detail   string // human-readable, for `state show` / `reconcile` output
	Fixable  bool   // whether Reconcile(fix=true) can repair it
}

// Validate checks every invariant over a Topology and returns the failures.
// Pure — no tmux, fully unit-testable.
func Validate(t *Topology) []Violation {
	var vs []Violation

	// --- outer pointer -------------------------------------------------
	if t.OuterPtr.Session != "" {
		if s, ok := t.SessionByID(t.OuterPtr.Session); !ok {
			vs = append(vs, Violation{VOuterStale, SevError, t.OuterPtr.Session,
				"outer pointer references a session that no longer exists", true})
		} else {
			switch s.Kind {
			case KindLauncher:
				vs = append(vs, Violation{VOuterIsLauncher, SevError, s.Name,
					"outer pointer stamped on the launcher — every workspace switch lands in the default shell", true})
			case KindPopup:
				vs = append(vs, Violation{VOuterIsPopup, SevError, s.Name,
					"outer pointer stamped on a popup session", true})
			}
			if t.OuterPtr.Window != "" && !t.HasWindow(t.OuterPtr.Window) {
				vs = append(vs, Violation{VOuterStale, SevError, t.OuterPtr.Window,
					"outer pointer references a window that no longer exists", true})
			}
		}
	}
	if t.OuterPtr.Client != "" && !t.hasClient(t.OuterPtr.Client) {
		vs = append(vs, Violation{VOuterClientDetached, SevWarn, t.OuterPtr.Client,
			"outer-client hint points at a detached client", true})
	}

	// --- one workspace client per tty ----------------------------------
	// Only workspace clients matter: two terminals on the same workspace tty
	// make the view snap between them. Popup clients live on their own popup
	// ptys, so counting them here would false-positive on every open popup.
	byTTY := map[string]int{}
	for _, c := range t.Clients {
		if c.Kind == ClientWorkspace && c.TTY != "" {
			byTTY[c.TTY]++
		}
	}
	for tty, n := range byTTY {
		if n > 1 {
			vs = append(vs, Violation{VMultipleClientsPerTTY, SevWarn, tty,
				fmt.Sprintf("%d workspace clients attached to one tty — the view snaps between them", n), false})
		}
	}

	// --- orphan popups -------------------------------------------------
	for _, s := range t.Sessions {
		if s.Kind == KindPopup && s.Popup.Form != FormNone && !t.PopupParentLive(s.Popup) {
			vs = append(vs, Violation{VOrphanPopup, SevError, s.Name,
				"popup-backing session whose parent window is gone", true})
		}
	}

	// --- capability: attention / worktree ------------------------------
	// These are kernel-owned capability invariants (the attention slot, the
	// worktree). Report-only: clearing a stray flag or killing a window is
	// destructive enough to leave to a human, and a non-listable window that
	// legitimately lost metadata could still want attention.
	kindByID := make(map[string]SessionKind, len(t.Sessions))
	for _, s := range t.Sessions {
		kindByID[s.ID] = s.Kind
	}
	for _, w := range t.Windows {
		if w.Attention {
			switch {
			case kindByID[w.SessionID] == KindPopup:
				// Fixable: a popup-backing window must never carry attention
				// (the parent workspace does), and the rollup already ignores
				// popup windows — so clearing this stray flag can't hide a
				// notification. --fix unsets it.
				vs = append(vs, Violation{VMisroutedAttention, SevWarn, w.WindowID,
					"attention flag stamped on a popup-backing window — the parent workspace should carry it (phantom-notification bug)", true})
			case kindByID[w.SessionID] == KindWorkspace && !Listable(w.RepoPath, w.WorkspaceKind):
				// Report-only: a non-listable workspace window could be a real
				// workspace that lost its metadata and still wants attention —
				// clearing it might drop a genuine signal, so leave it to a human.
				vs = append(vs, Violation{VMisroutedAttention, SevWarn, w.WindowID,
					"attention flag on a non-listable window (no @repo_path/@ai_workspace_kind) — a phantom the rollup ignores", false})
			}
		}
		if kindByID[w.SessionID] == KindWorkspace && w.RepoPath != "" && w.PaneCwd != "" && !w.PaneCwdLive {
			vs = append(vs, Violation{VDeadWorktree, SevWarn, w.WindowID,
				"workspace window whose working directory no longer exists (worktree may have been removed)", false})
		}
	}

	// --- client-moving hook armed at rest ------------------------------
	// A client-detached hook is only ever set transiently (OpenOnOuter /
	// ReturnToOuterShell) and self-clears. If one is armed with no popup
	// clients attached, it leaked and will re-fire on the next unrelated
	// detach, yanking the user to a baked-in window.
	//
	// `show-hooks -g` lists EVERY hook name, printing the unset ones as a
	// bare `client-detached` line and armed ones as `client-detached[0] …`.
	// Match the indexed form so the empty slot doesn't false-positive on
	// every server.
	if strings.Contains(t.GlobalHooks, "client-detached[") && len(t.InnerClients()) == 0 {
		vs = append(vs, Violation{VHookArmedAtRest, SevError, "client-detached",
			"a client-detached hook is armed at rest — it will fire on the next unrelated detach", true})
	}

	return vs
}

func (t *Topology) hasClient(name string) bool {
	for _, c := range t.Clients {
		if c.Name == name {
			return true
		}
	}
	return false
}
