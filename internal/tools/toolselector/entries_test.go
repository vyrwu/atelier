package toolselector

import (
	"testing"

	"github.com/vyrwu/atelier/internal/adapters/mock"
	"github.com/vyrwu/atelier/internal/integration"
)

func findEntry(entries []entry, kind string) *entry {
	for i := range entries {
		if entries[i].Kind == kind {
			return &entries[i]
		}
	}
	return nil
}

// TestBuildEntries_AIEntryUsesAdapterDisplayName locks the fix for the
// "Claude Code entry vanished from M-;" report: the AI agent is a config
// integration (not a registered tool), so the selector synthesizes its
// entry. That entry must be labeled with the ACTIVE adapter's own product
// name (so a swapped adapter reads correctly, not a generic "AI Agent"),
// and must be absent entirely when no AI adapter is configured.
func TestBuildEntries_AIEntryUsesAdapterDisplayName(t *testing.T) {
	prev := integration.Active()
	t.Cleanup(func() { integration.SetActive(prev) })

	integration.SetActive(integration.Set{AI: mock.New()})
	got := findEntry(buildEntries(nil), "ai:open")
	if got == nil {
		t.Fatal("AI entry missing from selector when an AI adapter is active")
	}
	if want := mock.New().DisplayName(); got.Name != want {
		t.Errorf("AI entry label = %q, want adapter DisplayName %q", got.Name, want)
	}

	integration.SetActive(integration.Set{}) // AI disabled
	if e := findEntry(buildEntries(nil), "ai:open"); e != nil {
		t.Errorf("AI entry present (%q) when no AI adapter is configured", e.Name)
	}
}
