package workspaces

import (
	"os"
	"strings"
	"testing"
)

// TestSessionsPicker_RoutesThroughLandOuter locks in the fix for
// "workspace opens INSIDE the Claude popup". The sessions picker may
// run via toolselector exec-in-place — its process lives in the
// selector's popup pty. A bare switch-client targets the popup-client,
// which causes the picked workspace to render inside the popup.
//
// Post-Layer-B, the implementation moved to internal/workspace.LandOuter.
// This source-inspection test verifies the picker still calls the
// primitive (no inline switch-client snuck back in) and the primitive
// still reads @atelier_outer_client and passes -c outer.
func TestSessionsPicker_RoutesThroughLandOuter(t *testing.T) {
	src, err := os.ReadFile("workspaces.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	s := string(src)

	// The sessions picker must call workspace.LandOuter (or some other
	// caller routed through it).
	if !strings.Contains(s, `workspace.LandOuter(h, "="+row.Session, "="+row.Session+":"+row.Window)`) {
		t.Errorf("sessions picker no longer calls workspace.LandOuter — risks regressing the M-s → opens-inside-popup bug")
	}

	// Workspaces tool must not have its OWN switchOuterTo or local helper.
	if strings.Contains(s, "func switchOuterTo(") {
		t.Errorf("workspaces.go still defines switchOuterTo locally; should be deleted in favor of workspace.LandOuter")
	}

	// Verify the helper itself still reads @atelier_outer_client.
	lifecycle, err := os.ReadFile("../../workspace/lifecycle.go")
	if err != nil {
		t.Fatalf("read workspace/lifecycle.go: %v", err)
	}
	ls := string(lifecycle)
	if !strings.Contains(ls, `ShowGlobalOption("@atelier_outer_client")`) {
		t.Errorf("workspace.LandOuter no longer reads @atelier_outer_client")
	}
	if !strings.Contains(ls, `"-c", outerClient`) {
		t.Errorf("workspace.LandOuter no longer passes -c <outer> to switch-client")
	}
}

// TestNoInlineSwitchClientInWorkspaces locks in: every switch-client in
// the workspaces TOOL must go through workspace.LandOuter. A new direct
// `h.Run("switch-client", ...)` call would risk reintroducing the
// "workspace opens inside the popup" bug.
func TestNoInlineSwitchClientInWorkspaces(t *testing.T) {
	src, err := os.ReadFile("workspaces.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if n := strings.Count(string(src), `h.Run("switch-client"`); n != 0 {
		t.Errorf("found %d inline `h.Run(\"switch-client\"...)` call(s) in workspaces.go; "+
			"all switch-client must go through workspace.LandOuter so the outer client is "+
			"targeted correctly under exec-in-place dispatch", n)
	}
}

// TestCreator_DropsDefaultBranchWindow_OnFreshSession locks in the
// "don't show main if user only created a non-default" fix.
// Post-Layer-B the kill-window logic lives inside
// workspace.CreateWorktreeWindow gated on spec.KillDefaultBranch. Each
// creator flow sets that field only when ensureSession returned
// created=true. Source-inspection: the helper still honors the field,
// and the creator flows still set it on first-create.
func TestCreator_DropsDefaultBranchWindow_OnFreshSession(t *testing.T) {
	lifecycle, err := os.ReadFile("../../workspace/lifecycle.go")
	if err != nil {
		t.Fatalf("read workspace/lifecycle.go: %v", err)
	}
	ls := string(lifecycle)
	if !strings.Contains(ls, "spec.KillDefaultBranch") {
		t.Errorf("workspace.CreateWorktreeWindow no longer honors KillDefaultBranch — the user's 'I only created non-default' contract is broken")
	}
	if !strings.Contains(ls, `"kill-window", "-t", "="+spec.Session+":"+spec.KillDefaultBranch`) {
		t.Errorf("workspace.CreateWorktreeWindow no longer kill-window's the named default branch")
	}

	src, err := os.ReadFile("workspaces.go")
	if err != nil {
		t.Fatalf("read workspaces.go: %v", err)
	}
	s := string(src)
	// Both creator flows must set KillDefaultBranch when ensureSession
	// returned created=true. Count the conditional sites.
	n := strings.Count(s, "spec.KillDefaultBranch = defaultBranch")
	if n < 2 {
		t.Errorf("expected at least 2 creator flows to set KillDefaultBranch on fresh-session "+
			"(manual-name + auto-name), found %d", n)
	}
}
