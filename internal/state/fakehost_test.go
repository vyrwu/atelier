package state

import (
	"fmt"
	"strings"
)

// fakeHost is a pure in-memory Host for unit-testing the topology / outer /
// reconcile helpers without a tmux server.
type fakeHost struct {
	sessions []string          // ListSessions
	windows  []string          // ListWindows ("$N @M")
	globals  map[string]string // Show/Set/UnsetGlobalOption
	runOut   map[string]string // keyed by the tmux verb (args[0])
	// dm answers DisplayMessageAt: dm[target][format] = value.
	dm     map[string]map[string]string
	killed []string
}

func newFakeHost() *fakeHost {
	return &fakeHost{globals: map[string]string{}, runOut: map[string]string{}, dm: map[string]map[string]string{}}
}

func (f *fakeHost) ListSessions() ([]string, error) { return f.sessions, nil }
func (f *fakeHost) ListWindows() ([]string, error)  { return f.windows, nil }

func (f *fakeHost) ShowGlobalOption(name string) (string, error) { return f.globals[name], nil }
func (f *fakeHost) SetGlobalOption(name, value string) error     { f.globals[name] = value; return nil }
func (f *fakeHost) UnsetGlobalOption(name string) error          { delete(f.globals, name); return nil }
func (f *fakeHost) KillSession(name string) error {
	f.killed = append(f.killed, name)
	kept := f.sessions[:0]
	for _, s := range f.sessions {
		if s != name {
			kept = append(kept, s)
		}
	}
	f.sessions = kept
	return nil
}

func (f *fakeHost) DisplayMessageAt(target, format string) (string, error) {
	m, ok := f.dm[target]
	if !ok {
		return "", fmt.Errorf("no such target %q", target)
	}
	v, ok := m[format]
	if !ok {
		return "", fmt.Errorf("no format %q for %q", format, target)
	}
	return v, nil
}

func (f *fakeHost) Run(args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, nil
	}
	verb := args[0]
	// CaptureTopology reads windows via `list-windows -F <enriched>`. When a
	// test hasn't set an explicit enriched payload, synthesize one from
	// f.windows ("$sid @wid") with empty capability fields, so the many
	// structural CaptureTopology tests keep working unchanged.
	if verb == "list-windows" {
		if v, ok := f.runOut["list-windows"]; ok {
			return []byte(v), nil
		}
		var lines []string
		for _, w := range f.windows {
			ff := strings.Fields(w)
			if len(ff) < 2 {
				continue
			}
			rec := make([]string, 10)
			rec[0], rec[1] = ff[0], ff[1]
			lines = append(lines, strings.Join(rec, winSep))
		}
		return []byte(strings.Join(lines, "\n")), nil
	}
	return []byte(f.runOut[verb]), nil
}

// windowLine builds an enriched list-windows capture line for tests that need
// capability fields. Order matches windowCaptureFormat.
func windowLine(sid, wid, name, repoPath, kind, attention, recap, tag, forge, worktree string) string {
	return strings.Join([]string{sid, wid, name, repoPath, kind, attention, recap, tag, forge, worktree}, winSep)
}

// setClients stores raw list-clients output lines (already in
// clientListFormat field order: name|sid|wid|tty|session).
func (f *fakeHost) setClients(lines ...string) {
	f.runOut["list-clients"] = strings.Join(lines, "\n")
}

// clientLine formats one list-clients line in the kernel's field order so
// tests read as (name, session, ...) without hand-ordering the pipes.
func clientLine(name, session, sid, wid, tty string) string {
	return strings.Join([]string{name, sid, wid, tty, session}, "|")
}

// setSessionsWithIDs stores list-sessions -F "#{session_id}|#{session_name}".
func (f *fakeHost) setSessionsWithIDs(lines ...string) {
	f.runOut["list-sessions"] = strings.Join(lines, "\n")
}
