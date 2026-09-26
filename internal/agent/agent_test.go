package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

func writeStatus(t *testing.T, session, body string) {
	t.Helper()
	path := filepath.Join(core.CacheDir(), "agents", session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Status is what the overlay glyph and the attention badge are drawn from.
func TestStatus(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if got := Status("never-seen", false); got != core.StatusGone {
		t.Errorf("dead session = %q, want gone", got)
	}
	// A live session with no status file yet has simply not reported — idle, not
	// gone, or a just-created space would render as dead.
	if got := Status("never-seen", true); got != core.StatusIdle {
		t.Errorf("live session with no status file = %q, want idle", got)
	}

	writeStatus(t, "blocked-one", fmt.Sprintf("blocked\n%d\n", time.Now().Unix()))
	if got := Status("blocked-one", true); got != core.StatusBlocked {
		t.Errorf("blocked session = %q", got)
	}
	// A dead window wins over whatever the file last said.
	if got := Status("blocked-one", false); got != core.StatusGone {
		t.Errorf("dead session with a stored status = %q, want gone", got)
	}
}

// A turn that dies without its Stop hook (crash, kill, hang) would otherwise
// claim to be working forever. It settles to idle after workingStaleAfter, and
// the next tool event restores it.
func TestStatusStaleWorkingDecays(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	fresh := time.Now().Add(-workingStaleAfter / 2).Unix()
	writeStatus(t, "fresh", fmt.Sprintf("working\n%d\n", fresh))
	if got := Status("fresh", true); got != core.StatusWorking {
		t.Errorf("recent working = %q, want working", got)
	}

	stale := time.Now().Add(-workingStaleAfter - time.Minute).Unix()
	writeStatus(t, "stale", fmt.Sprintf("working\n%d\n", stale))
	if got := Status("stale", true); got != core.StatusIdle {
		t.Errorf("stale working = %q, want it decayed to idle", got)
	}

	// Only "working" decays — a blocked agent is waiting on the user for as long
	// as it takes, and must keep the badge lit.
	writeStatus(t, "old-blocked", fmt.Sprintf("blocked\n%d\n", stale))
	if got := Status("old-blocked", true); got != core.StatusBlocked {
		t.Errorf("old blocked = %q, want blocked", got)
	}
}

func TestReadStatusMalformed(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	writeStatus(t, "no-ts", "working\n")
	if st, ts := readStatus("no-ts"); st != core.StatusWorking || !ts.IsZero() {
		t.Errorf("no timestamp: (%q, %v)", st, ts)
	}
	// No parsable timestamp means no decay — better to trust the status than to
	// silently call a working agent idle.
	if got := Status("no-ts", true); got != core.StatusWorking {
		t.Errorf("working with no timestamp = %q, want working", got)
	}

	writeStatus(t, "junk-ts", "working\nnot-a-number\n")
	if st, ts := readStatus("junk-ts"); st != core.StatusWorking || !ts.IsZero() {
		t.Errorf("junk timestamp: (%q, %v)", st, ts)
	}

	writeStatus(t, "blank", "")
	if st, _ := readStatus("blank"); st != "" {
		t.Errorf("blank file = %q, want empty", st)
	}
}

func TestSetStatusRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := SetStatus("sess", core.StatusBlocked); err != nil {
		t.Fatal(err)
	}
	st, ts := readStatus("sess")
	if st != core.StatusBlocked {
		t.Errorf("status = %q, want blocked", st)
	}
	if time.Since(ts) > time.Minute {
		t.Errorf("timestamp = %v, want ~now", ts)
	}
	if err := SetStatus("sess", core.StatusIdle); err != nil {
		t.Fatal(err)
	}
	if st, _ := readStatus("sess"); st != core.StatusIdle {
		t.Errorf("status after overwrite = %q, want idle", st)
	}
}

// The hook command is wired into the user's GLOBAL Claude settings, so it runs
// for their own non-atelier sessions too. Without $ATELIER_SESSION it must do
// nothing at all — not write a status file under an empty name.
func TestHandleHookIsGuarded(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("ATELIER_SESSION", "")

	if err := HandleHook("working"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cache, "atelier", "agents")); !os.IsNotExist(err) {
		t.Error("an unguarded hook wrote status for a non-atelier session")
	}

	t.Setenv("ATELIER_SESSION", "brave-otter")
	if err := HandleHook("blocked"); err != nil {
		t.Fatal(err)
	}
	if got := Status("brave-otter", true); got != core.StatusBlocked {
		t.Errorf("after hook: %q, want blocked", got)
	}
}

func TestBlockedCount(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	now := time.Now().Unix()
	writeStatus(t, "a", fmt.Sprintf("blocked\n%d\n", now))
	writeStatus(t, "b", fmt.Sprintf("working\n%d\n", now))
	writeStatus(t, "c", fmt.Sprintf("blocked\n%d\n", now))

	if got := BlockedCount([]string{"a", "b", "c", "never-seen"}); got != 2 {
		t.Errorf("BlockedCount = %d, want 2", got)
	}
	if got := BlockedCount(nil); got != 0 {
		t.Errorf("BlockedCount(nil) = %d, want 0", got)
	}
}

// Reviving a dead space must RESUME the conversation, not replay the original
// intent — replaying would re-run the task from the top and discard every turn
// since (the v0 "resume respawned sessions over stale prompt" fix).
func TestResumeCmdContinues(t *testing.T) {
	cmd := ResumeCmd()
	if !strings.Contains(cmd, "claude --continue") {
		t.Errorf("ResumeCmd() = %q, want it to --continue the conversation", cmd)
	}
	if !strings.Contains(cmd, "||") {
		t.Errorf("ResumeCmd() = %q, want a fallback for a space with no conversation yet", cmd)
	}
}

// The intent is user-written prose that tmux runs through a shell: quotes,
// backticks, and semicolons in it must stay data.
func TestLaunchCmdQuotesIntent(t *testing.T) {
	cases := []string{
		"fix the bug",
		"it's broken",
		"rm -rf /; echo pwned",
		"use `git log` and $HOME",
		`say "hello"`,
	}
	for _, intent := range cases {
		cmd := LaunchCmd(intent)
		if !strings.HasPrefix(cmd, "claude '") || !strings.HasSuffix(cmd, "'") {
			t.Errorf("LaunchCmd(%q) = %q, want a single-quoted argument", intent, cmd)
		}
		// Every quote in the body must be the escaped form, so the argument can
		// never be closed early.
		body := strings.TrimSuffix(strings.TrimPrefix(cmd, "claude '"), "'")
		for _, part := range strings.Split(body, `'\''`) {
			if strings.Contains(part, "'") {
				t.Errorf("LaunchCmd(%q) = %q leaves an unescaped quote", intent, cmd)
			}
		}
	}
}

// Only blocked draws attention; gone shows no marker at all (a dead space must
// not paint the status bar).
func TestStatusMarker(t *testing.T) {
	if got := StatusMarker(core.StatusGone); got != "" {
		t.Errorf("gone marker = %q, want empty", got)
	}
	for _, st := range []core.AgentStatus{core.StatusBlocked, core.StatusWorking, core.StatusIdle} {
		if StatusMarker(st) == "" {
			t.Errorf("%q has no marker", st)
		}
	}
	if !strings.Contains(StatusMarker(core.StatusBlocked), "●") {
		t.Error("blocked marker lost its attention glyph")
	}
}
