package workspace

import (
	"strings"
	"testing"
)

func TestParseWorkspaceLine_Ok(t *testing.T) {
	line := strings.Join([]string{
		"%5", "$1", "@2", "feat-branch", "feat", "/home/me/code/repo",
	}, "\t")
	w, ok := parseWorkspaceLine(line)
	if !ok {
		t.Fatalf("parse: ok=false")
	}
	if w.PaneID != "%5" || w.SessionID != "$1" || w.WindowID != "@2" {
		t.Fatalf("unexpected ids: %+v", w)
	}
	if w.Session != "feat-branch" || w.Name != "feat" || w.Cwd != "/home/me/code/repo" {
		t.Fatalf("unexpected fields: %+v", w)
	}
}

func TestParseWorkspaceLine_TooFewFields(t *testing.T) {
	if _, ok := parseWorkspaceLine("only\ttwo\tthree"); ok {
		t.Fatalf("expected ok=false on short input")
	}
}

func TestWorkspace_Target(t *testing.T) {
	w := Workspace{SessionID: "$3", WindowID: "@7"}
	if got, want := w.Target(), "$3:@7"; got != want {
		t.Fatalf("Target: got %q want %q", got, want)
	}
}

func TestWorkspace_AsJSON_RoundTrip(t *testing.T) {
	w := Workspace{
		PaneID: "%1", SessionID: "$2", WindowID: "@3",
		Session: "main", Name: "feat", Cwd: "/code",
		Repo: "repo", Branch: "feat", Attention: true, Recap: "wip",
	}
	data, err := w.AsJSON()
	if err != nil {
		t.Fatalf("AsJSON: %v", err)
	}
	if !strings.Contains(string(data), `"repo": "repo"`) {
		t.Fatalf("JSON missing repo field: %s", data)
	}
	if !strings.Contains(string(data), `"attention": true`) {
		t.Fatalf("JSON missing attention field: %s", data)
	}
}

func TestAtelierSessionFiltering(t *testing.T) {
	// AtelierSessionPrefix must match what we use to name popup sessions.
	if AtelierSessionPrefix != "_atelier_" {
		t.Fatalf("AtelierSessionPrefix drifted; must stay '_atelier_' for compat: got %q", AtelierSessionPrefix)
	}
}
