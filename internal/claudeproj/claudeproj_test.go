package claudeproj

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectSlug(t *testing.T) {
	cases := map[string]string{
		"/Users/a/code/github/vyrwu/atelier":      "-Users-a-code-github-vyrwu-atelier",
		"/Users/a/code/.worktrees/github/x/fix/y": "-Users-a-code--worktrees-github-x-fix-y",
	}
	for in, want := range cases {
		if got := ProjectSlug(in); got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLatestSessionID(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	cwd := "/Users/a/code/.worktrees/github/x/fix/y"
	dir := filepath.Join(cfg, "projects", ProjectSlug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(dir, "older.jsonl")
	newer := filepath.Join(dir, "newer.jsonl")
	for _, f := range []string{older, newer} {
		if err := os.WriteFile(f, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older, old, old); err != nil {
		t.Fatal(err)
	}

	if got := LatestSessionID(cwd); got != "newer" {
		t.Errorf("LatestSessionID = %q, want newer", got)
	}
	if got := LatestSessionID("/nowhere"); got != "" {
		t.Errorf("LatestSessionID(missing) = %q, want empty", got)
	}
	if !TranscriptExists("newer") {
		t.Error("TranscriptExists(newer) = false, want true")
	}
}
