package main

import (
	"github.com/vyrwu/atelier/internal/core"
	"strings"
	"testing"
)

func TestMaitreAgent(t *testing.T) {
	for _, tc := range []struct {
		cfg  core.Config
		want string
	}{{core.Config{MaitreAgent: "codex"}, "codex"}, {core.Config{Agent: "grok"}, "grok"}, {core.Config{MaitreAgent: "bogus", Agent: "codex"}, "codex"}, {core.Config{}, "claude"}} {
		if got := maitreAgent(tc.cfg); got != tc.want {
			t.Errorf("%+v: %s", tc.cfg, got)
		}
	}
}

func TestFragmentBindsMaitre(t *testing.T) {
	frag, _ := bindingsFragment()
	for _, want := range []string{`bind -n M-m set-option -gF @atelier_maitre_outer`, `bind -T maitre M-m detach-client`, `bind -T maitre M-r run-shell`, `maitre --restart`, `bind -T maitre M-x set-option`} {
		if !strings.Contains(frag, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(frag, "bind -n M-r ") {
		t.Fatal("M-r captured outside Maître")
	}
}

func TestMaitreRestartRespectsHandoff(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	token, releaseLaunch, err := reserveSwap(core.MaitreSlug)
	if err != nil || token == "" {
		t.Fatal(token, err)
	}
	releaseLaunch()
	check := func() {
		t.Helper()
		if err := prepareMaitre(true); err == nil || !strings.Contains(err.Error(), "already swapping") {
			t.Fatalf("restart did not reject the swap: %v", err)
		}
		if core.Load().Find(core.MaitreSlug) != nil {
			t.Fatal("restart touched state before checking the handoff")
		}
	}
	check() // pending launcher, before its child takes the lock
	releaseHandoff, err := takeSwap(core.MaitreSlug, token)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseHandoff()
	check() // active handoff, after consuming the reservation
}
