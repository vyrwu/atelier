package state

import (
	"os"
	"strconv"
	"strings"
)

// Topology is atelier's authoritative snapshot of the live tmux entity graph:
// sessions (with their kind), windows, attached clients (with their kind), the
// stored outer-focus pointer, and the global hooks armed at capture time.
//
// It is derived from a single read of tmux — never a persisted second copy
// that can drift. Everything the kernel decides (which client is outer,
// whether the outer pointer is valid, which popups are orphaned) is a pure
// function over a Topology, so the decisions are unit-testable without tmux.
type Topology struct {
	Sessions   []Session
	Windows    []Window
	Clients    []ClientRef
	OuterPtr   Outer
	LiveSidWid map[string]bool // "<sidDigits>_<widDigits>" for popup-parent liveness
	// GlobalHooks is the raw `show-hooks -g` output at capture time, used to
	// detect a client-moving hook left armed at rest (the ReturnToOuterShell /
	// OpenOnOuter leak). Empty if the read failed.
	GlobalHooks string
}

// Session is a tmux session and its role in atelier's graph.
type Session struct {
	ID    string // $N
	Name  string
	Kind  SessionKind
	Popup PopupInfo // valid when Kind==KindPopup and the name carries a parent
}

// Window is a tmux window and the kernel-owned capability state stamped on it.
// The capability fields model the program's behavior (attention, AI summary,
// workspace kind, tag, forge badge, worktree) — a stable, delivery-agnostic
// view. Today they are sourced from tmux @options in CaptureTopology; if that
// delivery ever changes (a daemon), only the capture layer changes, not this
// model or the validators over it.
type Window struct {
	SessionID   string // $N
	WindowID    string // @N
	WindowIndex int    // #{window_index} — the driver is the lowest index
	Name        string // window name (the driver title, for workspace windows)
	WorkspaceID string // @workspace_id (session-scoped; the workspace marker)
	Root        string // @workspace_root (session-scoped; the dedicated dir)
	RepoPath    string // @repo_path (session-scoped; optional single-repo hint)
	Driver      bool   // @workspace_driver == "1" — the one agent window
	Attention   bool   // @needs_attention == "1"
	Recap       string // @attention_recap — the AI summary
	Tag         string // @workspace_tag
	ForgeState  string // @forge_state
	// PaneCwd is the window's active pane cwd (pane_current_path). For a driver
	// window this is normally the workspace root, but the user can cd elsewhere.
	// PaneCwdLive is os.Stat'd in CaptureTopology so Validate stays pure over
	// the captured bool.
	PaneCwd     string
	PaneCwdLive bool
}

// ClientKind mirrors SessionKind for the session a client is attached to.
type ClientKind int

const (
	ClientWorkspace ClientKind = iota // attached to a workspace session — the only outer-eligible kind
	ClientLauncher                    // attached to the launcher ("default")
	ClientPopup                       // attached to a popup-backing session
)

func (k ClientKind) String() string {
	switch k {
	case ClientLauncher:
		return "launcher"
	case ClientPopup:
		return "popup"
	default:
		return "workspace"
	}
}

// ClientRef is an attached tmux client and its current session/window.
type ClientRef struct {
	Name      string // client_name (usually /dev/ttysNNN)
	Session   string // client_session (session name)
	SessionID string // session_id ($N) of the client's current session
	WindowID  string // window_id (@N) of the client's current window
	TTY       string // client_tty (for the ≤1-client-per-tty invariant)
	Kind      ClientKind
}

// Outer is the stored @atelier_outer_* pointer, exactly as read (before
// validation). Pane/Session/Window are ids; Client is a client name.
type Outer struct {
	Pane    string
	Session string
	Window  string
	Client  string
}

// Host is the tmux surface the topology/outer/reconcile helpers need.
// *tmuxhost.Client satisfies it; tests substitute a fake.
type Host interface {
	ListSessions() ([]string, error)
	ListWindows() ([]string, error)
	ShowGlobalOption(name string) (string, error)
	SetGlobalOption(name, value string) error
	UnsetGlobalOption(name string) error
	DisplayMessageAt(target, format string) (string, error)
	KillSession(name string) error
	Run(args ...string) ([]byte, error)
}

// clientListFormat keeps the free-text session NAME LAST so a stray '|' in it
// can't shift the fixed fields (same guard as cli/status.go's rollup format).
// The other fields (client_name, session_id $N, window_id @N, client_tty) are
// all '|'-free.
const clientListFormat = "#{client_name}|#{session_id}|#{window_id}|#{client_tty}|#{client_session}"

// winSep separates window-capture fields. It is the ASCII Unit Separator, not
// '|', because several window fields are free text (AI recap, tag, paths) that
// can legitimately contain '|'; \x1f never appears in branch names, filesystem
// paths, or AI summaries, so no field can shift another (protecting the
// invariant-critical fields that follow).
const winSep = "\x1f"

// windowCaptureFormat reads every window's structural ids + the kernel-owned
// capability state in a single list-windows call. The @workspace_* / @repo_path
// options are session-scoped but resolve through inheritance in a window's
// format context.
var windowCaptureFormat = strings.Join([]string{
	"#{session_id}", "#{window_id}", "#{window_index}", "#{window_name}",
	"#{@workspace_id}", "#{@workspace_root}", "#{@repo_path}", "#{@workspace_driver}",
	"#{@needs_attention}", "#{@attention_recap}", "#{@workspace_tag}", "#{@forge_state}",
	"#{pane_current_path}",
}, winSep)

