//go:build e2e

package cli_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestStampLastSeen_SetsRecentEpochOnSession verifies that the
// stamp-last-seen subcommand writes `@last_seen=<unix-now>` on the
// named session. This is the hook entry point fired by
// client-session-changed when the user leaves a workspace — get this
// wrong and the picker's "last used" timer keeps reading the old
// session_last_attached value and inflates stale-looking rows.
func TestStampLastSeen_SetsRecentEpochOnSession(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("alpha")

	before := time.Now().Unix()
	if out, err := srv.RunAtelier("internal", "stamp-last-seen",
		"--socket", srv.Socket, "alpha"); err != nil {
		t.Fatalf("stamp-last-seen: %v\n%s", err, out)
	}
	after := time.Now().Unix()

	got, err := srv.Client.Run("show-option", "-v", "-t", "alpha", "@last_seen")
	if err != nil {
		t.Fatalf("show-option: %v", err)
	}
	gotStr := strings.TrimRight(string(got), "\n")
	stamped, err := strconv.ParseInt(gotStr, 10, 64)
	if err != nil {
		t.Fatalf("@last_seen not a unix epoch: %q (%v)", gotStr, err)
	}
	if stamped < before || stamped > after {
		t.Errorf("@last_seen = %d, expected in [%d, %d]", stamped, before, after)
	}
}

// TestStampLastSeen_EmptySessionIsNoOp covers the first-attach edge:
// tmux's client-session-changed hook fires with `client_last_session`
// EMPTY on the very first attach (no prior session). The command
// must be a no-op rather than failing — failing here would surface
// as a stream of err lines in atelier's debug log for every fresh
// tmux startup.
func TestStampLastSeen_EmptySessionIsNoOp(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("alpha")

	for _, arg := range []string{"", "   "} {
		if out, err := srv.RunAtelier("internal", "stamp-last-seen",
			"--socket", srv.Socket, arg); err != nil {
			t.Errorf("stamp-last-seen %q: should not error, got: %v\n%s", arg, err, out)
		}
	}
}
