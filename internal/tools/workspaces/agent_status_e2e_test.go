//go:build e2e

package workspaces_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
	"github.com/vyrwu/atelier/internal/tools/workspaces"
	"github.com/vyrwu/atelier/internal/workspace"
)

// TestAgentStatus_DerivesAttentionAndPickerIcons locks the 3-state agent status
// end to end: SetAgentStatus stamps @agent_status and derives @needs_attention
// (only "blocked" needs you), and the M-s picker renders a distinct colored dot
// per state — yellow ⏺ blocked, blue ⏺ running, dim ○ idle.
func TestAgentStatus_DerivesAttentionAndPickerIcons(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := testtmux.New(t)

	seed := func(session, status string) string {
		srv.NewSession(session)
		out, err := srv.Client.Run("display-message", "-p", "-t", session+":1", "#{window_id}")
		if err != nil {
			t.Fatalf("resolve window id for %s: %v", session, err)
		}
		wid := strings.TrimSpace(string(out))
		if err := srv.Client.SetWindowOption(wid, "@repo_path", "/tmp/"+session); err != nil {
			t.Fatalf("seed @repo_path: %v", err)
		}
		if err := workspace.SetAgentStatus(srv.Client, wid, status); err != nil {
			t.Fatalf("SetAgentStatus %s: %v", session, err)
		}
		return wid
	}

	blockedWid := seed("blocked", workspace.AgentBlocked)
	runningWid := seed("running", workspace.AgentRunning)
	idleWid := seed("idle", workspace.AgentIdle)

	// Attention is derived: only "blocked" raises @needs_attention.
	attn := func(wid string) string {
		v, _ := srv.Client.GetWindowOption(wid, "@needs_attention")
		return strings.TrimSpace(v)
	}
	if attn(blockedWid) != "1" {
		t.Errorf("blocked should raise @needs_attention, got %q", attn(blockedWid))
	}
	if attn(runningWid) != "" {
		t.Errorf("running must NOT raise attention, got %q", attn(runningWid))
	}
	if attn(idleWid) != "" {
		t.Errorf("idle must NOT raise attention, got %q", attn(idleWid))
	}

	// Picker renders one distinct colored dot per state.
	rows, err := workspaces.BuildSessionList(srv.Client)
	if err != nil {
		t.Fatalf("BuildSessionList: %v", err)
	}
	display := func(session string) string {
		for _, r := range rows {
			if r.Session == session {
				return r.Display
			}
		}
		t.Fatalf("session %q not in picker rows", session)
		return ""
	}
	if d := display("blocked"); !strings.Contains(d, "33m⏺") {
		t.Errorf("blocked → yellow ⏺; got %q", d)
	}
	if d := display("running"); !strings.Contains(d, "34m⏺") {
		t.Errorf("running → blue ⏺; got %q", d)
	}
	if d := display("idle"); !strings.Contains(d, "90m○") {
		t.Errorf("idle → dim ○; got %q", d)
	}
}
