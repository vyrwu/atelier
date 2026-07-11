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
		// popup-table `bind -T popup "M-q" ...` doesn't reach fzf
		// because `display-popup -E` hands raw stdin to the spawned
		// process — tmux only intercepts a few specific keys for the
		// popup table. Without this fzf-level bind, M-q inside any
		// picker (M-s sessions, M-; tool selector, etc.) is a no-op.
		//
		// FR-5.3: delegate to `atelier server quit` rather than
		// `kill-server` so background agents survive. The command
		// detaches the OUTER client (read from @atelier_outer_client),
		// which closes the popup naturally and exits atelier.
		"--bind=alt-q:execute-silent(atelier server quit)",
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

// WithNth sets which fields are DISPLAYED (--with-nth, e.g. "3", "3,4").
// Note fzf renumbers fields for WithSearchNth relative to this projection.
func WithNth(spec string) Opt {
	return func(args []string) []string {
		return append(args, "--with-nth="+spec)
	}
}

// WithSearchNth restricts the SEARCH scope to specific fields (--nth). The
// indices count fields of the --with-nth projection (fzf renumbers), so with
// WithNth("3,4") a WithSearchNth("1") searches only the first displayed field.
func WithSearchNth(spec string) Opt {
	return func(args []string) []string {
		return append(args, "--nth="+spec)
	}
}

// WithWrap wraps long items across multiple rows instead of truncating them
// with an ellipsis (fzf ≥ 0.53). Continuation rows are marked with the fzf
// wrap sign.
func WithWrap() Opt {
	return func(args []string) []string {
		return append(args, "--wrap")
	}
}

// WithHighlightLine extends the current-line highlight (the bg+ color) across
// the full width of the window instead of only the item's content, spanning
// every row of a multi-line item (fzf ≥ 0.53).
func WithHighlightLine() Opt {
	return func(args []string) []string {
		return append(args, "--highlight-line")
	}
}

// WithWrapSign sets the string shown at the start of each wrapped
// continuation row (fzf ≥ 0.53). Pass a run of spaces to indent wrapped text
// into a hanging indent instead of showing a marker.
func WithWrapSign(sign string) Opt {
	return func(args []string) []string {
		return append(args, "--wrap-sign="+sign)
	}
}

// WithTabstop sets the rendered width of a tab character. A picker that packs
// two --with-nth fields onto one display line uses --tabstop=1 so the tab fzf
// inserts between them collapses to a single space instead of snapping to the
// next 8-column tab stop.
func WithTabstop(n int) Opt {
	return func(args []string) []string {
		return append(args, fmt.Sprintf("--tabstop=%d", n))
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

// WithReadZero reads NUL-separated input records instead of newline-separated
// ones. Required for multi-line items: a single selectable entry whose display
// text contains newlines. Pair with WithPrintZero so the selection reads back
// intact. (fzf ≥ 0.53 renders items with embedded newlines across rows.)
func WithReadZero() Opt {
	return func(args []string) []string {
		return append(args, "--read0")
	}
}

// WithPrintZero delimits fzf's output with NUL instead of newline, so a
// multi-line selection survives round-trip. Pair with WithReadZero.
func WithPrintZero() Opt {
	return func(args []string) []string {
		return append(args, "--print0")
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
