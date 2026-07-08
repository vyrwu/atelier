//go:build e2e

package cli

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestCheckEscapeTime_High flags a config that sets escape-time
// above the 50ms threshold. Older tmux configs (pre-3.3) and
// inherited dotfiles often carry `set -g escape-time 500` from
// when that was the recommended fix for vim's Esc — modern tmux
// no longer needs that. Doctor surfaces it so the user knows to
// drop the workaround.
func TestCheckEscapeTime_High(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("seed")
	if _, err := srv.Client.Run("set-option", "-g", "escape-time", "500"); err != nil {
		t.Fatalf("set escape-time: %v", err)
	}

	r := checkEscapeTime(srv.Client)
	if r.Status != StatusWarn {
		t.Errorf("500ms escape-time should WARN, got %s (%q)", r.Status, r.Detail)
	}
	if r.Remediation == "" {
		t.Errorf("WARN must carry remediation (the user needs to know how to fix it)")
	}
}

// TestCheckEscapeTime_Low: configured ≤50ms → PASS, no noise on
// every doctor run.
func TestCheckEscapeTime_Low(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("seed")
	if _, err := srv.Client.Run("set-option", "-g", "escape-time", "10"); err != nil {
		t.Fatalf("set escape-time: %v", err)
	}

	r := checkEscapeTime(srv.Client)
	if r.Status != StatusPass {
		t.Errorf("10ms escape-time should PASS, got %s (%q)", r.Status, r.Detail)
	}
}

// TestCheckStatuslineFormat_NoAtelier locks the silent-breakage
// case: a vanilla tmux config has no atelier segments at all.
// In plugin mode the user might assume statusline injection ran
// and not notice the icons are missing. Doctor must FAIL loudly.
func TestCheckStatuslineFormat_NoAtelier(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("seed")
	// Default tmux window-status-format has neither freshness nor
	// attention segments.

	r := checkStatuslineFormat(srv.Client)
	if r.Status != StatusFail {
		t.Errorf("vanilla statusline should FAIL, got %s (%q)", r.Status, r.Detail)
	}
}

// TestCheckStatuslineFormat_BothPresent: when both segments are
// injected (the post-stamp-statusline state), PASS.
func TestCheckStatuslineFormat_BothPresent(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("seed")
	fmt := "#W " +
		"#(atelier status freshness '#{@workspace_behind}' '#{@workspace_ahead}' '#{@workspace_pull_error}' '#{@workspace_freshness_ts}' '#{@repo_path}')" +
		"#(atelier status attention count)"
	if _, err := srv.Client.Run("set-option", "-g", "window-status-format", fmt); err != nil {
		t.Fatalf("set window-status-format: %v", err)
	}

	r := checkStatuslineFormat(srv.Client)
	if r.Status != StatusPass {
		t.Errorf("both segments present should PASS, got %s (%q)", r.Status, r.Detail)
	}
}

// TestCheckStatuslineFormat_PartialInjection: only one of the two
// segments survived (e.g. user's tmux.conf overwrote half). Doctor
// must WARN with a hint about which side is missing.
func TestCheckStatuslineFormat_PartialInjection(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("seed")
	// Only freshness, no attention.
	if _, err := srv.Client.Run("set-option", "-g", "window-status-format",
		"#W #(atelier status freshness 'a' 'b' 'c' 'd' 'e')"); err != nil {
		t.Fatalf("set window-status-format: %v", err)
	}

	r := checkStatuslineFormat(srv.Client)
	if r.Status != StatusWarn {
		t.Errorf("partial statusline should WARN, got %s (%q)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "attention") {
		t.Errorf("WARN must name the missing segment; got %q", r.Detail)
	}
}
