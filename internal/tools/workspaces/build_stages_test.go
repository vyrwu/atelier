package workspaces

import (
	"os"
	"strings"
	"testing"
)

// TestBuildClaudeNamedWorkspace_StageOrder locks in FR-2.1: the
// auto-named build flow must emit, in order:
//  1. Inferring branch name…
//  2. Fetching origin/<branch>…   (after Claude returns a valid name)
//  3. Building worktree…           (after fetch succeeds)
//
// We don't run the real Claude / git calls here — we test the stages
// emitted BEFORE the first failure. With prompt="" and no git repo,
// Claude returns invalid (empty) and the flow aborts. That still locks
// in the first stage. To test stages 2-3 without spinning up a real
// repo would require dependency-injection of claudegen/runGit; that's
// future work. For now the AST-scan companion test covers presence.
func TestBuildClaudeNamedWorkspace_FirstStageIsAskingClaude(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes real claude CLI; skip in -short")
	}
	var stages []string
	status := func(label string) { stages = append(stages, label) }
	// Empty prompt → claudegen errors → flow aborts after stage 1.
	_, _, _, _ = buildClaudeNamedWorkspace(status, "", "fake-repo", "/tmp/nonexistent", "main", nil, false)
	if len(stages) == 0 {
		t.Fatal("no stages emitted")
	}
	if stages[0] != "Inferring branch name…" {
		t.Errorf("first stage = %q, want %q", stages[0], "Inferring branch name…")
	}
}

// TestBuildClaudeNamedWorkspace_NilStatusTolerated asserts the helper
// stays usable when no status reporter is wired (defensive — keeps the
// call site safe to use from CLI/test contexts without a popup).
func TestBuildClaudeNamedWorkspace_NilStatusTolerated(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes real claude CLI; skip in -short")
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil status panicked: %v", r)
		}
	}()
	_, _, _, _ = buildClaudeNamedWorkspace(nil, "", "fake-repo", "/tmp/nonexistent", "main", nil, false)
}

// TestBuildStages_AppearInWorkspaces_go uses simple source-search to
// assert the FR-2.1 stage labels are present in workspaces.go. This
// catches accidental string drift / removal at PR-review-grade
// granularity without needing to spin up a real build.
func TestBuildStages_AppearInWorkspaces_go(t *testing.T) {
	src, err := readWorkspacesSource()
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	wantLabels := []string{
		"Inferring branch name…",
		"Asking the AI for a session name…",
		"Fetching origin/",
		"Building worktree…",
		"Stamping tmux options…",
	}
	for _, want := range wantLabels {
		if !strings.Contains(src, want) {
			t.Errorf("workspaces.go missing FR-2.1 stage label %q", want)
		}
	}
}

func readWorkspacesSource() (string, error) {
	b, err := os.ReadFile("workspaces.go")
	return string(b), err
}
