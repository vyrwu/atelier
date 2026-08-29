package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/state"
)

// wedgedTopology models the two headline failure modes at once: the outer
// pointer stamped on the launcher, and an orphan popup whose parent is gone.
func wedgedTopology() *state.Topology {
	claude, _ := state.ParsePopup("_atelier_claude_9_9")
	return &state.Topology{
		Sessions: []state.Session{
			{ID: "$0", Name: "default", Kind: state.KindLauncher},
			{ID: "$1", Name: "ws", Kind: state.KindWorkspace},
			{ID: "$2", Name: "_atelier_claude_9_9", Kind: state.KindPopup, Popup: claude},
		},
		Windows:    []state.Window{{SessionID: "$1", WindowID: "@2"}},
		Clients:    []state.ClientRef{{Name: "/dev/ttys0", Session: "ws", SessionID: "$1", WindowID: "@2", TTY: "/dev/ttys0", Kind: state.ClientWorkspace}},
		OuterPtr:   state.Outer{Session: "$0"}, // launcher — the bug
		LiveSidWid: map[string]bool{"1_2": true},
	}
}

func TestWriteStateJSON_ShapeAndViolations(t *testing.T) {
	top := wedgedTopology()
	var buf bytes.Buffer
	if err := writeStateJSON(&buf, top, state.Validate(top)); err != nil {
		t.Fatalf("writeStateJSON: %v", err)
	}
	var got stateShowJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if len(got.Sessions) != 3 || len(got.Clients) != 1 {
		t.Fatalf("sessions=%d clients=%d, want 3/1", len(got.Sessions), len(got.Clients))
	}
	if got.Outer.Valid {
		t.Error("outer stamped on launcher must be reported invalid")
	}
	codes := map[string]bool{}
	for _, v := range got.Violations {
		codes[v.Code] = true
	}
	if !codes["outer_is_launcher"] || !codes["orphan_popup"] {
		t.Errorf("violations missing expected codes: %+v", got.Violations)
	}
	// The orphan popup session is flagged orphan=true in the session list.
	var sawOrphan bool
	for _, s := range got.Sessions {
		if s.Name == "_atelier_claude_9_9" && s.Orphan {
			sawOrphan = true
		}
	}
	if !sawOrphan {
		t.Error("orphan popup session not marked orphan in JSON")
	}
}

func TestWriteStateText_RendersSectionsAndViolations(t *testing.T) {
	top := wedgedTopology()
	var buf bytes.Buffer
	writeStateText(&buf, top, state.Validate(top))
	s := buf.String()
	for _, want := range []string{"SESSIONS", "CLIENTS", "OUTER", "VIOLATIONS", "outer_is_launcher", "orphan_popup", "[INVALID]"} {
		if !strings.Contains(s, want) {
			t.Errorf("text output missing %q; got:\n%s", want, s)
		}
	}
}

func TestWriteState_RendersWindowCapability(t *testing.T) {
	top := &state.Topology{
		Sessions: []state.Session{{ID: "$1", Name: "vyrwu/atelier", Kind: state.KindWorkspace}},
		Windows: []state.Window{
			{SessionID: "$1", WindowID: "@2", Name: "feat/x", RepoPath: "/r",
				WorkspaceID: "vyrwu/atelier", Root: "/ws/root", Driver: true,
				Attention: true, Recap: "summary", Tag: "wip",
				PaneCwd: "/gone", PaneCwdLive: false},
		},
		LiveSidWid: map[string]bool{"1_2": true},
	}
	var buf bytes.Buffer
	writeStateText(&buf, top, state.Validate(top))
	s := buf.String()
	for _, want := range []string{"WINDOWS", "feat/x", "attn", "ws=vyrwu/atelier", "driver", "tag=wip", "CWD-GONE", "recap"} {
		if !strings.Contains(s, want) {
			t.Errorf("windows section missing %q; got:\n%s", want, s)
		}
	}

	// JSON carries the per-window capability too.
	var jbuf bytes.Buffer
	if err := writeStateJSON(&jbuf, top, state.Validate(top)); err != nil {
		t.Fatalf("writeStateJSON: %v", err)
	}
	var got stateShowJSON
	if err := json.Unmarshal(jbuf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Windows) != 1 || !got.Windows[0].Attention || got.Windows[0].PaneCwdLive ||
		got.Windows[0].WorkspaceID != "vyrwu/atelier" || got.Windows[0].Root != "/ws/root" || !got.Windows[0].Driver {
		t.Errorf("window JSON wrong: %+v", got.Windows)
	}
}

func TestWriteStateText_CleanHasNoViolations(t *testing.T) {
	top := &state.Topology{
		Sessions:   []state.Session{{ID: "$1", Name: "ws", Kind: state.KindWorkspace}},
		Windows:    []state.Window{{SessionID: "$1", WindowID: "@2"}},
		OuterPtr:   state.Outer{Session: "$1", Window: "@2"},
		LiveSidWid: map[string]bool{"1_2": true},
	}
	var buf bytes.Buffer
	writeStateText(&buf, top, state.Validate(top))
	if !strings.Contains(buf.String(), "(none)") {
		t.Errorf("clean topology should render (none); got:\n%s", buf.String())
	}
}
