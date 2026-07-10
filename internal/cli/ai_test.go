package cli

import (
	"testing"

	"github.com/vyrwu/atelier/internal/adapters/mock"
	"github.com/vyrwu/atelier/internal/integration"
)

// TestActiveAI_GatedWhenUnset: with no AI adapter installed, the kernel's ai
// commands must error clearly rather than no-op (predictable degradation).
func TestActiveAI_GatedWhenUnset(t *testing.T) {
	integration.SetActive(integration.Set{})
	defer integration.SetActive(integration.Set{})
	if _, err := activeAI(); err == nil {
		t.Fatal("activeAI must error when no AI integration is configured")
	}
}

// TestActiveAI_ResolvesInjectedAdapter proves the swap: whatever adapter the
// composition root installs is what the kernel drives through the port.
func TestActiveAI_ResolvesInjectedAdapter(t *testing.T) {
	integration.SetActive(integration.Set{AI: mock.New()})
	defer integration.SetActive(integration.Set{})
	ai, err := activeAI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ai.Name() != "mock" {
		t.Errorf("activeAI resolved %q, want the injected mock adapter", ai.Name())
	}
}
