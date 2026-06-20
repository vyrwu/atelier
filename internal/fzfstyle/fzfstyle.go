// Package fzfstyle is the single source of truth for atelier's fzf
// appearance. All atelier pickers (tool selector, workspace selector,
// k8s contexts picker, etc.) construct their fzf arguments via this
// package so the visual identity stays consistent.
//
// The atelier palette mirrors the bash setup exactly:
//
//	label:103,border:103,footer:103   (gray-violet structural color)
//	hl:-1,hl+:-1                       (preserve terminal default highlight)
//	prompt/pointer/query/hl colored by the caller's accent color
//
// Tools pass an accent color (256-color number or named color) and
// fzfstyle.Args produces the matching fzf flags. Callers compose
// per-picker behavior (--bind, --header, --footer, --print-query, etc.)
// via Opt values.
package fzfstyle

import (
	"fmt"
	"strings"
)

// BaseColors is the structural color portion of the atelier fzf palette,
// constant across every picker.
const BaseColors = "label:103,border:103,footer:103,hl:-1,hl+:-1"

// Args returns the canonical atelier fzf args for a picker.
//
//	prompt       — e.g. "⌘ ", "栽 " (with trailing space)
//	label        — border label text WITHOUT surrounding spaces; Args adds them
//	accentColor  — 256-color number ("172") or named ("red", "green")
//	opts         — composable extensions (header, footer, binds, etc.)
//
// The label is wrapped with single spaces inside (matches bash's
// `--border-label=' Select Tool '`).
func Args(prompt, label, accentColor string, opts ...Opt) []string {
	colorStr := fmt.Sprintf(
		"prompt:%[1]s:bold,pointer:%[1]s,query:%[1]s,%s",
		accentColor, BaseColors)

	args := []string{
		"--prompt=" + prompt,
		"--color=" + colorStr,
		"--info=inline-right",
		"--height=100%",
		"--reverse",
		"--ansi",
		"--border=rounded",
		"--border-label= " + label + " ",
		"--border-label-pos=0",
		// Global M-q quit binding INSIDE every fzf picker. Tmux's
		// popup-table `bind -T popup "M-q" kill-server` doesn't reach
		// fzf because `display-popup -E` hands raw stdin to the
		// spawned process — tmux only intercepts a few specific keys
		// for the popup table. Without this fzf-level bind, M-q
		// inside any picker (M-s sessions, M-; tool selector, etc.)
		// is a no-op. `kill-server` tears down the whole tmux server,
		// fzf included, so no explicit fzf exit is needed.
		"--bind=alt-q:execute-silent(tmux kill-server)",
	}
	for _, o := range opts {
		args = o(args)
	}
	return args
}

// Opt is a composable fzf-arg transformer.
type Opt func([]string) []string

// WithHeader sets the fzf header (shown above the input line).
func WithHeader(s string) Opt {
	return func(args []string) []string {
		return append(args, "--header="+s)
	}
}

// WithFooter sets the footer line.
func WithFooter(s string) Opt {
	return func(args []string) []string {
		return append(args, "--footer="+s)
	}
}

// WithBind adds a key binding (key:action). Multiple WithBind calls are valid.
func WithBind(key, action string) Opt {
	return func(args []string) []string {
		return append(args, "--bind="+key+":"+action)
	}
}

// WithDelimiter sets the field delimiter (typically "\t").
func WithDelimiter(d string) Opt {
	return func(args []string) []string {
		return append(args, "--delimiter="+d)
	}
}

// WithNth sets which fields are searched (e.g. "1", "2..").
func WithNth(spec string) Opt {
	return func(args []string) []string {
		return append(args, "--with-nth="+spec)
	}
}

// WithExpect adds keys that, when pressed, cause fzf to exit and report
// the key name on its first output line.
func WithExpect(keys ...string) Opt {
	return func(args []string) []string {
		return append(args, "--expect="+strings.Join(keys, ","))
	}
}

// WithPrintQuery enables --print-query (typed text echoed on output).
func WithPrintQuery() Opt {
	return func(args []string) []string {
		return append(args, "--print-query")
	}
}

// WithNoClear keeps the screen content visible after fzf exits.
func WithNoClear() Opt {
	return func(args []string) []string {
		return append(args, "--no-clear")
	}
}

// WithQuery pre-fills the search query.
func WithQuery(q string) Opt {
	return func(args []string) []string {
		return append(args, "--query="+q)
	}
}

// WithCustomColor overrides the color string entirely (advanced — for
// pickers that need extra colors beyond the standard palette, e.g. the
// session picker that styles `hl:red,hl+:red:bold` for confirm mode).
func WithCustomColor(c string) Opt {
	return func(args []string) []string {
		// Remove the auto-generated --color= and replace.
		out := make([]string, 0, len(args))
		for _, a := range args {
			if !strings.HasPrefix(a, "--color=") {
				out = append(out, a)
			}
		}
		out = append(out, "--color="+c)
		return out
	}
}

// Icon256 wraps text in a 256-color ANSI escape.
func Icon256(color int, text string) string {
	return fmt.Sprintf("\033[38;5;%dm%s\033[0m", color, text)
}

// Named256 wraps text in a 256-color ANSI escape; color may be a number
// string ("173") or a named color (returns text unwrapped for unknown).
func ColoredText(color, text string) string {
	if color == "" {
		return text
	}
	// If it's a number, use 256-color.
	if isAllDigits(color) {
		return fmt.Sprintf("\033[38;5;%sm%s\033[0m", color, text)
	}
	// Named colors: map to basic ANSI.
	if code, ok := namedColors[color]; ok {
		return fmt.Sprintf("\033[%sm%s\033[0m", code, text)
	}
	return text
}

var namedColors = map[string]string{
	"red":     "31",
	"green":   "32",
	"yellow":  "33",
	"blue":    "34",
	"magenta": "35",
	"cyan":    "36",
	"white":   "37",
	"gray":    "90",
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
