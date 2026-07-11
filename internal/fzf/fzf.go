// Package fzf wraps fzf invocation for atelier's pickers.
//
// Atelier shells out to fzf because re-implementing fuzzy match + a
// terminal-pretty picker in Go would be a lot of code for no gain. Every
// atelier picker is launched inside a tmux popup, so fzf renders in the
// popup's terminal directly.
package fzf

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// NUL is the record separator fzf uses in --read0 / --print0 mode. A
// picker that renders multi-line items (a single selectable entry spanning
// more than one terminal row) MUST feed input and read output NUL-separated,
// because the item text itself contains newlines. Pass --read0 and/or
// --print0 via extraArgs and this package switches framing to match.
const NUL = "\x00"

// Pick presents lines to fzf and returns the selected line.
// Returns ("", ErrCancelled) if the user dismisses fzf without selecting.
func Pick(lines []string, extraArgs ...string) (string, error) {
	res, err := PickWithExpect(lines, nil, extraArgs...)
	if err != nil {
		return "", err
	}
	return res.Selection, nil
}

// Result is the structured outcome of an fzf invocation.
//
// Key is the special key the user pressed (empty for Enter; one of the
// values passed to --expect otherwise). Query is the text the user typed
// into the fzf prompt (only present when --print-query was set).
// Selection is the chosen line (empty if no match).
type Result struct {
	Key       string
	Query     string
	Selection string
}

// PickWithExpect presents lines to fzf with one or more expect keys
// (e.g. ["ctrl-a", "ctrl-t"]). When the user presses one of those keys
// instead of Enter, Result.Key is set accordingly. Combine with
// `--print-query` (passed via extraArgs) to capture the typed query too.
//
// Used by atelier-workspaces to implement the Ctrl-A toggle between
// manual branch-name and Claude-named flows in a single binding.
func PickWithExpect(lines []string, expectKeys []string, extraArgs ...string) (Result, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return Result{}, fmt.Errorf("fzf not on PATH: %w", err)
	}
	args := []string{"--no-multi", "--height=100%", "--reverse"}
	if len(expectKeys) > 0 {
		args = append(args, "--expect="+strings.Join(expectKeys, ","))
	}
	args = append(args, extraArgs...)
	cmd := exec.Command("fzf", args...)
	// Records are newline-separated by default, NUL-separated under --read0
	// (multi-line items). Empty lines slice → empty stdin (matches bash's
	// `fzf ... < /dev/null`); without this, one empty-string item appears as
	// a ghost entry and gets selected on Enter.
	cmd.Stdin = strings.NewReader(joinRecords(lines, recordSep(extraArgs)))
	// --print0 makes fzf delimit all output tokens (query, expect key,
	// selection) with NUL instead of newline — required when a selected
	// item spans multiple lines.
	outSep := "\n"
	if containsFlag(extraArgs, "--print0") {
		outSep = NUL
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			hasPQ := containsPrintQuery(extraArgs)
			// fzf exits 130 on Ctrl-C / Esc cancel.
			if code == 130 {
				return Result{}, ErrCancelled
			}
			// fzf exits 1 on "no match" — but with --print-query OR
			// --expect, fzf still prints meaningful output (the query +
			// expect key) even when there's no selection. Treat as
			// cancelled ONLY when there's no useful output to parse.
			if code == 1 {
				if (hasPQ || len(expectKeys) > 0) && strings.TrimSpace(out.String()) != "" {
					return parseOutput(out.String(), len(expectKeys) > 0, hasPQ, outSep), nil
				}
				return Result{}, ErrCancelled
			}
		}
		return Result{}, fmt.Errorf("fzf: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	return parseOutput(out.String(), len(expectKeys) > 0, containsPrintQuery(extraArgs), outSep), nil
}

// PickWithPreview is like Pick but enables fzf's preview window.
func PickWithPreview(lines []string, previewCmd, previewSize string, extraArgs ...string) (string, error) {
	args := []string{
		"--preview=" + previewCmd,
		"--preview-window=" + previewSize,
	}
	args = append(args, extraArgs...)
	return Pick(lines, args...)
}

// parseOutput decomposes fzf stdout into (key, query, selection) given
// what flags were set. fzf output ordering (from `man fzf`):
//
//   - --print-query alone:           line 1=query, line 2=selection
//   - --expect alone:                line 1=expect_key, line 2=selection
//   - --print-query AND --expect:    line 1=query, line 2=expect_key, line 3=selection
//
// (Empty trailing lines are stripped by TrimRight before splitting, so
// short outputs may have fewer lines than the maximum.)
//
// sep is the token separator fzf used: "\n" normally, NUL under --print0.
// A multi-line selection round-trips only under NUL, because splitting on
// "\n" would break the item apart at its embedded newlines.
func parseOutput(s string, hasExpect, hasPrintQuery bool, sep string) Result {
	var r Result
	lines := strings.Split(strings.TrimRight(s, sep), sep)
	idx := 0
	if hasPrintQuery {
		if idx < len(lines) {
			r.Query = lines[idx]
			idx++
		}
	}
	if hasExpect {
		if idx < len(lines) {
			r.Key = lines[idx]
			idx++
		}
	}
	if idx < len(lines) {
		r.Selection = lines[idx]
	}
	return r
}

func containsPrintQuery(args []string) bool {
	return containsFlag(args, "--print-query")
}

// containsFlag reports whether args carries the exact bare flag or its
// "flag=value" form.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// recordSep is the input record separator for the given fzf args: NUL when
// --read0 is set (multi-line items), newline otherwise.
func recordSep(args []string) string {
	if containsFlag(args, "--read0") {
		return NUL
	}
	return "\n"
}

// joinRecords frames lines for fzf's stdin with a trailing separator. An
// empty slice yields empty stdin (no ghost entry).
func joinRecords(lines []string, sep string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, sep) + sep
}

// ErrCancelled is returned when the user dismisses fzf without selecting.
type ErrCancelledType struct{}

func (ErrCancelledType) Error() string { return "fzf cancelled" }

var ErrCancelled error = ErrCancelledType{}
