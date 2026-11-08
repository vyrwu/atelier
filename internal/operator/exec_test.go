package operator

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestFirstLine(t *testing.T) {
	got, err := firstLine(strings.NewReader("spry-otter\n"), time.Second)
	if err != nil || got != "spry-otter" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := firstLine(strings.NewReader(""), time.Second); err == nil {
		t.Fatal("empty output accepted")
	}
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	if _, err := firstLine(r, 10*time.Millisecond); err == nil {
		t.Fatal("did not time out")
	}
}
