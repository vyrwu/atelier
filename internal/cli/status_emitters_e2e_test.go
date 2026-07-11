//go:build e2e

package cli_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestStatusEmitters_PublicAPIContract locks in the output shape of
// `atelier status freshness` and `atelier status attention count` as
// the PUBLIC EMBEDDING API. Users plug these into their tmux
// statusline format via `#(...)` invocations; if these break, every
// embedded statusline in the wild breaks silently (tmux's #(...)
// discards stderr — broken emitters render as empty strings, which
// users mistake for "no data" rather than "broken integration").
//
// Specifically guards against the bug found in the v0.1.0 audit
// where the attention subcommand was named `--count` (with leading
// dashes), making it unreachable through cobra's parser. Every
// `atelier status attention --count` invocation errored "unknown
// flag" and produced no output, silently breaking the rollup for
// every user.
func TestStatusEmitters_PublicAPIContract(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("seed")

	t.Run("freshness: empty repo path → empty output", func(t *testing.T) {
		out, err := srv.RunAtelier("status", "freshness", "", "", "", "", "")
		if err != nil {
			t.Fatalf("freshness invocation errored: %v\n%s", err, out)
		}
		if len(strings.TrimSpace(string(out))) != 0 {
			t.Errorf("expected empty output for empty repo_path; got %q", string(out))
		}
	})

	t.Run("freshness: in-sync renders green checkmark", func(t *testing.T) {
		out, err := srv.RunAtelier("status", "freshness",
			"0", "0", "", "1729094400", "/fake/repo")
		if err != nil {
			t.Fatalf("invocation errored: %v\n%s", err, out)
		}
		s := string(out)
		if !strings.Contains(s, "fg=green") || !strings.Contains(s, "✔") {
			t.Errorf("expected green ✔; got %q", s)
		}
	})

	t.Run("freshness: behind renders red ↓N", func(t *testing.T) {
		out, err := srv.RunAtelier("status", "freshness",
			"3", "0", "", "1729094400", "/fake/repo")
		if err != nil {
			t.Fatalf("invocation errored: %v\n%s", err, out)
		}
		s := string(out)
		if !strings.Contains(s, "fg=red") || !strings.Contains(s, "↓3") {
			t.Errorf("expected red ↓3; got %q", s)
		}
	})

	t.Run("freshness: ahead renders yellow ↑N", func(t *testing.T) {
		out, err := srv.RunAtelier("status", "freshness",
			"0", "2", "", "1729094400", "/fake/repo")
		if err != nil {
			t.Fatalf("invocation errored: %v\n%s", err, out)
		}
		s := string(out)
		if !strings.Contains(s, "fg=yellow") || !strings.Contains(s, "↑2") {
			t.Errorf("expected yellow ↑2; got %q", s)
		}
	})

	t.Run("freshness: pull error renders red ⚠ with message", func(t *testing.T) {
		out, err := srv.RunAtelier("status", "freshness",
			"0", "0", "fetch failed", "", "/fake/repo")
		if err != nil {
			t.Fatalf("invocation errored: %v\n%s", err, out)
		}
		s := string(out)
		if !strings.Contains(s, "fg=red") ||
			!strings.Contains(s, "⚠") ||
			!strings.Contains(s, "fetch failed") {
			t.Errorf("expected red ⚠ with msg; got %q", s)
		}
	})

	t.Run("attention: count subcommand is reachable", func(t *testing.T) {
		// The actual bug: invoking the rollup must SUCCEED. Output
		// content depends on tmux state (which @needs_attention=1
		// windows exist). We don't assert content here — just that
		// the command parses and exits 0.
		out, err := srv.RunAtelier("--socket", srv.Socket,
			"status", "attention", "count")
		if err != nil {
			t.Fatalf("attention count invocation errored — public API broken: %v\n%s",
				err, out)
		}
	})

	t.Run("forge: open state renders colored PR glyph", func(t *testing.T) {
		out, err := srv.RunAtelier("status", "forge", "open")
		if err != nil {
			t.Fatalf("forge invocation errored: %v\n%s", err, out)
		}
		s := string(out)
		glyph, color, _ := integration.ForgeGlyph(integration.ForgeOpen)
		if !strings.Contains(s, "fg=colour"+color) || !strings.Contains(s, glyph) {
			t.Errorf("expected forge open badge (colour%s + glyph); got %q", color, s)
		}
	})

	t.Run("forge: no forge item → empty output", func(t *testing.T) {
		out, err := srv.RunAtelier("status", "forge", "")
		if err != nil {
			t.Fatalf("forge invocation errored: %v\n%s", err, out)
		}
		if len(strings.TrimSpace(string(out))) != 0 {
			t.Errorf("expected empty output for empty @forge_state; got %q", string(out))
		}
	})
}
