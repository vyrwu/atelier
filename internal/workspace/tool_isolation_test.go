package workspace_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTools_DoNotReimplementWindowManagement enforces the architectural
// rule from CLAUDE.md: code under internal/tools/ must not directly
// invoke tmux verbs that mutate sessions / windows / popup-client state.
// All of that lives in internal/workspace and internal/popup.
//
// Why this is a test, not just a guideline: every tool that opens a
// popup, creates a workspace, lands the outer client, or stamps
// workspace metadata hits the same edge cases. Five tools used to
// copy-paste `applyPopupStyle`; three used to inline the parent-context
// resolver; two used to inline ensureSession. Each duplication
// re-derived the same edge-case bugs. The lint test makes the rule
// mechanically enforceable so it can't quietly erode.
//
// The allowlist below documents currently-known violations that haven't
// been migrated yet — each has a TODO with the target helper. Adding a
// new violation requires either fixing it or extending the allowlist,
// which is a visible PR signal.
func TestTools_DoNotReimplementWindowManagement(t *testing.T) {
	// Verbs that must go through internal/workspace.
	verbs := []string{
		"new-session", "new-window",
		"switch-client", "select-window",
		"kill-session", "kill-window",
		"respawn-pane",
		"rename-window",
	}

	// allowlist documents known violations awaiting migration. Each
	// entry pins a SINGLE site (file:line:verb) — adding any others
	// fails the test. The entries are TODOs, not permanent exceptions.
	allowlist := map[string]string{
		// k8s/pg own their singleton popup sessions and the respawn-pane
		// dance — these are tool-INTERNAL session management, not
		// workspace-lifecycle. Borderline; could lift into popup.SessionGlobal
		// methods (Respawn already exists; ensure-with-env doesn't).
		"internal/tools/k8s/k8s.go:respawn-pane":  "TODO: lift into popup.SessionGlobal (Respawn-style with env)",
		"internal/tools/k8s/k8s.go:new-session":   "TODO: same — k8s creates its own popup session with env vars",
		"internal/tools/pg/pg.go:respawn-pane":    "TODO: same as k8s",
		"internal/tools/pg/pg.go:new-session":     "TODO: same as k8s",
		"internal/tools/aws/aws.go:respawn-pane":  "TODO: aws respawns the CALLER pane under aws-vault; tool-specific, candidate for popup.RespawnCallerPane(target, cmd)",

		// Workspaces-tool remaining sites — Layer B partial migration.
		"internal/tools/workspaces/workspaces.go:new-session":   "TODO: CloneCommand + runAutoSession should use workspace.EnsureSession / workspace.NewMultiRepoSession",
		"internal/tools/workspaces/workspaces.go:new-window":    "TODO: ensureDefaultBranchWindow should call workspace.NewDefaultBranchWindow (extracts the remaining new-window site)",
		"internal/tools/workspaces/workspaces.go:kill-window":   "TODO: DeleteRowCommand should call workspace.DeleteWindow(h, session, window)",
		"internal/tools/workspaces/workspaces.go:kill-session":  "TODO: DeleteRowCommand should call workspace.DeleteSession(h, session)",

		// toolselector's "Shell" dispatch navigates to window :1.
		"internal/tools/toolselector/selector.go:select-window": "TODO: lift to workspace.LandOnWindow(\":1\") or similar; the special case is invocable as navigation",
	}

	root := findRepoRoot(t)
	toolsRoot := filepath.Join(root, "internal", "tools")
	verbRe := regexp.MustCompile(`"(` + strings.Join(verbs, "|") + `)"`)

	got := map[string]bool{}
	var unexpected []string

	_ = filepath.Walk(toolsRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for _, match := range verbRe.FindAllStringSubmatch(string(data), -1) {
			verb := match[1]
			key := rel + ":" + verb
			got[key] = true
			if _, ok := allowlist[key]; !ok {
				unexpected = append(unexpected, key)
			}
		}
		return nil
	})

	if len(unexpected) > 0 {
		t.Errorf("new tmux-verb usage in internal/tools/ not in the allowlist:\n  %s\n\nFix by routing through internal/workspace or internal/popup, OR (if genuinely tool-specific) add to the allowlist with a TODO pointing at the right helper.",
			strings.Join(unique(unexpected), "\n  "))
	}

	// Reverse check: catch stale allowlist entries (the file or verb
	// went away — entry should be removed so the allowlist stays
	// trustworthy as the current violation set).
	for key := range allowlist {
		if !got[key] {
			t.Errorf("stale allowlist entry: %q no longer present in source — drop it from the allowlist", key)
		}
	}
}

// findRepoRoot walks up from the test's working directory to the
// module root (the directory containing go.mod).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for dir := wd; dir != "/"; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
	}
	t.Fatal("repo root not found")
	return ""
}

func unique(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
