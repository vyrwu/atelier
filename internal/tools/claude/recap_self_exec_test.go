package claude

import (
	"os"
	"strings"
	"testing"
)

// TestNotifyAttention_RecapInvocationUsesOwnBinary locks in the fix for
// the "claude: unknown command 'tools'" hook crash. notify-attention
// MUST re-invoke atelier-claude with its OWN subcommand name
// (`_recap-from-hook`), NOT `tools claude _recap-from-hook` — that
// prefix is only valid on the main `atelier` dispatcher.
//
// We assert via source inspection because the actual exec.Command call
// only fires asynchronously after a real Claude hook payload arrives;
// no good way to observe it from a unit test.
func TestNotifyAttention_RecapInvocationUsesOwnBinary(t *testing.T) {
	src, err := os.ReadFile("claude.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if strings.Contains(string(src), `exec.Command(self, "tools", "claude", "_recap-from-hook"`) {
		t.Errorf("notify-attention still constructs `atelier-claude tools claude _recap-from-hook`; " +
			"that path doesn't exist on atelier-claude. Use `exec.Command(self, \"_recap-from-hook\", ...)`.")
	}
	if !strings.Contains(string(src), `exec.Command(self, "_recap-from-hook"`) {
		t.Errorf("expected exec.Command(self, \"_recap-from-hook\", ...) in notify-attention; not found")
	}
}
