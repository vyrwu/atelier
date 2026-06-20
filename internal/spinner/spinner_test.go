package spinner

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRun_PropagatesError(t *testing.T) {
	s := &Spinner{Writer: &bytes.Buffer{}, Frames: DefaultFrames, Interval: time.Millisecond, Message: "x"}
	want := errors.New("boom")
	got := s.Run(func() error { return want })
	if got != want {
		t.Fatalf("Run: got %v want %v", got, want)
	}
}

func TestRun_PropagatesNilOnSuccess(t *testing.T) {
	s := &Spinner{Writer: &bytes.Buffer{}, Frames: DefaultFrames, Interval: time.Millisecond, Message: "x"}
	if err := s.Run(func() error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRun_RendersSpinnerFrames(t *testing.T) {
	buf := &bytes.Buffer{}
	s := &Spinner{Writer: buf, Frames: DefaultFrames, Interval: time.Millisecond, Message: "thinking"}
	_ = s.Run(func() error {
		time.Sleep(15 * time.Millisecond)
		return nil
	})
	if !strings.Contains(buf.String(), "thinking") {
		t.Fatalf("spinner output missing message: %q", buf.String())
	}
	// Confirm we wrote at least one braille glyph.
	hasFrame := false
	for _, f := range DefaultFrames {
		if strings.Contains(buf.String(), f) {
			hasFrame = true
			break
		}
	}
	if !hasFrame {
		t.Fatalf("spinner never rendered a frame: %q", buf.String())
	}
}

func TestNew_DefaultsToStderr(t *testing.T) {
	s := New("test")
	if s.Writer == nil {
		t.Fatalf("default writer should be non-nil")
	}
}
