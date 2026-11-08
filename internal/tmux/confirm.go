package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func SessionClients(session string) ([]string, error) {
	out, err := Run("list-clients", "-t", "="+session, "-F", "#{client_name}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// ConfirmMenu uses unique, server-local signals, never agent input. A late yes
// after the deadline cannot authorize another request. Both waiters are released
// on every exit so the server retains no blocked command queues.
func ConfirmMenu(client, prompt string, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	channel := fmt.Sprintf("atelier-confirm-%d-%d", os.Getpid(), time.Now().UnixNano())
	type answer struct {
		yes bool
		err error
	}
	answers := make(chan answer, 2)
	for _, yes := range []bool{false, true} {
		suffix := "-n"
		if yes {
			suffix = "-y"
		}
		go func() {
			// #nosec G204 G702 -- Fixed tmux binary; generated channel and socket are separate arguments.
			err := exec.CommandContext(ctx, "tmux", "-L", socket(), "wait-for", channel+suffix).Run()
			answers <- answer{yes: yes, err: err}
		}()
	}
	defer func() {
		_, _ = Run("wait-for", "-S", channel+"-y")
		_, _ = Run("wait-for", "-S", channel+"-n")
	}()
	_, err := Run("display-menu", "-c", client, "-T", EscapeText(prompt), "Yes", "y", "wait-for -S "+channel+"-y", "No", "n", "wait-for -S "+channel+"-n")
	if err != nil {
		return false, err
	}
	select {
	case <-ctx.Done():
		return false, nil
	case a := <-answers:
		if ctx.Err() != nil {
			return false, nil
		}
		return a.yes, a.err
	}
}