func parseWindow(line string) (Window, bool) {
	f := strings.Split(line, winSep)
	if len(f) < 13 {
		return Window{}, false
	}
	idx := 0
	if n, err := strconv.Atoi(strings.TrimSpace(f[2])); err == nil {
		idx = n
	}
	w := Window{
		SessionID:   f[0],
		WindowID:    f[1],
		WindowIndex: idx,
		Name:        f[3],
		WorkspaceID: f[4],
		Root:        f[5],
		RepoPath:    f[6],
		Driver:      strings.TrimSpace(f[7]) == "1",
		Attention:   strings.TrimSpace(f[8]) == "1",
		Recap:       f[9],
		Tag:         f[10],
		ForgeState:  f[11],
		PaneCwd:     f[12],
	}
	w.PaneCwdLive = paneCwdLive(w.PaneCwd)
	return w, true
}

// paneCwdLive reports whether a window's active pane cwd still exists. An empty
// path counts as live (nothing to be missing — mirrors workspace/restore.go's
// pathExists). One stat syscall, no git. Done here in capture so Validate
// stays pure over the resulting bool.
func paneCwdLive(path string) bool {
	if path == "" {
		return true
	}
	_, err := os.Stat(path)
	return err == nil
}

// clientKind classifies a client by its session name for outer-eligibility.
// Only a real workspace client can be the outer. The launcher is excluded;
// and — matching the long-standing convention that every atelier-internal
// backing session is named with a leading underscore (and the retained
// `_`-prefix filters in the workspace picker / sessionlist) — ANY
// underscore-prefixed session is treated as non-outer (inner), not just the
// recognized popup forms. That keeps a stray `_scratch` shell from capturing
// the outer pick or receiving tool popups.
func clientKind(name string) ClientKind {
	if name == LauncherSessionName {
		return ClientLauncher
	}
	if strings.HasPrefix(name, "_") {
		return ClientPopup
	}
	return ClientWorkspace
}

// ClassifyClients lists attached clients and classifies each by the kind of
// the session it is attached to (name-based, via ClassifySession). This is the
// lean read the popup-open hot path uses — one list-clients call, no session/
// window/hook sweep.
func ClassifyClients(h Host) ([]ClientRef, error) {
	out, err := h.Run("list-clients", "-F", clientListFormat)
	if err != nil {
		return nil, err
	}
	var clients []ClientRef
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "|", 5)
		if len(f) < 5 {
			continue
		}
		session := f[4]
		clients = append(clients, ClientRef{
			Name:      f[0],
			Session:   session,
			SessionID: f[1],
			WindowID:  f[2],
			TTY:       f[3],
			Kind:      clientKind(session),
		})
	}
	return clients, nil
}

// CaptureTopology performs the full graph read: sessions (+ids), windows,
// clients, the four outer-* globals, and the global hooks. One authoritative
// snapshot for `state show`, Validate, and Reconcile. Not for the hot popup
// path — use ClassifyClients there.
func CaptureTopology(h Host) (*Topology, error) {
	t := &Topology{LiveSidWid: map[string]bool{}}

	sessOut, err := h.Run("list-sessions", "-F", "#{session_id}|#{session_name}")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(sessOut)), "\n") {
		if line == "" {
			continue
		}
		id, name, found := strings.Cut(line, "|")
		if !found {
			continue
		}
		s := Session{ID: id, Name: name, Kind: ClassifySession(name)}
		if s.Kind == KindPopup {
			s.Popup, _ = ParsePopup(name)
		}
		t.Sessions = append(t.Sessions, s)
	}

	winOut, err := h.Run("list-windows", "-a", "-F", windowCaptureFormat)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(winOut)), "\n") {
		if line == "" {
			continue
		}
		w, ok := parseWindow(line)
		if !ok {
			continue
		}
		t.Windows = append(t.Windows, w)
		t.LiveSidWid[Digits(w.SessionID)+"_"+Digits(w.WindowID)] = true
	}

	if t.Clients, err = ClassifyClients(h); err != nil {
		return nil, err
	}

	t.OuterPtr = Outer{
		Pane:    show(h, OptOuterPane),
		Session: show(h, OptOuterSession),
		Window:  show(h, OptOuterWindow),
		Client:  show(h, OptOuterClient),
	}

	if hooks, err := h.Run("show-hooks", "-g"); err == nil {
		t.GlobalHooks = string(hooks)
	}
	return t, nil
}

func show(h Host, opt string) string {
	v, _ := h.ShowGlobalOption(opt)
	return strings.TrimSpace(v)
}

// OuterClients returns the attached workspace-kind clients — the only clients
// eligible to be the outer. Excludes the launcher and popup clients, which is
// what keeps the launcher "default" session out of the outer pick.
func (t *Topology) OuterClients() []ClientRef {
	var out []ClientRef
	for _, c := range t.Clients {
		if c.Kind == ClientWorkspace {
			out = append(out, c)
		}
	}
	return out
}

// InnerClients returns the attached popup-kind clients.
func (t *Topology) InnerClients() []ClientRef {
	var out []ClientRef
	for _, c := range t.Clients {
		if c.Kind == ClientPopup {
			out = append(out, c)
		}
	}
	return out
}

// SessionByID returns the session with the given id ($N).
func (t *Topology) SessionByID(id string) (Session, bool) {
	for _, s := range t.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	return Session{}, false
}

// SessionByName returns the session with the given name.
func (t *Topology) SessionByName(name string) (Session, bool) {
	for _, s := range t.Sessions {
		if s.Name == name {
			return s, true
		}
	}
	return Session{}, false
}

// HasWindow reports whether a window id (@N) is live.
func (t *Topology) HasWindow(windowID string) bool {
	for _, w := range t.Windows {
		if w.WindowID == windowID {
			return true
		}
	}
	return false
}

// PopupParentLive reports whether the parent (session, window) of a popup is
// still live.
func (t *Topology) PopupParentLive(info PopupInfo) bool {
	return t.LiveSidWid[info.SidDigit+"_"+info.WidDigit]
}
