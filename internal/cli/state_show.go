package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/state"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// stateShowCmd is `atelier state show`: a one-shot topology + invariant
// report. Replaces the dozen-command `tmux list-*/show-*` archaeology that
// diagnosing a stranded-outer / orphan-popup wedge used to require. Read-only.
func stateShowCmd() *cobra.Command {
	var socket string
	var asJSON bool
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the live tmux topology + invariant report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			top, err := state.CaptureTopology(tmuxhost.New(socket))
			if err != nil {
				return err
			}
			violations := state.Validate(top)
			if asJSON {
				return writeStateJSON(cmd.OutOrStdout(), top, violations)
			}
			writeStateText(cmd.OutOrStdout(), top, violations)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	c.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	return c
}

func writeStateText(w io.Writer, top *state.Topology, violations []state.Violation) {
	fmt.Fprintln(w, "SESSIONS")
	for _, s := range top.Sessions {
		line := fmt.Sprintf("  %-24s %-9s %s", s.Name, s.Kind, s.ID)
		if s.Kind == state.KindPopup && s.Popup.Form != state.FormNone {
			live := "orphan"
			if top.PopupParentLive(s.Popup) {
				live = "live"
			}
			line += fmt.Sprintf("  parent=$%s@%s %s", s.Popup.SidDigit, s.Popup.WidDigit, live)
		}
		fmt.Fprintln(w, line)
	}

	fmt.Fprintln(w, "WINDOWS")
	for _, win := range top.Windows {
		fmt.Fprintf(w, "  %s %s %-24s %s\n", win.SessionID, win.WindowID, win.Name, strings.Join(windowFlags(win), " "))
	}

	fmt.Fprintln(w, "CLIENTS")
	for _, c := range top.Clients {
		fmt.Fprintf(w, "  %-14s %-9s session=%s tty=%s\n", c.Name, c.Kind, c.Session, c.TTY)
	}

	fmt.Fprintln(w, "OUTER")
	fmt.Fprintf(w, "  pane=%s session=%s window=%s client=%s  [%s]\n",
		dash(top.OuterPtr.Pane), dash(top.OuterPtr.Session), dash(top.OuterPtr.Window), dash(top.OuterPtr.Client), outerStatus(top))

	fmt.Fprintln(w, "VIOLATIONS")
	if len(violations) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	for _, v := range violations {
		fmt.Fprintf(w, "  %-5s %-24s %s: %s\n", v.Severity, v.Code, v.Subject, v.Detail)
	}
}

// windowFlags renders a window's kernel-owned capability state as compact
// indicators for `state show`.
func windowFlags(win state.Window) []string {
	var f []string
	if win.Attention {
		f = append(f, "attn")
	}
	if win.WorkspaceID != "" {
		f = append(f, "ws="+win.WorkspaceID)
	}
	if win.Driver {
		f = append(f, "driver")
	}
	if win.Tag != "" {
		f = append(f, "tag="+win.Tag)
	}
	if win.ForgeState != "" {
		f = append(f, "forge="+win.ForgeState)
	}
	if win.RepoPath != "" && win.PaneCwd != "" && !win.PaneCwdLive {
		f = append(f, "CWD-GONE")
	}
	if win.Recap != "" {
		f = append(f, "recap")
	}
	return f
}

// outerStatus labels the outer pointer: "empty" when nothing is stamped (a
// legitimate no-chain state), "INVALID" when the stored pointer is stale /
// launcher / popup, else "valid".
func outerStatus(top *state.Topology) string {
	if top.OuterPtr.Session == "" && top.OuterPtr.Pane == "" && top.OuterPtr.Window == "" && top.OuterPtr.Client == "" {
		return "empty"
	}
	if _, corrected := state.ResolveOuter(top); corrected {
		return "INVALID"
	}
	return "valid"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// JSON DTOs — a stable shape independent of the internal Topology layout.
type stateShowJSON struct {
	Sessions   []sessionJSON   `json:"sessions"`
	Windows    []windowJSON    `json:"windows"`
	Clients    []clientJSON    `json:"clients"`
	Outer      outerJSON       `json:"outer"`
	Violations []violationJSON `json:"violations"`
}

type windowJSON struct {
	SessionID   string `json:"session_id"`
	WindowID    string `json:"window_id"`
	Name        string `json:"name"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Root        string `json:"root,omitempty"`
	RepoPath    string `json:"repo_path,omitempty"`
	Driver      bool   `json:"driver,omitempty"`
	Attention   bool   `json:"attention,omitempty"`
	Recap       string `json:"recap,omitempty"`
	Tag         string `json:"tag,omitempty"`
	ForgeState  string `json:"forge_state,omitempty"`
	PaneCwdLive bool   `json:"pane_cwd_live"`
}

type sessionJSON struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Tool   string `json:"tool,omitempty"`
	Parent string `json:"parent,omitempty"`
	Orphan bool   `json:"orphan,omitempty"`
}

type clientJSON struct {
	Name    string `json:"name"`
	Session string `json:"session"`
	Kind    string `json:"kind"`
	TTY     string `json:"tty"`
}

type outerJSON struct {
	Pane    string `json:"pane"`
	Session string `json:"session"`
	Window  string `json:"window"`
	Client  string `json:"client"`
	Valid   bool   `json:"valid"`
}

type violationJSON struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Subject  string `json:"subject"`
	Detail   string `json:"detail"`
	Fixable  bool   `json:"fixable"`
}

func writeStateJSON(w io.Writer, top *state.Topology, violations []state.Violation) error {
	out := stateShowJSON{Violations: []violationJSON{}}
	for _, s := range top.Sessions {
		sj := sessionJSON{ID: s.ID, Name: s.Name, Kind: s.Kind.String()}
		if s.Kind == state.KindPopup && s.Popup.Form != state.FormNone {
			sj.Tool = s.Popup.Tool
			sj.Parent = fmt.Sprintf("$%s@%s", s.Popup.SidDigit, s.Popup.WidDigit)
			sj.Orphan = !top.PopupParentLive(s.Popup)
		}
		out.Sessions = append(out.Sessions, sj)
	}
	for _, win := range top.Windows {
		out.Windows = append(out.Windows, windowJSON{
			SessionID: win.SessionID, WindowID: win.WindowID, Name: win.Name,
			WorkspaceID: win.WorkspaceID, Root: win.Root, RepoPath: win.RepoPath,
			Driver: win.Driver, Attention: win.Attention,
			Recap: win.Recap, Tag: win.Tag, ForgeState: win.ForgeState, PaneCwdLive: win.PaneCwdLive,
		})
	}
	for _, c := range top.Clients {
		out.Clients = append(out.Clients, clientJSON{Name: c.Name, Session: c.Session, Kind: c.Kind.String(), TTY: c.TTY})
	}
	out.Outer = outerJSON{
		Pane: top.OuterPtr.Pane, Session: top.OuterPtr.Session,
		Window: top.OuterPtr.Window, Client: top.OuterPtr.Client, Valid: outerStatus(top) != "INVALID",
	}
	for _, v := range violations {
		out.Violations = append(out.Violations, violationJSON{
			Code: string(v.Code), Severity: v.Severity.String(), Subject: v.Subject,
			Detail: v.Detail, Fixable: v.Fixable,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
