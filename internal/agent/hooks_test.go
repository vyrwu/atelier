package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The status the overlay shows is only ever as right as this map. #91 in v0 was
// exactly this table being wrong — a finished turn reported as still running.
func TestAtelierHooksMapping(t *testing.T) {
	want := map[string]string{
		"UserPromptSubmit": "working",
		"SessionStart":     "idle", // also fires on --continue, /clear, compaction: at the prompt, not working
		"PreToolUse":       "working",
		"PostToolUse":      "working",
		"Notification":     "blocked", // Claude needs the user
		"Stop":             "idle",    // the turn finished — idle, not working
	}
	got := atelierHooks()
	if len(got) != len(want) {
		t.Fatalf("hook events = %v, want %v", got, want)
	}
	for event, status := range want {
		if got[event].status != status {
			t.Errorf("%s → %q, want %q", event, got[event].status, status)
		}
	}
}

// Only the notifications where Claude is stopped on you may mark an agent
// blocked. Claude also sends an idle reminder a minute after every finished
// turn; unmatched, that turned every idle agent blocked and lit the badge.
func TestNotificationMatchesOnlyWhatNeedsYou(t *testing.T) {
	m := atelierHooks()["Notification"].matcher
	for _, want := range []string{"permission_prompt", "elicitation_dialog", "agent_needs_input"} {
		if !strings.Contains(m, want) {
			t.Errorf("matcher %q misses %q", m, want)
		}
	}
	if strings.Contains(m, "idle_prompt") {
		t.Errorf("matcher %q includes the idle reminder", m)
	}
	for event, h := range atelierHooks() {
		if event != "Notification" && h.matcher != "" {
			t.Errorf("%s has a matcher %q; only Notification should", event, h.matcher)
		}
	}
}

// The matcher is written into the hook group, where Claude reads it.
func TestMergeEventHookWritesTheMatcher(t *testing.T) {
	groups := mergeEventHook(nil, hookCommand("blocked"), needsYou)
	g, ok := groups[0].(map[string]any)
	if !ok || g["matcher"] != needsYou {
		t.Errorf("group = %+v, want matcher %q", groups[0], needsYou)
	}
	if _, has := mergeEventHook(nil, hookCommand("idle"), "")[0].(map[string]any)["matcher"]; has {
		t.Error("an unmatched hook was written with a matcher key")
	}
}

// The hook lands in the user's global settings and fires for their own Claude
// sessions too, so it must be a no-op without $ATELIER_SESSION and must never
// fail a turn.
func TestHookCommandIsGuarded(t *testing.T) {
	cmd := hookCommand("working")
	if !strings.Contains(cmd, `[ -n "$ATELIER_SESSION" ]`) {
		t.Errorf("hookCommand = %q, want it guarded on $ATELIER_SESSION", cmd)
	}
	if !strings.HasSuffix(cmd, "|| true") {
		t.Errorf("hookCommand = %q, want it to always succeed", cmd)
	}
	if !strings.Contains(cmd, "atelier hook working") {
		t.Errorf("hookCommand = %q, want it to record the status", cmd)
	}
}

// Claude honours $CLAUDE_CONFIG_DIR and relocates BOTH files into it, flat.
// Writing to the legacy path when it is set is a silent no-op — hooks that never
// fire, so every space looks idle forever.
func TestClaudeConfigPaths(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/claude")
	if got, want := settingsPath(), "/custom/claude/settings.json"; got != want {
		t.Errorf("settingsPath() = %q, want %q", got, want)
	}
	if got, want := claudeConfigJSON(), "/custom/claude/.claude.json"; got != want {
		t.Errorf("claudeConfigJSON() = %q, want %q", got, want)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got, want := settingsPath(), filepath.Join(home, ".claude", "settings.json"); got != want {
		t.Errorf("legacy settingsPath() = %q, want %q", got, want)
	}
	if got, want := claudeConfigJSON(), filepath.Join(home, ".claude.json"); got != want {
		t.Errorf("legacy claudeConfigJSON() = %q, want %q", got, want)
	}
}

// mergeEventHook edits a file the user owns. It must add atelier's hook exactly
// once, keep every hook that isn't atelier's, and drop a stale atelier hook
// rather than stacking a second copy on every install.
func TestMergeEventHook(t *testing.T) {
	cmd := hookCommand("working")
	userGroup := map[string]any{
		"matcher": "Bash",
		"hooks":   []any{map[string]any{"type": "command", "command": "my-own-linter"}},
	}
	staleGroup := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": `[ -n "$ATELIER_SESSION" ] && atelier hook old-status || true`}},
	}

	got := mergeEventHook([]any{userGroup, staleGroup}, cmd, "")
	if len(got) != 2 {
		t.Fatalf("groups = %d, want the user's one plus atelier's one", len(got))
	}
	if !reflectDeepEqualish(got[0], userGroup) {
		t.Errorf("user's hook group was not preserved: %+v", got[0])
	}
	if countAtelierCommands(got) != 1 {
		t.Errorf("atelier commands = %d, want exactly one", countAtelierCommands(got))
	}
	if !strings.Contains(flatten(got), jsonBody(cmd)) {
		t.Errorf("current atelier command missing from %+v", got)
	}

	// Re-running install is idempotent — the second pass must not grow the list.
	again := mergeEventHook(got, cmd, "")
	if len(again) != len(got) || countAtelierCommands(again) != 1 {
		t.Errorf("not idempotent: %d groups, %d atelier commands", len(again), countAtelierCommands(again))
	}

	// No prior hooks for the event at all (nil, or a value of the wrong shape).
	for name, existing := range map[string]any{"nil": nil, "wrong-shape": "garbage"} {
		if g := mergeEventHook(existing, cmd, ""); len(g) != 1 || countAtelierCommands(g) != 1 {
			t.Errorf("%s existing: got %+v", name, g)
		}
	}
}

