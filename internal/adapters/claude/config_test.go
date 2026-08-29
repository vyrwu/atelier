package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// writeAIConfig writes a config.toml containing body into a temp XDG config
// home and points LoadConfig at it. Returns nothing — LoadConfig reads the
// process env via config.Path().
func writeAIConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir := filepath.Join(home, "atelier")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultConfig_ModelResolution(t *testing.T) {
	c := DefaultConfig()
	if got := c.NamingModel(); got != "sonnet" {
		t.Errorf("NamingModel default = %q, want sonnet", got)
	}
	if got := c.RecapModel(); got != "haiku" {
		t.Errorf("RecapModel default (inherits model) = %q, want haiku", got)
	}
	if got := c.SummaryModel(); got != "haiku" {
		t.Errorf("SummaryModel default (inherits model) = %q, want haiku", got)
	}
}

// No config file → defaults, with prompt constants filled in.
func TestLoadConfig_NoFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.NamingModel() != "sonnet" || c.RecapModel() != "haiku" {
		t.Errorf("no-file models: naming=%q recap=%q, want sonnet/haiku", c.NamingModel(), c.RecapModel())
	}
	if c.SummaryModel() != "haiku" {
		t.Errorf("no-file summary model = %q, want haiku", c.SummaryModel())
	}
	if c.Prompts.Recap != DefaultRecapSystemPrompt {
		t.Error("recap prompt should fall back to DefaultRecapSystemPrompt")
	}
	if c.Prompts.Workspace != DefaultWorkspaceSystemPrompt {
		t.Error("workspace prompt should fall back to DefaultWorkspaceSystemPrompt")
	}
	if c.Prompts.Summary != DefaultSummarySystemPrompt {
		t.Error("summary prompt should fall back to DefaultSummarySystemPrompt")
	}
}

// A bare global `model` propagates to recap (which has no default override)
// but NOT to naming (whose default override is sonnet).
func TestLoadConfig_GlobalModelPropagation(t *testing.T) {
	writeAIConfig(t, "[ai]\nmodel = \"opus\"\n")
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.RecapModel(); got != "opus" {
		t.Errorf("RecapModel = %q, want opus (inherits global model)", got)
	}
	if got := c.NamingModel(); got != "sonnet" {
		t.Errorf("NamingModel = %q, want sonnet (default override preserved)", got)
	}
}

// An explicit empty naming override opts back into inheriting `model`.
func TestLoadConfig_EmptyNamingInheritsModel(t *testing.T) {
	writeAIConfig(t, "[ai]\nmodel = \"opus\"\n[ai.models]\nnaming = \"\"\n")
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.NamingModel(); got != "opus" {
		t.Errorf("NamingModel = %q, want opus (explicit empty override inherits model)", got)
	}
}

// Per-task overrides win over the global model.
func TestLoadConfig_TaskOverrides(t *testing.T) {
	writeAIConfig(t, "[ai]\nmodel = \"haiku\"\n[ai.models]\nnaming = \"opus\"\nrecap = \"sonnet\"\n")
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.NamingModel() != "opus" || c.RecapModel() != "sonnet" {
		t.Errorf("overrides: naming=%q recap=%q, want opus/sonnet", c.NamingModel(), c.RecapModel())
	}
}

func TestLoadConfig_PromptOverrides(t *testing.T) {
	writeAIConfig(t, "[ai.prompts]\nrecap = \"custom recap\"\nworkspace = \"custom workspace\"\nsummary = \"custom summary\"\n")
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Prompts.Recap != "custom recap" {
		t.Errorf("Prompts.Recap = %q, want custom recap", c.Prompts.Recap)
	}
	if c.Prompts.Workspace != "custom workspace" {
		t.Errorf("Prompts.Workspace = %q, want custom workspace", c.Prompts.Workspace)
	}
	if c.Prompts.Summary != "custom summary" {
		t.Errorf("Prompts.Summary = %q, want custom summary", c.Prompts.Summary)
	}
}
