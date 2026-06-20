//go:build e2e

package popupshell_test

import (
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/tools/popupshell"
)

func TestCreate_CreatesBackingSession(t *testing.T) {
	srv := testtmux.New(t)

	if err := popupshell.Create(srv.Client, "$0", "@1"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	srv.MustHaveSession(popupshell.Name("$0", "@1"))
}

func TestCreate_Idempotent(t *testing.T) {
	srv := testtmux.New(t)

	if err := popupshell.Create(srv.Client, "$0", "@1"); err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	if err := popupshell.Create(srv.Client, "$0", "@1"); err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	want := popupshell.Name("$0", "@1")
	hits := 0
	for _, s := range srv.Sessions() {
		if s == want {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("expected exactly one %q, got %d (sessions=%v)", want, hits, srv.Sessions())
	}
}

func TestCLI_CreateThroughDispatcher(t *testing.T) {
	srv := testtmux.New(t)

	// Go through the core's `tools` dispatcher; verifies plugin discovery
	// finds atelier-popupshell and routes the args correctly.
	out, err := srv.RunAtelier(
		"tools", "popupshell", "create",
		"--socket", srv.Socket,
		"--session", "$0",
		"--window", "@1",
	)
	if err != nil {
		t.Fatalf("RunAtelier: %v\noutput:\n%s", err, out)
	}
	srv.MustHaveSession(popupshell.Name("$0", "@1"))
}

func TestCLI_CreateDirectBinary(t *testing.T) {
	srv := testtmux.New(t)

	// Skip the dispatcher entirely, call atelier-popupshell directly.
	out, err := srv.RunTool("popupshell", "create",
		"--socket", srv.Socket,
		"--session", "$2",
		"--window", "@5",
	)
	if err != nil {
		t.Fatalf("RunTool: %v\noutput:\n%s", err, out)
	}
	srv.MustHaveSession(popupshell.Name("$2", "@5"))
}
