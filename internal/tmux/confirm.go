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

// ConfirmMenu waits on the menu command itself: unlike ordinary tmux commands,
// display-menu remains running until the human answers or dismisses it. Its
// transient option is unique to this request; late selections after timeout are
// ignored because the pending option has been removed.
func ConfirmMenu(ctx context.Context, client, prompt string, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	option := fmt.Sprintf("@atelier_confirm_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err := SetGlobal(option, "pending"); err != nil {
		return false, err
	}
	defer func() { _, _ = Run("set-option", "-gu", option) }()
	choose := func(answer string) string {
		return "if-shell -F '" + EscapeText("#{==:#{"+option+"},pending}") + "' 'set-option -g " + option + " " + answer + "'"
	}
	// #nosec G204 G702 -- Fixed tmux binary, literal prompt argument and generated callback commands; no user text enters a command.
	out, err := exec.CommandContext(ctx, "tmux", "-L", socket(), "display-menu", "-c", client, "-T", EscapeText(prompt), "Yes", "y", choose("yes"), "No", "n", choose("no")).CombinedOutput()
	if ctx.Err() != nil {
		// Killing the command client leaves the human client's overlay up.
		// Close it as well, for cancellation and the confirmation timeout.
		_, _ = Run("display-popup", "-C", "-c", client)
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("confirm menu: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return GlobalOption(option) == "yes", nil
}
