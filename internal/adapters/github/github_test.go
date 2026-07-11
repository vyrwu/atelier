package github

import (
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		state   string
		isDraft bool
		want    integration.ForgeState
	}{
		{"OPEN", false, integration.ForgeOpen},
		{"open", false, integration.ForgeOpen},
		{"OPEN", true, integration.ForgeDraft},
		{"MERGED", false, integration.ForgeMerged},
		{"CLOSED", false, integration.ForgeClosed},
		{"  merged  ", false, integration.ForgeMerged},
		{"weird", false, integration.ForgeOpen}, // unexpected → open
		{"weird", true, integration.ForgeDraft}, // unexpected + draft → draft
	}
	for _, c := range cases {
		if got := classify(c.state, c.isDraft); got != c.want {
			t.Errorf("classify(%q, %v) = %q, want %q", c.state, c.isDraft, got, c.want)
		}
	}
}

// TestStatus_EmptyCwd_NoForge guards the non-git / no-cwd path: never shell
// out, always ForgeNone, never error.
func TestStatus_EmptyCwd_NoForge(t *testing.T) {
	st, err := Adapter{}.Status(integration.WorkspaceRef{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.State != integration.ForgeNone {
		t.Errorf("empty cwd should be ForgeNone, got %q", st.State)
	}
}
