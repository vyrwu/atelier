package operator

import (
	"fmt"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/tmux"
)

const DefaultLines = 60
const pasteSettle = 150 * time.Millisecond

func Read(slug string, lines int) (string, error) {
	if lines < 0 || lines > 100000 {
		return "", fmt.Errorf("lines must be between 0 and 100000")
	}
	w, err := find(slug, wantActive)
	if err != nil {
		return "", err
	}
	screen, err := tmux.CapturePane("="+w.Session+":"+agent.Window, lines)
	return trimScreen(screen), err
}

func trimScreen(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func ValidKey(k string) bool {
	switch k {
	case "Enter", "Escape", "Up", "Down", "Tab", "1", "2", "3", "4", "5", "6", "7", "8", "9", "C-c":
		return true
	}
	return false
}

func Send(slug, text string, keys []string, enter bool) error {
	for _, k := range keys {
		if !ValidKey(k) {
			return fmt.Errorf("unknown key %q; use Enter, Escape, Up, Down, Tab, 1–9 or C-c", k)
		}
	}
	if text == "" && len(keys) == 0 {
		return fmt.Errorf("send needs text or keys")
	}
	w, err := find(slug, wantActive)
	if err != nil {
		return err
	}
	pane := "=" + w.Session + ":" + agent.Window
	if text != "" {
		if err := tmux.SendLiteral(pane, text); err != nil {
			return err
		}
	}
	if len(keys) > 0 {
		if err := tmux.SendKeys(pane, keys...); err != nil {
			return err
		}
	}
	if text != "" && enter {
		time.Sleep(pasteSettle)
		return tmux.SendKeys(pane, "Enter")
	}
	return nil
}
