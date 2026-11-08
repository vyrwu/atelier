package gc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
)

func TestPurgeKeepsWorktreeWithoutGitMarker(t *testing.T) {
	root := sandbox(t)
	wt := space(t, root, "broken", repo(t), "feat")
	file := filepath.Join(wt, "uncommitted.txt")
	touch(t, file, time.Now())
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	put(t, core.Workspace{Slug: "broken", Session: "broken", Retired: true})
	if got := Unsaved(core.Workspace{Slug: "broken"}); len(got) == 0 {
		t.Fatal("missing git marker was treated as clean")
	}
	if why, err := (Policy{}).Purge(context.Background(), "broken", time.Now()); err != nil || !strings.Contains(why, "unsaved") {
		t.Fatalf("purge did not keep unverifiable work: %q, %v", why, err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("purge removed unverified work")
	}
}

func TestUnsavedUnreadableDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permissions")
	}
	root := sandbox(t)
	locked := filepath.Join(root, "space", "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if got := Unsaved(core.Workspace{Slug: "space"}); len(got) == 0 {
		t.Fatal("unreadable directory was treated as clean")
	}
}

func TestSweepRecoversCodexConversationBeforeDirectoryEdit(t *testing.T) {
	root := sandbox(t)
	dir := filepath.Join(root, "recover")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(core.WorkspaceGuidePath(), filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "rollout-recover.jsonl")
	old := time.Now().Add(-24 * time.Hour)
	data, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"id": "existing", "cwd": dir, "source": "cli"}})
	touch(t, path, old)
	// #nosec G703 -- sandbox(t) sets CODEX_HOME to this test's temporary directory.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "later-note.txt"), time.Now())
	if _, err := Sweep(false, time.Now()); err != nil {
		t.Fatal(err)
	}
	w := core.Load().Find("recover")
	if w == nil || w.Agent != string(agent.Codex) || !w.Created.IsZero() || !w.Retired {
		t.Fatalf("incorrect recovery: %+v", w)
	}
	if id := agent.LatestSession(agent.Of(w.Agent), w.Root(), w.Created); id != "existing" {
		t.Fatalf("recovered space lost its conversation: %q", id)
	}
	if LastActive(*w).Before(old.Add(time.Hour)) {
		t.Fatal("recovery lost the directory's recent activity")
	}
}
