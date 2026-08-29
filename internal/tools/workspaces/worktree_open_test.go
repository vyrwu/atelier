package workspaces

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/statestore"
)

func TestBuildWorktreeLines(t *testing.T) {
	wts := []statestore.Worktree{
		{Repo: "acme/api", Branch: "fix/login", Path: "/wt/acme/api/fix-login"},
		{Repo: "acme/web", Branch: "main", Path: "/wt/acme/web/main"},
	}
	lines := buildWorktreeLines(wts)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	// Field 1 (display) shows repo + branch; field 2 (after tab) is the path.
	if !strings.HasPrefix(lines[0], "acme/api  fix/login\t") {
		t.Errorf("line[0] display wrong: %q", lines[0])
	}
	if got := worktreePathFromPicked(lines[0]); got != "/wt/acme/api/fix-login" {
		t.Errorf("path field = %q, want /wt/acme/api/fix-login", got)
	}
}

func TestWorktreePathFromPicked(t *testing.T) {
	// A line with the trailing tab-delimited path (fzf --ansi keeps the tab).
	if got := worktreePathFromPicked("acme/web  main\t/wt/acme/web/main"); got != "/wt/acme/web/main" {
		t.Errorf("got %q", got)
	}
	// No tab → return as-is (defensive).
	if got := worktreePathFromPicked("/plain/path"); got != "/plain/path" {
		t.Errorf("got %q", got)
	}
}
