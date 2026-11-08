package ui

import (
	"fmt"
	"strings"

	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/tmux"
)

// MaitreBorderStyle shares the overlay palette with the native agent viewport.
// tmux owns this border because the agent owns the terminal inside it.
func MaitreBorderStyle() string {
	return fmt.Sprintf("fg=colour%s,bg=default", cFaint)
}

// ConfigureMaitreView reserves a footer outside the agent's pane, so agent
// redraws cannot erase the shortcuts. These are session-local options: ordinary
// spaces retain their status bar. tmux clips the divider and adapts the legend
// to the attached client's width, including after a resize.
func ConfigureMaitreView() error {
	options := [][2]string{
		{"status-position", "bottom"},
		{"status-style", fmt.Sprintf("fg=colour%s,bg=default", cDim)},
		{"status-format[0]", "#[align=left] " + strings.Repeat("─", 1024) + "#[align=right] "},
		{"status-format[1]", "#{?#{e|<:#{client_width},46}, esc close · M-r restart · M-x swap, esc close  ·  M-r restart  ·  M-x swap agent}"},
		{"status", "2"},
	}
	for _, option := range options {
		if err := tmux.SetSessionOption(core.MaitreSlug, option[0], option[1]); err != nil {
			return err
		}
	}
	return nil
}
