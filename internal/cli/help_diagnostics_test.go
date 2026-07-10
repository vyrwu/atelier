package cli

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/adapters/github"
	"github.com/vyrwu/atelier/internal/adapters/mock"
	"github.com/vyrwu/atelier/internal/integration"
)

func findDiag(ds []diagnostic, substr string) *diagnostic {
	for i := range ds {
		if strings.Contains(ds[i].message, substr) {
			return &ds[i]
		}
	}
	return nil
}

// TestIntegrationDiagnostics covers the fix for the "PR badge vanished"
// report: forge is opt-in, so the diagnostics must (a) confirm the forge
// adapter by name when configured, and (b) surface an actionable hint (not
// silence) when it's off — mirroring the AI line.
func TestIntegrationDiagnostics(t *testing.T) {
	// Both capabilities configured.
	ds := integrationDiagnostics(integration.Set{AI: mock.New(), Forge: github.New()})
	if d := findDiag(ds, "AI integration: mock"); d == nil {
		t.Error("missing AI integration line")
	}
	if d := findDiag(ds, "forge integration: github"); d == nil || d.status != 0 {
		t.Errorf("forge line wrong: %+v", d)
	}

	// Forge off (the default) → a warning with a config hint, AI absent.
	ds = integrationDiagnostics(integration.Set{})
	if d := findDiag(ds, "AI integration"); d != nil {
		t.Errorf("AI line present when AI disabled: %+v", d)
	}
	off := findDiag(ds, "forge integration: off")
	if off == nil {
		t.Fatal("missing forge-off line when forge disabled")
	}
	if off.status != 1 || off.hint == "" {
		t.Errorf("forge-off should warn with a hint, got %+v", off)
	}
}
