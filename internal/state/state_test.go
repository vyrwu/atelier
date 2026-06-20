package state

import "testing"

func TestExtractTool_WorkspaceScoped(t *testing.T) {
	if got, want := extractTool("_atelier_lazygit_12_34"), "lazygit"; got != want {
		t.Fatalf("extractTool: got %q want %q", got, want)
	}
}

func TestExtractTool_SessionGlobal(t *testing.T) {
	if got, want := extractTool("_atelier_k8s"), "k8s"; got != want {
		t.Fatalf("extractTool: got %q want %q", got, want)
	}
}

func TestExtractTool_NonAtelier(t *testing.T) {
	if got := extractTool("my-workspace"); got != "" {
		t.Fatalf("extractTool: expected empty for non-atelier session, got %q", got)
	}
}
