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

// providerConfig selects which adapter realizes a kernel capability port. AI
// lives under `[ai] provider`, forge under `[forge] provider`; an empty /
// absent value disables that capability and the kernel degrades gracefully.
//
//	[ai]
//	provider = "claude"   # workspace agent + naming + summary + attention (default: claude)
//	[forge]
//	provider = "github"   # per-workspace PR badge + open-in-browser (default: off)
//
// AI defaults to "claude" (the flagship agent, and atelier's out-of-the-box
// behavior); set `provider = ""` to disable it or `provider = "mock"` to swap
// in the deterministic offline adapter. Forge defaults to off; `github` needs
// `gh`, while `mock` is the deterministic offline adapter (reads a fixture
// map — used by the demo sandbox and tests). Model/prompt tuning lives under
// [ai] too but is owned by the active adapter, not this selector.
type providerConfig struct {
	Provider string `toml:"provider"`
}

// composeIntegrations is the composition root: the ONLY place that maps
// config strings to concrete adapters. Keeping this out of internal/kernel
// preserves the dependency rule — the kernel never imports an adapter.
func composeIntegrations() integration.Set {
	ai := providerConfig{Provider: "claude"} // default: claude on
	_ = config.LoadSection("ai", &ai)
	forge := providerConfig{} // default: off
	_ = config.LoadSection("forge", &forge)

	var set integration.Set
	switch forge.Provider {
	case "":
		// disabled (default)
	case "github":
		set.Forge = github.New()
	case "mock":
		set.Forge = mock.New()
	default:
		fmt.Fprintf(os.Stderr, "atelier: unknown [forge] provider = %q (known: github, mock, \"\" to disable); forge disabled\n", forge.Provider)
	}
	switch ai.Provider {
	case "":
		// explicitly disabled
	case "claude":
		set.AI = claude.New()
	case "mock":
		set.AI = mock.New()
	default:
		fmt.Fprintf(os.Stderr, "atelier: unknown [ai] provider = %q (known: claude, mock, \"\" to disable); AI disabled\n", ai.Provider)
	}
	return set
}
