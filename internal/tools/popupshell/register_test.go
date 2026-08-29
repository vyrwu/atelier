package popupshell

import (
	"testing"

	"github.com/vyrwu/atelier/internal/manifest"
)

func TestManifest_ValidWorkspaceTool(t *testing.T) {
	if err := Manifest.Validate(); err != nil {
		t.Fatalf("Manifest.Validate: %v", err)
	}
	if !Manifest.Tool {
		t.Error("popupshell must be a Tool (so it shows in M-;)")
	}
	if Manifest.Popup != manifest.KindWorkspace {
		t.Errorf("Popup = %q, want workspace", Manifest.Popup)
	}
	if Manifest.UI == nil || Manifest.UI.PopupTitle != "Popup" {
		t.Errorf("selector label must be %q", "Popup")
	}
	// No dedicated key — it lives in the M-; menu only.
	if Manifest.Binding.Key != "" {
		t.Errorf("popupshell should not bind a global key, got %q", Manifest.Binding.Key)
	}
}

func TestOpenCommand(t *testing.T) {
	if OpenCommand().Use != "open" {
		t.Error("OpenCommand should be `open` (the primary invoke)")
	}
}
