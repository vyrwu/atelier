package mock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
)

// TestForge_ClassifiesFromFixture verifies the mock forge is a real,
// deterministic ForgeIntegration: it reads the cwd->state fixture under the
// active config home and classifies workspaces offline (no gh).
func TestForge_ClassifiesFromFixture(t *testing.T) {
	var _ integration.ForgeIntegration = New()

	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "atelier"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := map[string]string{
		"/wt/open":   "open",
		"/wt/draft":  "draft",
		"/wt/merged": "merged",
		"/wt/closed": "closed",
	}
	data, _ := json.Marshal(fixture)
	if err := os.WriteFile(MockForgeFixturePath(), data, 0o644); err != nil {
		t.Fatal(err)
	}

	for cwd, want := range fixture {
		st, err := New().Status(integration.WorkspaceRef{Cwd: cwd})
		if err != nil {
			t.Fatalf("Status(%s): %v", cwd, err)
		}
		if string(st.State) != want {
			t.Errorf("Status(%s).State = %q, want %q", cwd, st.State, want)
		}
	}

	// Unmapped workspace and empty cwd → ForgeNone (badge cleared).
	if st, _ := New().Status(integration.WorkspaceRef{Cwd: "/wt/unknown"}); st.State != integration.ForgeNone {
		t.Errorf("unmapped cwd State = %q, want none", st.State)
	}
	if st, _ := New().Status(integration.WorkspaceRef{Cwd: ""}); st.State != integration.ForgeNone {
		t.Errorf("empty cwd State = %q, want none", st.State)
	}
}

// TestForge_NoFixture is ForgeNone everywhere when the fixture is absent
// (the standalone / non-sandbox case).
func TestForge_NoFixture(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if st, _ := New().Status(integration.WorkspaceRef{Cwd: "/anything"}); st.State != integration.ForgeNone {
		t.Errorf("State = %q, want none (no fixture)", st.State)
	}
}
