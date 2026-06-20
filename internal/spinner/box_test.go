package spinner

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBoxSpinner_FallsBackToInlineWhenWriterIsNotATTY(t *testing.T) {
	// bytes.Buffer isn't a *os.File, so terminalSize returns ok=false and
	// BoxSpinner falls back to the inline Spinner.
	buf := &bytes.Buffer{}
	s := &BoxSpinner{
		Message:  "thinking",
		Writer:   buf,
		Frames:   DefaultFrames,
		Interval: time.Millisecond,
	}
	if err := s.Run(func() error { time.Sleep(5 * time.Millisecond); return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "thinking") {
		t.Fatalf("expected inline fallback to render the message, got %q", buf.String())
	}
}

func TestBoxSpinner_PropagatesError(t *testing.T) {
	buf := &bytes.Buffer{}
	want := errors.New("nope")
	s := &BoxSpinner{Writer: buf, Frames: DefaultFrames, Interval: time.Millisecond, Message: "x"}
	if got := s.Run(func() error { return want }); got != want {
		t.Fatalf("Run: got %v want %v", got, want)
	}
}

func TestNewBox_Defaults(t *testing.T) {
	s := NewBox("test")
	if s.Writer == nil {
		t.Fatalf("default writer should be non-nil")
	}
	if len(s.Frames) == 0 {
		t.Fatalf("default frames should be non-empty")
	}
}

// TestFormatStageLabel locks in the FR-2.1 elapsed-seconds rule:
// stages running longer than 10s get a "(Xs)" suffix so the user can
// tell stuck from slow.
func TestFormatStageLabel(t *testing.T) {
	cases := []struct {
		name    string
		label   string
		elapsed time.Duration
		want    string
	}{
		{"below threshold, fresh", "Asking Claude...", 1 * time.Second, "Asking Claude..."},
		{"below threshold, near boundary", "Asking Claude...", 9*time.Second + 999*time.Millisecond, "Asking Claude..."},
		{"at threshold", "Asking Claude...", 10 * time.Second, "Asking Claude (10s)..."},
		{"above threshold", "Asking Claude...", 12 * time.Second, "Asking Claude (12s)..."},
		{"above threshold, no ellipsis on input", "Building worktree", 15 * time.Second, "Building worktree (15s)..."},
		{"above threshold, trailing periods stripped", "Fetching origin/main.....", 11 * time.Second, "Fetching origin/main (11s)..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatStageLabel(tc.label, tc.elapsed); got != tc.want {
				t.Errorf("formatStageLabel(%q, %v) = %q, want %q", tc.label, tc.elapsed, got, tc.want)
			}
		})
	}
}

// TestSetStatus_UpdatesCurrentLabel locks in the SetStatus → currentLabel
// path: a stage update is reflected in the next rendered label, and
// resets the elapsed timer so the fresh stage doesn't inherit the prior
// stage's "(Xs)" suffix.
func TestSetStatus_UpdatesCurrentLabel(t *testing.T) {
	s := &BoxSpinner{Message: "Building workspace..."}
	// Pretend Run already started — manually init state.
	s.stageStart = time.Now()
	s.status = s.Message

	if got := s.currentLabel(); got != "Building workspace..." {
		t.Errorf("initial currentLabel = %q, want %q", got, "Building workspace...")
	}

	s.SetStatus("Fetching origin/main...")
	if got := s.currentLabel(); got != "Fetching origin/main..." {
		t.Errorf("after SetStatus, currentLabel = %q, want %q", got, "Fetching origin/main...")
	}

	// Force the stage to look like it's been running long enough.
	s.mu.Lock()
	s.stageStart = time.Now().Add(-12 * time.Second)
	s.mu.Unlock()
	if got := s.currentLabel(); got != "Fetching origin/main (12s)..." {
		t.Errorf("aged stage, currentLabel = %q, want %q", got, "Fetching origin/main (12s)...")
	}

	// Next SetStatus must reset the elapsed timer.
	s.SetStatus("Building worktree...")
	if got := s.currentLabel(); got != "Building worktree..." {
		t.Errorf("after SetStatus reset, currentLabel = %q, want %q", got, "Building worktree...")
	}
}
