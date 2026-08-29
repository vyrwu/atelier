package mock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
)

// TestForge_ListsFromFixture verifies the mock forge is a real, deterministic
// ForgeIntegration: it reads the repoPath->[]ForgePR fixture under the active
// config home and lists PRs offline (no gh).
func TestForge_ListsFromFixture(t *testing.T) {
	var _ integration.ForgeIntegration = New()

	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "atelier"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := map[string][]ForgePR{
		"/repos/api": {
			{Number: 1, Repo: "acme/api", Title: "add endpoint", State: "open", CI: "pass", ReviewDecision: "approved", Branch: "feat/endpoint"},
			{Number: 2, Repo: "acme/api", Title: "wip", State: "draft", CI: "pending", Branch: "feat/wip"},
		},
		"/repos/web": {
			{Number: 7, Repo: "acme/web", Title: "fix nav", State: "merged", Branch: "fix/nav"},
		},
	}
	data, _ := json.Marshal(fixture)
	if err := os.WriteFile(MockForgeFixturePath(), data, 0o644); err != nil {
		t.Fatal(err)
	}

	prs, err := New().List("/repos/api")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("List(/repos/api) len = %d, want 2", len(prs))
	}
	if prs[0].Number != 1 || prs[0].State != integration.ForgeOpen || prs[0].CI != integration.CIPass ||
		prs[0].ReviewDecision != integration.ReviewApproved || prs[0].Repo != "acme/api" || prs[0].Branch != "feat/endpoint" {
		t.Errorf("PR[0] = %+v, mismatch", prs[0])
	}
	if prs[1].State != integration.ForgeDraft || prs[1].CI != integration.CIPending {
		t.Errorf("PR[1] = %+v, want draft/pending", prs[1])
	}

	web, _ := New().List("/repos/web")
	if len(web) != 1 || web[0].State != integration.ForgeMerged {
		t.Errorf("List(/repos/web) = %+v, want one merged PR", web)
	}

	// Unmapped repoPath and empty repoPath → nil (no PRs).
	if got, _ := New().List("/repos/unknown"); got != nil {
		t.Errorf("unmapped repoPath = %+v, want nil", got)
	}
	if got, _ := New().List(""); got != nil {
		t.Errorf("empty repoPath = %+v, want nil", got)
	}
}

// TestForge_NoFixture is nil everywhere when the fixture is absent (the
// standalone / non-sandbox case).
func TestForge_NoFixture(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got, _ := New().List("/anything"); got != nil {
		t.Errorf("List = %+v, want nil (no fixture)", got)
	}
}

// TestForge_OpenClose are no-ops that never error (the mock is stateless).
func TestForge_OpenClose(t *testing.T) {
	pr := integration.PullRequest{Number: 1, State: integration.ForgeOpen}
	if err := New().Open(pr); err != nil {
		t.Errorf("Open: %v", err)
	}
	if err := New().Close(pr); err != nil {
		t.Errorf("Close: %v", err)
	}
}
