package main

import (
	"fmt"
	"os"

	"github.com/vyrwu/atelier/internal/adapters/claude"
	"github.com/vyrwu/atelier/internal/adapters/github"
	"github.com/vyrwu/atelier/internal/adapters/mock"
	"github.com/vyrwu/atelier/internal/config"
	"github.com/vyrwu/atelier/internal/integration"
)

// integrationsConfig is the `[integrations]` section of config.toml. It
// selects which adapter realizes each kernel capability port. An empty /
// absent value disables that capability; the kernel degrades gracefully.
//
//	[integrations]
//	forge = "github"   # per-workspace PR badge + open-in-browser (default: off)
//	ai    = "claude"   # workspace agent + naming + summary + attention (default: claude)
//
// AI defaults to "claude" (the flagship agent, and atelier's out-of-the-box
// behavior); set `ai = ""` to disable it or `ai = "mock"` to swap in the
// deterministic offline adapter. Forge defaults to off; `forge = "github"`
// needs `gh`, while `forge = "mock"` is the deterministic offline adapter
// (reads a fixture map — used by the demo sandbox and tests).
type integrationsConfig struct {
	Forge string `toml:"forge"`
	AI    string `toml:"ai"`
}

// composeIntegrations is the composition root: the ONLY place that maps
// config strings to concrete adapters. Keeping this out of internal/kernel
// preserves the dependency rule — the kernel never imports an adapter.
func composeIntegrations() integration.Set {
	cfg := integrationsConfig{AI: "claude"} // default: claude on
	_ = config.LoadSection("integrations", &cfg)

	var set integration.Set
	switch cfg.Forge {
	case "":
		// disabled (default)
	case "github":
		set.Forge = github.New()
	case "mock":
		set.Forge = mock.New()
	default:
		fmt.Fprintf(os.Stderr, "atelier: unknown [integrations] forge = %q (known: github, mock, \"\" to disable); forge disabled\n", cfg.Forge)
	}
	switch cfg.AI {
	case "":
		// explicitly disabled
	case "claude":
		set.AI = claude.New()
	case "mock":
		set.AI = mock.New()
	default:
		fmt.Fprintf(os.Stderr, "atelier: unknown [integrations] ai = %q (known: claude, mock, \"\" to disable); AI disabled\n", cfg.AI)
	}
	return set
}
