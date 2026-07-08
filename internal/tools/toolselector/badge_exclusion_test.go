package toolselector

import (
	"testing"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/plugin"
)

// The selector lists a plugin only if it explicitly registers a tool
// (Manifest.Tool). A provider plugin (ghpr: contributes a badge, Tool
// unset) must never appear; a normal tool must.
func TestBuildEntries_OnlyRegisteredTools(t *testing.T) {
	plugins := []plugin.Plugin{
		{
			Name: "ghpr",
			Manifest: &manifest.Manifest{
				Name:  "ghpr",
				Popup: manifest.KindNone,
				Badge: &manifest.Badge{Option: "@ghpr_badge"},
				// Tool unset → provider only.
			},
		},
		{
			Name: "widget",
			Manifest: &manifest.Manifest{
				Name:  "widget",
				Popup: manifest.KindGlobal,
				Tool:  true,
			},
		},
		{
			Name: "toolselector",
			Manifest: &manifest.Manifest{
				Name: "toolselector",
				Tool: true, // even self-registered, excluded by name
			},
		},
	}

	entries := buildEntries(plugins)
	for _, e := range entries {
		if e.Kind == "ghpr" || e.Name == "ghpr" {
			t.Errorf("provider ghpr (Tool unset) must not appear, got %+v", e)
		}
		if e.Kind == "toolselector" {
			t.Errorf("toolselector must not appear in its own menu, got %+v", e)
		}
	}
	var sawWidget bool
	for _, e := range entries {
		if e.Kind == "widget" {
			sawWidget = true
		}
	}
	if !sawWidget {
		t.Error("registered tool widget (Tool=true) should appear in the selector")
	}
}
