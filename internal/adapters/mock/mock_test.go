package mock

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
)

// tagAwareSysPrompt is the sentinel the mock keys on for the two-line
// contract: any prompt mentioning "grouping tag" (matching the kernel's
// branchNamingWithTagSysPrompt / sessionNamingWithTagSysPrompt).
const tagAwareSysPrompt = "Format: <type>/<desc> then a grouping tag"

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

// The tag-aware two-line contract: line 1 is the name derived from the
// unwrapped INTENT body (not the EXISTING TAGS preamble), line 2 echoes a
// mentioned existing tag or is empty.
func TestGenerateName_TagAware_NameFromIntentBody(t *testing.T) {
	got, _ := New().GenerateName(context.Background(), tagAwareSysPrompt,
		"EXISTING TAGS: billing, infra\nINTENT: add dark mode toggle")
	name, tag, ok := strings.Cut(got, "\n")
	if !ok {
		t.Fatalf("want two lines, got %q", got)
	}
	if name != "feat/add-dark-mode-toggle" {
		t.Errorf("name = %q, want feat/add-dark-mode-toggle (must ignore the EXISTING TAGS wrapper)", name)
	}
	if tag != "" {
		t.Errorf("tag = %q, want empty (intent mentions no existing tag)", tag)
	}
}

func TestGenerateName_TagAware_ReusesMentionedTag(t *testing.T) {
	got, _ := New().GenerateName(context.Background(), tagAwareSysPrompt,
		"EXISTING TAGS: billing, infra\nINTENT: billing webhook 500s on retry")
	_, tag, _ := strings.Cut(got, "\n")
	if tag != "billing" {
		t.Errorf("tag = %q, want billing (intent mentions it)", tag)
	}
}

func TestGenerateName_SingleLineWhenNotTagAware(t *testing.T) {
	got, _ := New().GenerateName(context.Background(), "Format: <type>/<desc>", "add dark mode")
	if strings.Contains(got, "\n") {
		t.Errorf("non-tag-aware prompt must yield one line, got %q", got)
	}
}
