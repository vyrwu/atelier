package all

import (
	"testing"

	"github.com/vyrwu/atelier/internal/plugin"
)

// TestBuiltins_ToolFlagContract guards the opt-in tool registration: every
// first-party tool that should appear in the M-; selector must declare
// Manifest.Tool. A tool that forgets the flag would silently vanish from
// the selector. This runs in-process against the real registered manifests —
// importing this package (package all) has already registered them all.
func TestBuiltins_ToolFlagContract(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate from real launchers
	res, err := plugin.Discover()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	tool := map[string]bool{}
	seen := map[string]bool{}
	for _, p := range res.Plugins {
		seen[p.Name] = true
		if p.Manifest != nil {
			tool[p.Name] = p.Manifest.Tool
		}
	}

	// Only the pre-launch-logic pickers stay compiled-in. Simpler tools
	// (popupshell, lazygit, gh-dash, gh-enhance, ccusage) are now [tools.*]
	// config launchers, not built-ins.
	mustBeTool := []string{
		"k8s", "aws", "pg", "workspaces",
	}
	for _, name := range mustBeTool {
		if !seen[name] {
			t.Errorf("tool %q was not registered", name)
			continue
		}
		if !tool[name] {
			t.Errorf("tool %q must declare Tool=true or it vanishes from the M-; selector", name)
		}
	}

	// The selector must be registered but NOT as a tool. Assert BOTH — a
	// bare `if tool["toolselector"]` would also pass if the selector
	// silently stopped being registered at all.
	if !seen["toolselector"] {
		t.Errorf("toolselector must be registered")
	}
	if tool["toolselector"] {
		t.Errorf("toolselector must not register itself as a tool")
	}
}

// TestAgentAndForge_AreKernelIntegrations_NotTools locks the hexagonal
// split: the AI agent (claude) and the code forge (github/ghpr) are
// swappable integration ADAPTERS selected by config — they fill kernel
// capability slots (agent/attention/summary; forge badge). They must NOT be
// registered tools in the M-; selector.
func TestAgentAndForge_AreKernelIntegrations_NotTools(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	res, _ := plugin.Discover()
	for _, name := range []string{"claude", "ghpr"} {
		if _, ok := res.FindByName(name); ok {
			t.Errorf("%q must NOT be a registered tool — it is a kernel integration adapter selected via [integrations] in config", name)
		}
	}
}