func TestGroupHasAtelierHook(t *testing.T) {
	cases := map[string]struct {
		group any
		want  bool
	}{
		"atelier":     {map[string]any{"hooks": []any{map[string]any{"command": "atelier hook idle"}}}, true},
		"user":        {map[string]any{"hooks": []any{map[string]any{"command": "atelier-other-tool"}}}, false},
		"empty-hooks": {map[string]any{"hooks": []any{}}, false},
		"no-hooks":    {map[string]any{"matcher": "Bash"}, false},
		"not-a-map":   {"garbage", false},
		"nil":         {nil, false},
	}
	for name, c := range cases {
		if got := groupHasAtelierHook(c.group); got != c.want {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

// InstallHooks rewrites the user's settings.json. Anything already in that file
// — their hooks, their unrelated settings — must survive untouched.
func TestInstallHooksPreservesSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")

	existing := `{
	  "model": "opus",
	  "permissions": {"allow": ["Bash(ls:*)"]},
	  "hooks": {
	    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "my-own-linter"}]}]
	  }
	}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := InstallHooks(); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("settings.json is no longer valid JSON: %v", err)
	}
	if got["model"] != "opus" {
		t.Errorf("unrelated setting lost: %+v", got["model"])
	}
	if got["permissions"] == nil {
		t.Error("permissions block lost")
	}

	hooks, ok := got["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks = %+v", got["hooks"])
	}
	for event := range atelierHooks() {
		groups, ok := hooks[event].([]any)
		if !ok || countAtelierCommands(groups) != 1 {
			t.Errorf("%s: got %+v, want exactly one atelier hook", event, hooks[event])
		}
	}
	if !strings.Contains(flatten(hooks["PreToolUse"].([]any)), "my-own-linter") {
		t.Error("the user's own PreToolUse hook was dropped")
	}

	// Installing twice is how an upgrade lands — it must not accumulate hooks.
	if err := InstallHooks(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var twice map[string]any
	if err := json.Unmarshal(data, &twice); err != nil {
		t.Fatal(err)
	}
	hooks2 := twice["hooks"].(map[string]any)
	for event := range atelierHooks() {
		if n := countAtelierCommands(hooks2[event].([]any)); n != 1 {
			t.Errorf("%s after a second install: %d atelier hooks, want 1", event, n)
		}
	}
}

// A fresh machine has no settings file (and no .claude dir) — install must
// create both rather than fail.
func TestInstallHooksFromNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "nested"))

	if err := InstallHooks(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "nested", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got["hooks"].(map[string]any)) != len(atelierHooks()) {
		t.Errorf("hooks = %+v", got["hooks"])
	}
}

func countAtelierCommands(groups []any) int {
	n := 0
	for _, g := range groups {
		if groupHasAtelierHook(g) {
			n++
		}
	}
	return n
}

// jsonBody is s as it appears inside a marshalled JSON document (quotes escaped).
func jsonBody(s string) string {
	return strings.Trim(flatten(s), `"`)
}

func flatten(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func reflectDeepEqualish(a, b any) bool { return flatten(a) == flatten(b) }

// A dotfiles-managed settings file is a symlink. It is written through, not
// replaced by a plain file — which silently detached it from the dotfiles repo.
func TestWriteConfigWritesThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles-settings.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(link, []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
	if got, _ := os.ReadFile(target); string(got) != `{"x":1}` {
		t.Errorf("target = %q, want the new content", got)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the file's own 0600 kept", fi.Mode().Perm())
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, ".*atelier-*")); len(matches) != 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}

// Spaces created back to back each register themselves in Claude's config. The
// read-modify-write is locked and each writer has its own temp file, so none is
// lost — before, the last writer won and the others had no MCP tools.
func TestPrepareProjectConcurrently(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	const n = 12
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			errs <- PrepareProject(filepath.Join("/spaces", string(rune('a'+i))), "s")
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config corrupted: %v", err)
	}
	if got := len(cfg["projects"].(map[string]any)); got != n {
		t.Errorf("%d of %d spaces registered — updates were lost", got, n)
	}
}
