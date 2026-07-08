//go:build e2e

package toolselector

import (
	"testing"

	"github.com/vyrwu/atelier/internal/plugin"
	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestManifests_ToolFlagContract guards the opt-in tool registration:
// discovery is run against the real, freshly-built plugin binaries and
// every first-party tool must declare Manifest.Tool. Without this, a tool
// that forgets the flag would silently vanish from the M-; selector.
// Providers (ghpr) and the selector itself must NOT be tools.
func TestManifests_ToolFlagContract(t *testing.T) {
	srv := testtmux.New(t)
	res, err := plugin.Discover(srv.BinDir())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	tool := map[string]bool{}
	for _, p := range res.Plugins {
		if p.Manifest != nil {
			tool[p.Name] = p.Manifest.Tool
		}
	}

	mustBeTool := []string{
		"claude", "k8s", "aws", "lazygit", "pg",
		"popupshell", "ccusage", "ghdash", "ghenhance", "workspaces",
	}
	for _, name := range mustBeTool {
		got, ok := tool[name]
		if !ok {
			t.Errorf("tool %q was not discovered", name)
			continue
		}
		if !got {
			t.Errorf("tool %q must declare Tool=true or it vanishes from the M-; selector", name)
		}
	}

	// Providers / the selector must not register as tools.
	if got, ok := tool["ghpr"]; !ok || got {
		t.Errorf("ghpr must be discovered as a non-tool provider (Tool=false), got ok=%v tool=%v", ok, got)
	}
	if tool["toolselector"] {
		t.Errorf("toolselector must not register itself as a tool")
	}
}
