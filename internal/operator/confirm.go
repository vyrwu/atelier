package operator

import (
	"errors"
	"os"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

var ErrAway = errors.New("the operator isn't looking: ask them to open Maître (M-m) and retry")
var ErrDeclined = errors.New("the operator said no (or the confirmation timed out)")

const confirmTimeout = 60 * time.Second

type Asker func(client, prompt string, timeout time.Duration) (bool, error)

func FromMaitre() bool { return os.Getenv("ATELIER_SESSION") == core.MaitreSlug }

func confirmWith(prompt string, clients func() ([]string, error), ask Asker) error {
	attached, err := clients()
	if err != nil {
		return err
	}
	if len(attached) == 0 {
		return ErrAway
	}
	yes, err := ask(attached[0], prompt, confirmTimeout)
	if err != nil {
		return err
	}
	if !yes {
		return ErrDeclined
	}
	return nil
}

func ConfirmDestroy(prompt string) error {
	_ = agent.SetStatus(core.MaitreSlug, core.StatusBlocked)
	agent.RefreshAttention()
	defer func() { _ = agent.SetStatus(core.MaitreSlug, core.StatusWorking); agent.RefreshAttention() }()
	return confirmWith(prompt, func() ([]string, error) { return tmux.SessionClients(core.MaitreSlug) }, tmux.ConfirmMenu)
}
