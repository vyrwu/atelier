package state

import (
	"fmt"
	"testing"
)

// fake host to exercise SweepOrphanPopups
type advHost struct {
	sessions [][]string // successive ListSessions results
	killed   []string
	unset    []string
	calls    int
	windows  []string
}

func (a *advHost) ListSessions() ([]string, error) {
	r := a.sessions[a.calls]
	if a.calls < len(a.sessions)-1 {
		a.calls++
	}
	return r, nil
}
func (a *advHost) ListWindows() ([]string, error)               { return a.windows, nil }
func (a *advHost) ShowGlobalOption(string) (string, error)      { return "", nil }
func (a *advHost) SetGlobalOption(string, string) error         { return nil }
func (a *advHost) UnsetGlobalOption(n string) error             { a.unset = append(a.unset, n); return nil }
func (a *advHost) DisplayMessageAt(t, f string) (string, error) { return "", nil }
func (a *advHost) KillSession(n string) error                   { a.killed = append(a.killed, n); return nil }
func (a *advHost) Run(args ...string) ([]byte, error)           { return nil, nil }

func TestZZSweepSessionGlobal(t *testing.T) {
	// A session-global _atelier_k8s popup (no parent) alongside a live workspace.
	// Parent window @1 of session $1 is live. No orphaned parented popups.
	h := &advHost{
		sessions: [][]string{{"work", "_atelier_k8s"}, {"work", "_atelier_k8s"}},
		windows:  []string{"$1 @1"},
	}
	err := SweepOrphanPopups(h)
	fmt.Printf("session-global: killed=%v unset=%v err=%v\n", h.killed, h.unset, err)
}

func TestZZSweepEmptyDigits(t *testing.T) {
	// _atelier___ parses ok with sid="" wid="". live map has "1_1".
	h := &advHost{
		sessions: [][]string{{"work", "_atelier___"}, {"work", "_atelier___"}},
		windows:  []string{"$1 @1"},
	}
	err := SweepOrphanPopups(h)
	fmt.Printf("empty-digits: killed=%v unset=%v err=%v\n", h.killed, h.unset, err)
}

func TestZZSweepChainKeptAliveForever(t *testing.T) {
	// Only a session-global _atelier_k8s remains (parent window gone means nothing
	// since it has no parent). Does the chain get cleared? Workspace still live.
	h := &advHost{
		sessions: [][]string{{"work", "_atelier_k8s"}, {"work", "_atelier_k8s"}},
		windows:  []string{"$1 @1"},
	}
	_ = SweepOrphanPopups(h)
	fmt.Printf("chain-clear called (unset)? %v\n", h.unset)
}
