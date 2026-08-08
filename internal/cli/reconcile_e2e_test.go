//go:build e2e

package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestReconcile_FixKillsOrphanPopup drives the full recovery path end-to-end:
// an orphan popup (parent window gone) is reported on a dry run and killed
// with --fix.
func TestReconcile_FixKillsOrphanPopup(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")

	// Orphan popup: encodes parent $99/@99, which does not exist.
	orphan := "_atelier_claude_99_99"
	if err := srv.Client.NewSession(orphan, true); err != nil {
		t.Fatalf("create orphan popup: %v", err)
	}

	// Dry run reports the orphan as would-fix and does NOT kill it.
	out, err := srv.RunAtelier("reconcile", "--socket", srv.Socket)
	if err != nil {
		t.Fatalf("reconcile dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "orphan_popup") || !strings.Contains(string(out), "would-fix") {
		t.Fatalf("dry-run output missing orphan/would-fix:\n%s", out)
	}
	// Regression guard: `show-hooks -g` lists the UNSET client-detached slot
	// as a bare line on every server; that must NOT surface as a violation.
	if strings.Contains(string(out), "hook_armed_at_rest") {
		t.Fatalf("phantom hook_armed_at_rest on a server with no armed hook:\n%s", out)
	}
	if has, _ := srv.Client.HasSession(orphan); !has {
		t.Fatal("dry run must not kill the orphan")
	}

	// --fix kills it and reports repaired.
	out, err = srv.RunAtelier("reconcile", "--fix", "--socket", srv.Socket)
	if err != nil {
		t.Fatalf("reconcile --fix: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "repaired") {
		t.Fatalf("--fix output missing repaired:\n%s", out)
	}
	if has, _ := srv.Client.HasSession(orphan); has {
		t.Fatal("orphan popup should be killed after --fix")
	}
}

// TestStateShow_JSONReportsOrphan confirms `atelier state show --json` emits
// parseable JSON that flags the orphan popup — the archaeology-replacing view.
func TestStateShow_JSONReportsOrphan(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("work")
	if err := srv.Client.NewSession("_atelier_claude_99_99", true); err != nil {
		t.Fatalf("create orphan popup: %v", err)
	}

	out, err := srv.RunAtelier("state", "show", "--json", "--socket", srv.Socket)
	if err != nil {
		t.Fatalf("state show --json: %v\n%s", err, out)
	}
	var got struct {
		Violations []struct {
			Code string `json:"code"`
		} `json:"violations"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal state show json: %v\n%s", err, out)
	}
	var sawOrphan bool
	for _, v := range got.Violations {
		if v.Code == "orphan_popup" {
			sawOrphan = true
		}
	}
	if !sawOrphan {
		t.Fatalf("state show did not report orphan_popup:\n%s", out)
	}
}
