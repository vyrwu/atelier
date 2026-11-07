package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentSpaceFromDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "current", "repo", "branch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATELIER_ROOT", root)
	t.Setenv("ATELIER_SLUG", "")
	t.Setenv("TMUX_PANE", "")
	t.Chdir(dir)
	if got := currentSpace(); got != "current" {
		t.Fatalf("currentSpace() = %q; want current for a shell outside tmux", got)
	}
	alias := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATELIER_ROOT", alias)
	if got := currentSpace(); got != "current" {
		t.Fatalf("currentSpace() = %q with a symlinked root; want current", got)
	}
	t.Chdir(root)
	if got := currentSpace(); got != "" {
		t.Fatalf("root itself is not a space: %q", got)
	}
	t.Chdir(t.TempDir())
	if got := currentSpace(); got != "" {
		t.Fatalf("unrelated directory is not a space: %q", got)
	}
}
