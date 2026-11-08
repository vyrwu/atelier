package operator

import (
	"fmt"
	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

const OuterOption = "@atelier_maitre_outer"

func Focus(slug string) error {
	w, err := find(slug, wantActive)
	if err != nil {
		return err
	}
	outer := tmux.GlobalOption(OuterOption)
	if outer == "" || tmux.ClientSession(outer) == "" {
		return fmt.Errorf("no client to move; open Maître with M-m")
	}
	if err := agent.EnsureAgent(w); err != nil {
		return err
	}
	// A revived session has no display options yet; restore the cached title
	// and status even when no new hook fires on resume.
	agent.RefreshTitle(w.Session)
	if err := tmux.SelectWindow(w.Session, agent.Window); err != nil {
		return err
	}
	if err := tmux.SwitchClient(outer, w.Session); err != nil {
		return err
	}
	status := agent.Status(w.Session, true)
	if status == core.StatusBlocked {
		_ = agent.SetStatus(w.Session, core.StatusIdle)
		status = core.StatusIdle
		agent.RefreshAttention()
	}
	_ = tmux.SetStatusMarker(w.Session, agent.StatusMarker(status))
	return tmux.DetachSession(core.MaitreSlug)
}
