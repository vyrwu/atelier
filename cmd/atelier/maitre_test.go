package main

import (
	"github.com/vyrwu/atelier/internal/core"
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
