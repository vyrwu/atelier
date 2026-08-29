package mock

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
)

func TestAdapter_SatisfiesPort(t *testing.T) {
	var _ integration.AIIntegration = New()
	var _ integration.ForgeIntegration = New()
	if New().Name() != "mock" {
		t.Errorf("Name = %q, want mock", New().Name())
	}
}

func TestSummarizeWorkspace(t *testing.T) {
	prs := []integration.PullRequest{{Number: 1}, {Number: 2}}
	got, err := New().SummarizeWorkspace(context.Background(), "ship it", "mock recap", prs)
	if err != nil {
		t.Fatal(err)
	}
	if want := "mock workspace summary (2 PRs)"; got != want {
		t.Errorf("SummarizeWorkspace = %q, want %q", got, want)
	}

	// Deterministic across calls.
	again, _ := New().SummarizeWorkspace(context.Background(), "ship it", "mock recap", prs)
	if again != got {
		t.Errorf("SummarizeWorkspace not deterministic: %q vs %q", again, got)
	}

	// Nothing to summarize → empty.
	if empty, _ := New().SummarizeWorkspace(context.Background(), "", "", nil); empty != "" {
		t.Errorf("SummarizeWorkspace(empty) = %q, want empty", empty)
	}
}

// The mock's GenerateName satisfies the kernel's intent-first naming CONTRACT
// (four KEY: value lines) deterministically — the proof the AI naming port is
// genuinely swappable with `[ai] provider = "mock"`. slugRe mirrors the slug
// the kernel's parseWorkspacePlan sanitizes to.
var slugRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// keyLine extracts the value of a "KEY: value" line from the mock's output.
func keyLine(out, key string) string {
	for _, ln := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(ln, key+": "); ok {
			return strings.TrimSpace(v)
		}
		if ln == key+":" {
			return ""
		}
	}
	return ""
}

func TestGenerateName_KeyContract(t *testing.T) {
	got, err := New().GenerateName(context.Background(), workspaceNamingSentinel,
		"REPO INDEX:\nwawa/web-app\nEXISTING TAGS: (none)\nINTENT: Add dark mode toggle!!!")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TITLE", "SLUG", "TAG", "REPOS"} {
		if !strings.Contains(got, key+":") {
			t.Errorf("output missing %s line:\n%s", key, got)
		}
	}
	if slug := keyLine(got, "SLUG"); !slugRe.MatchString(slug) {
		t.Errorf("SLUG %q is not a clean slug", slug)
	}
	// REPOS echoes the first indexed repo so the sandbox materializes a worktree.
	if repos := keyLine(got, "REPOS"); repos != "wawa/web-app" {
		t.Errorf("REPOS = %q, want wawa/web-app (first in index)", repos)
	}
}

func TestGenerateName_Deterministic(t *testing.T) {
	in := "REPO INDEX:\n(none)\nEXISTING TAGS: (none)\nINTENT: same intent"
	a, _ := New().GenerateName(context.Background(), workspaceNamingSentinel, in)
	b, _ := New().GenerateName(context.Background(), workspaceNamingSentinel, in)
	if a != b {
		t.Errorf("GenerateName not deterministic: %q vs %q", a, b)
	}
}

func TestGenerateName_ReusesMentionedTag(t *testing.T) {
	got, _ := New().GenerateName(context.Background(), workspaceNamingSentinel,
		"REPO INDEX:\n(none)\nEXISTING TAGS: billing, infra\nINTENT: billing webhook 500s on retry")
	if tag := keyLine(got, "TAG"); tag != "billing" {
		t.Errorf("TAG = %q, want billing (intent mentions it)", tag)
	}
	// No mentioned tag → empty.
	got, _ = New().GenerateName(context.Background(), workspaceNamingSentinel,
		"REPO INDEX:\n(none)\nEXISTING TAGS: billing, infra\nINTENT: add dark mode toggle")
	if tag := keyLine(got, "TAG"); tag != "" {
		t.Errorf("TAG = %q, want empty (no existing tag mentioned)", tag)
	}
}

// workspaceNamingSentinel stands in for the kernel's workspace-naming system
// prompt (the mock keys only on the wrapped intent, so any value works).
const workspaceNamingSentinel = "name a workspace"
