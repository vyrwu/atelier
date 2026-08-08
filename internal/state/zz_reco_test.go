package state

import (
	"fmt"
	"testing"
)

func TestZZReconcileConvergence(t *testing.T) {
	// Build a topology with: outer ptr on launcher, a detached outer client hint,
	// an orphan popup, a hook armed at rest.
	top := &Topology{
		Sessions: []Session{
			{ID: "$0", Name: "default", Kind: KindLauncher},
			{ID: "$1", Name: "work", Kind: KindWorkspace},
			{ID: "$2", Name: "_atelier_claude_9_9", Kind: KindPopup, Popup: PopupInfo{Form: FormAtelier, Tool: "claude", SidDigit: "9", WidDigit: "9"}},
		},
		Windows:     []Window{{SessionID: "$1", WindowID: "@1"}},
		LiveSidWid:  map[string]bool{"1_1": true},
		OuterPtr:    Outer{Session: "$0", Window: "@1", Client: "/dev/ttys999"},
		GlobalHooks: "client-detached[0] run-shell foo\n",
	}
	vs := Validate(top)
	fmt.Printf("run1 violations=%d\n", len(vs))
	for _, v := range vs {
		fmt.Printf("  %s fixable=%v sev=%s subj=%s\n", v.Code, v.Fixable, v.Severity, v.Subject)
	}
}
