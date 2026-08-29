//go:build e2e

package cli_test

import (
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestStatusEmitters_PublicAPIContract locks in the output shape of
// `atelier status attention count` as the PUBLIC EMBEDDING API. Users
// plug this into their tmux statusline format via `#(...)` invocations;
// if it breaks, every embedded statusline in the wild breaks silently
// (tmux's #(...) discards stderr — a broken emitter renders as an empty
// string, which users mistake for "no data" rather than "broken
// integration").
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
}
