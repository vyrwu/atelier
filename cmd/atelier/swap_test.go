package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
)

// Crossing agents returns to the target's prior conversation. Choosing the
// current agent is intentionally different: it starts a fresh conversation,
// seeded by exactly the same handoff note.
func TestHandoffAgentCmdSessionChoice(t *testing.T) {
	note := "read the exported conversation"
	for _, from := range []agent.Kind{agent.Claude, agent.Codex} {
		to := from.Other()
		cross := handoffAgentCmd(from, to, "target-session", note)
		if !strings.Contains(cross, "resume") || !strings.Contains(cross, "target-session") {
			t.Errorf("%s → %s does not resume the target: %q", from, to, cross)
		}
		same := handoffAgentCmd(from, from, "old-session", note)
		if strings.Contains(same, "resume") || strings.Contains(same, "old-session") || !strings.Contains(same, note) {
			t.Errorf("%s → fresh %s must start from the handoff: %q", from, from, same)
		}
	}
}

// The second launcher must be refused both while RunAgent is launching and in
// the gap before its child starts. The handoff then owns the same exclusion.
func TestSwapReservationCoversLaunchAndHandoff(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	const slug = "test-space"
	token, releaseLaunch, err := reserveSwap(slug)
	if err != nil || token == "" {
		t.Fatalf("reserve launch: %q, %v", token, err)
	}
	assertBusy := func() {
		t.Helper()
		if other, release, err := reserveSwap(slug); err != nil || other != "" {
			if release != nil {
				release()
			}
			t.Fatalf("competing launch was admitted: %q, %v", other, err)
		}
	}
	assertBusy()
	releaseLaunch()
	assertBusy() // the old bug: no process holds the lock at this point
	releaseHandoff, err := takeSwap(slug, token)
	if err != nil {
		t.Fatal(err)
	}
	assertBusy()
	releaseHandoff()
	next, release, err := reserveSwap(slug)
	if err != nil || next == "" {
		t.Fatalf("completed handoff did not release the space: %q, %v", next, err)
	}
	release()
	if stale, err := takeSwap(slug, token); err == nil {
		stale()
		t.Fatal("an obsolete handoff claimed the next launch")
	}
	finish, err := takeSwap(slug, next)
	if err != nil {
		t.Fatal(err)
	}
	finish()
}

// A fresh same-agent conversation gets a saved export and no model is asked
// to summarize first. A failed save aborts rather than handing over just the
// original task and silently losing the recent work.
func TestHandoffNoteExportsOrFails(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("ATELIER_ROOT", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	ws := core.Workspace{Slug: "test-space", Intent: "fix login", Created: time.Now().Add(-time.Hour)}
	if err := os.MkdirAll(ws.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate a directory made by the older, permissive build.
	handoffDir := filepath.Join(ws.Root(), ".atelier", "handoff")
	if err := os.MkdirAll(handoffDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(handoffDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(ws.Root(), "-")
	transcriptDir := filepath.Join(dir, "claude", "projects", project)
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := fmt.Sprintf(`{"type":"user","entrypoint":"cli","cwd":%q,"timestamp":%q,"message":{"content":"fix the new regression"}}`, ws.Root(), time.Now().Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(transcriptDir, "session.jsonl"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	note, err := handoffNote(ws, agent.Claude, agent.Claude, time.Now())
	if err != nil || !strings.Contains(note, "previous Claude Code conversation") {
		t.Fatalf("handoff note = %q, %v", note, err)
	}
	files, _ := filepath.Glob(filepath.Join(ws.Root(), ".atelier", "handoff", "*.md"))
	if len(files) != 1 {
		t.Fatalf("handoff files = %v", files)
	}
	for path, mode := range map[string]os.FileMode{handoffDir: 0o700, files[0]: 0o600} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != mode {
			t.Errorf("%s mode = %o, want %o", path, fi.Mode().Perm(), mode)
		}
	}
	export, _ := os.ReadFile(files[0])
	if !strings.Contains(string(export), "fix the new regression") || !strings.Contains(note, files[0]) {
		t.Errorf("handoff lost the conversation: %s / %s", note, export)
	}
	// A non-directory .atelier blocks a save without relying on permissions
	// (CI may run as root).
	ws.Slug = "blocked"
	if err := os.MkdirAll(ws.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root(), ".atelier"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	project = regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(ws.Root(), "-")
	transcriptDir = filepath.Join(dir, "claude", "projects", project)
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record = fmt.Sprintf(`{"type":"user","entrypoint":"cli","cwd":%q,"timestamp":%q,"message":{"content":"work must survive"}}`, ws.Root(), time.Now().Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(transcriptDir, "session.jsonl"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := handoffNote(ws, agent.Claude, agent.Codex, time.Now()); err == nil {
		t.Error("a failed export save must abort the handoff")
	}
}
