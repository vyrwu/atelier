package spinner

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestMain forces the headless path for the whole package: the spinner UI is a
// bubbletea program on the controlling tty (integration-tested by eye, like
// textprompt.Read); the unit tests exercise Run's task/error semantics and the
// pure model logic, so they must never actually render.
func TestMain(m *testing.M) {
	ttyForRender = func() (*os.File, bool) { return nil, false }
	os.Exit(m.Run())
}

func TestBoxSpinner_PropagatesError(t *testing.T) {
	want := os.ErrPermission
	if got := NewBox("x").Run(func() error { return want }); got != want {
		t.Fatalf("Run error = %v, want %v", got, want)
	}
}

func TestBoxSpinner_ReturnsNilOnSuccess(t *testing.T) {
	ran := false
	if err := NewBox("x").Run(func() error { ran = true; return nil }); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if !ran {
		t.Fatal("Run must execute the task fn")
	}
}

// Delay must not change task/error semantics on the headless path.
func TestBoxSpinner_DelayDoesNotBreakErrorPropagation(t *testing.T) {
	want := os.ErrPermission
	s := &BoxSpinner{Message: "x", Delay: 50 * time.Millisecond}
	if got := s.Run(func() error { return want }); got != want {
		t.Fatalf("Run error = %v, want %v", got, want)
	}
}

func TestBoxSpinner_SetStatusStoresLabel(t *testing.T) {
	s := NewBox("initial")
	s.SetStatus("Fetching origin/main...")
	s.mu.Lock()
	got := s.status
	s.mu.Unlock()
	if got != "Fetching origin/main..." {
		t.Errorf("after SetStatus, status = %q, want %q", got, "Fetching origin/main...")
	}
}

func TestFormatStageLabel(t *testing.T) {
	cases := []struct {
		name    string
		label   string
		elapsed time.Duration
		want    string
	}{
		{"under threshold → verbatim", "Naming...", 3 * time.Second, "Naming..."},
		{"over threshold → elapsed suffix", "Asking Claude...", 12 * time.Second, "Asking Claude (12s)..."},
		{"trailing dots trimmed before suffix", "Building....", 15 * time.Second, "Building (15s)..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatStageLabel(tc.label, tc.elapsed); got != tc.want {
				t.Errorf("formatStageLabel(%q, %v) = %q, want %q", tc.label, tc.elapsed, got, tc.want)
			}
		})
	}
}

// --- model ---

func TestSpinModel_StatusMsgUpdatesLabel(t *testing.T) {
	m := newSpinModel("first", time.Now())
	next, _ := m.Update(statusMsg{label: "second", at: time.Now()})
	m = next.(spinModel)
	if m.label != "second" {
		t.Fatalf("label = %q, want second", m.label)
	}
	m.width, m.height = 40, 10
	if !strings.Contains(m.View(), "second") {
		t.Errorf("View should render the current label; got:\n%s", m.View())
	}
}

func TestSpinModel_DoneQuits(t *testing.T) {
	_, cmd := newSpinModel("x", time.Now()).Update(doneMsg{})
	if cmd == nil {
		t.Fatal("doneMsg should return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("doneMsg should quit; got %T", cmd())
	}
}

func TestSpinModel_ViewShowsElapsedSuffixWhenSlow(t *testing.T) {
	m := newSpinModel("Asking Claude...", time.Now().Add(-12*time.Second))
	m.width, m.height = 40, 10
	if !strings.Contains(m.View(), "(12s)") {
		t.Errorf("a long-running stage should show an elapsed suffix; got:\n%s", m.View())
	}
}
