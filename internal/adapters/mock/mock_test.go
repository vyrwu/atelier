package mock

import (
	"context"
	"regexp"
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
)

func TestAdapter_SatisfiesPort(t *testing.T) {
	var _ integration.AIIntegration = New()
	if New().Name() != "mock" {
		t.Errorf("Name = %q, want mock", New().Name())
	}
}

// These mirror the kernel's naming-validation regexes (workspaces
// conventionalBranchRe / autoSessionNameRe). The mock's GenerateName must
// satisfy them so auto-mode works with `[integrations] ai = "mock"` — the
// proof that the AI port is genuinely swappable.
var (
	branchRe  = regexp.MustCompile(`^(feat|fix|chore|refactor|docs|test|perf|style)/[a-z0-9-]+$`)
	sessionRe = regexp.MustCompile(`^auto/[a-z0-9-]+$`)
)

func TestGenerateName_BranchFormat(t *testing.T) {
	got, err := New().GenerateName(context.Background(), "Format: <type>/<desc>", "Add dark mode toggle!!!")
	if err != nil {
		t.Fatal(err)
	}
	if !branchRe.MatchString(got) {
		t.Errorf("GenerateName = %q, does not match conventional branch regex", got)
	}
}

func TestGenerateName_SessionFormat(t *testing.T) {
	// The session naming prompt specifies the `auto/` format.
	got, err := New().GenerateName(context.Background(), "Format: auto/<short-desc>", "wire up the multi repo thing")
	if err != nil {
		t.Fatal(err)
	}
	if !sessionRe.MatchString(got) {
		t.Errorf("GenerateName = %q, does not match auto-session regex", got)
	}
}

func TestGenerateName_Deterministic(t *testing.T) {
	a, _ := New().GenerateName(context.Background(), "x", "same intent")
	b, _ := New().GenerateName(context.Background(), "x", "same intent")
	if a != b {
		t.Errorf("GenerateName not deterministic: %q vs %q", a, b)
	}
}

func TestGenerateName_EmptyIntentFallsBack(t *testing.T) {
	got, _ := New().GenerateName(context.Background(), "x", "!!! ??? ")
	if !branchRe.MatchString(got) {
		t.Errorf("empty-ish intent should still yield a valid name, got %q", got)
	}
}
