// Package awsassume composes granted `assume` invocations for atelier's
// AWS-auth tools (aws, k8s, pg).
//
// granted's `assume` is a *sourced shell function*, not a binary on PATH —
// it exports credentials into the calling shell (or, with --exec, runs a
// command with them). Because tmux runs pane commands through a
// non-interactive shell, `assume` is undefined there. Every invocation must
// therefore go through an interactive login shell (`$SHELL -i -c ...`) so the
// granted shell integration is loaded and the function resolves.
//
// Two shapes are used:
//   - PickerCmd — assume a profile so credentials PERSIST in the pane's shell
//     (the AWS-profile picker). This is granted's headline behavior over
//     `aws-vault exec`, which only ever spawned a subshell.
//   - WrapAuth — assume a profile just for the duration of a one-shot tool
//     launch (k8s/pg run a TUI, then exit), via `assume <profile> --exec`.
package awsassume

import (
	"os"
	"strings"
)

// DefaultShell returns $SHELL, falling back to /bin/zsh.
func DefaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}

// ShellQuote wraps s in single quotes, POSIX-escaping embedded single quotes
// (`'` becomes `'\”`).
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// PickerCmd builds the command the AWS-profile picker respawns the caller
// pane with: run `assume <profile>` in an interactive shell so credentials
// are exported into it, then `exec $SHELL` to hand the user a fresh shell
// that inherits them (and keeps the pane alive after they exit sub-shells).
//
//	$SHELL -i -c 'assume '<profile>'; exec $SHELL'
func PickerCmd(profile, shell string) string {
	inner := "assume " + ShellQuote(profile) + "; exec " + shell
	return shell + " -i -c " + ShellQuote(inner)
}

// WrapAuth wraps a tool launch command with a user-configured granted auth
// prefix (e.g. `assume prod --exec`). The launch command is passed as a
// single quoted argument — granted's --exec takes one command string — and
// the whole thing runs in an interactive shell so the `assume` function
// resolves. When authCmd is empty, launch is returned unwrapped.
//
//	$SHELL -i -c 'assume prod --exec '<launch>''
func WrapAuth(authCmd, launch, shell string) string {
	authCmd = strings.TrimSpace(authCmd)
	if authCmd == "" {
		return launch
	}
	inner := authCmd + " " + ShellQuote(launch)
	return shell + " -i -c " + ShellQuote(inner)
}
