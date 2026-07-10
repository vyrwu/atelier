package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/plugin"
)

// TestListTools_RendersNameAndKind locks the `atelier tools list` output
// columns: name, kind (built-in/launcher), description.
func TestListTools_RendersNameAndKind(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate from real launchers
	plugin.RegisterBuiltin(&manifest.Manifest{
		Name:        "__toolstest_tool",
		Description: "fixture tool",
		Popup:       manifest.KindWorkspace,
	}, func(*cobra.Command) {})

	cmd := ToolsCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools list: %v", err)
	}
	out := buf.String()
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "__toolstest_tool") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("list missing the registered tool:\n%s", out)
	}
	if !strings.Contains(line, "built-in") {
		t.Errorf("kind column missing 'built-in': %q", line)
	}
	if !strings.Contains(line, "fixture tool") {
		t.Errorf("description missing: %q", line)
	}
}

// TestToolsCommand_UnknownTool_Errors verifies the dispatcher returns a
// clear error (not a panic / not silent success) for an unregistered name.
func TestToolsCommand_UnknownTool_Errors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cmd := ToolsCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"definitely-not-a-registered-tool-xyz"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected an unknown-tool error, got %v", err)
	}
}
