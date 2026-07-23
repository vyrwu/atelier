//go:build e2e

package popup

import (
	"strings"
	"testing"
	"time"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestListClients_HonorsOuterClientGlobal guards the "no tool launches with
// two clients attached" bug: OpenOnOuter opens the tool popup on whatever
// listClients returns as `outer`. With two terminals attached to the same
// workspace session, the old code returned the FIRST client tmux listed —
// often the terminal the user isn't looking at — so every popup opened
// invisibly and tools appeared dead. listClients must prefer the client the
// user actually drove (@atelier_outer_client).
func TestListClients_HonorsOuterClientGlobal(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("ws")
	_ = srv.Attach(t, "ws")
	_ = srv.Attach(t, "ws")
	time.Sleep(300 * time.Millisecond)

	out, err := srv.Client.Run("list-clients", "-F", "#{client_session}|#{client_name}")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	var outers []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		p := strings.SplitN(line, "|", 2)
		if len(p) != 2 || strings.HasPrefix(p[0], "_") {
			continue
		}
		outers = append(outers, p[1])
	}
	if len(outers) < 2 {
		t.Skipf("need >=2 attached workspace clients to exercise the bug, got %v", outers)
	}

	// Choose a non-first client as the user's outer. The naive first-client
	// logic would have returned outers[0]; the global must override it.
	want := outers[len(outers)-1]
	if _, err := srv.Client.Run("set-option", "-g", "@atelier_outer_client", want); err != nil {
		t.Fatalf("set @atelier_outer_client: %v", err)
	}

	outer, _, err := listClients(srv.Client)
	if err != nil {
		t.Fatalf("listClients: %v", err)
	}
	if outer != want {
		t.Errorf("listClients outer = %q, want %q (outers=%v) — popup would open on the wrong client",
			outer, want, outers)
	}
}
