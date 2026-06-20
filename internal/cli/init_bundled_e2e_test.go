//go:build e2e

package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestInit_SourcesCleanlyOnFreshTmux is the load-bearing regression
// guard for the bundled launcher. It captures `atelier init` output,
// writes it to a temp file, sources it into a fresh tmux server,
// and asserts tmux's stderr is empty.
//
// The specific bug this caught: `unbind -T popup ...` errors with
// "table popup doesn't exist" when run before any `bind -T popup ...`
// has created the table. Plugin-mode users never hit this because
// their host tmux already has popup binds. Bundled mode is the FIRST
// thing tmux sees, so atelier must create the table itself (via the
// PopupTableShim emitted at the top of every init).
//
// Without this test, the bundled launcher silently produced a broken
// statusline on first `atelier` invocation. With it, any future
// init-generation regression that produces a tmux parse error fails
// CI before it lands.
func TestInit_SourcesCleanlyOnFreshTmux(t *testing.T) {
	for _, mode := range []struct {
		name string
		args []string
	}{
		{"engine + theme (default)", []string{"init"}},
		{"engine only (--bare)", []string{"init", "--bare"}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			srv := testtmux.New(t)
			// testtmux defers server startup until a client command;
			// seed a session so the server exists before source-file.
			srv.NewSession("seed")

			// Generate the init config by invoking atelier directly.
			out, err := srv.RunAtelier(mode.args...)
			if err != nil {
				t.Fatalf("atelier %v: %v\n%s", mode.args, err, out)
			}
			if len(out) == 0 {
				t.Fatal("atelier init produced empty output")
			}

			// Write to a temp file so we can `source-file` it.
			confPath := filepath.Join(t.TempDir(), "atelier.conf")
			if err := os.WriteFile(confPath, out, 0o600); err != nil {
				t.Fatalf("write conf: %v", err)
			}

			// Source the config into the running test tmux server.
			// tmux exits non-zero AND prints to stderr on any parse
			// error; we check both. `tmux source-file` won't fully
			// abort on a single bad line — it continues — so we MUST
			// inspect stderr for "table doesn't exist" / "unknown
			// command" / similar reports.
			sourceCmd := exec.Command("tmux", "-L", srv.Socket,
				"source-file", confPath)
			sourceOut, sourceErr := sourceCmd.CombinedOutput()
			combined := string(sourceOut)
			if sourceErr != nil {
				t.Errorf("tmux source-file exit error: %v\noutput:\n%s",
					sourceErr, combined)
			}

			// These are the specific failure signatures we want to
			// catch. Any of them indicates a generation regression.
			for _, badPhrase := range []string{
				"doesn't exist",
				"unknown command",
				"syntax error",
				"can't find",
				"bad ",
			} {
				if strings.Contains(combined, badPhrase) {
					t.Errorf("tmux reported %q in source-file output:\n%s",
						badPhrase, combined)
				}
			}
		})
	}
}
