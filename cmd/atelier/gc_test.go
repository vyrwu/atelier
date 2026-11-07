package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vyrwu/atelier/internal/core"
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
	if got := cwdSpace(); got != "current" {
		t.Fatalf("cwdSpace() = %q; want current for a shell outside tmux", got)
	}
	alias := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATELIER_ROOT", alias)
	if got := cwdSpace(); got != "current" {
		t.Fatalf("cwdSpace() = %q with a symlinked root; want current", got)
	}
	t.Chdir(root)
	if got := cwdSpace(); got != "" {
		t.Fatalf("root itself is not a space: %q", got)
	}
	t.Chdir(t.TempDir())
	if got := cwdSpace(); got != "" {
		t.Fatalf("unrelated directory is not a space: %q", got)
	}
}

func TestCurrentSpacesProtectEnclosingAndCWD(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "cwd-space", "repo", "branch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATELIER_ROOT", root)
	t.Setenv("ATELIER_SLUG", "enclosing")
	t.Setenv("TMUX_PANE", "")
	t.Chdir(dir)
	got := currentSpaces(&core.State{})
	if !slices.Contains(got, "enclosing") || !slices.Contains(got, "cwd-space") {
		t.Fatalf("protected spaces = %v; want both enclosing and cwd spaces", got)
	}
}
