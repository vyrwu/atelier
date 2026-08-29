package toolselector

import (
	"testing"

	"github.com/vyrwu/atelier/internal/adapters/mock"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/plugin"
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

// TestBuildEntries_PopupToolIsDisplayed locks the fix for "Popup Tool is not
// being displayed in M-;": the built-in popupshell tool must surface as a
// "Popup" entry (labeled from its UI.PopupTitle), dispatching to
// `atelier tools popupshell open`.
func TestBuildEntries_PopupToolIsDisplayed(t *testing.T) {
	plugins := []plugin.Plugin{{
		Name: "popupshell",
		Manifest: &manifest.Manifest{
			Tool: true, Name: "popupshell", Description: "Shell popup",
			Binding: &manifest.Binding{Style: manifest.StyleFull, Invoke: "open"},
			UI:      &manifest.UI{Icon: "窓", AccentColor: "109", PopupTitle: "Popup"},
		},
	}}
	e := findEntry(buildEntries(plugins), "popupshell")
	if e == nil {
		t.Fatal("Popup tool missing from the M-; selector")
	}
	if e.Name != "Popup" {
		t.Errorf("Popup entry label = %q, want %q", e.Name, "Popup")
	}
}
