package workspaces

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/dispatch"
	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/fzfstyle"
	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/spinner"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// ============================================================================
// M-s — aggregate workspace picker (one row per workspace)
// ============================================================================

func SessionsCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "sessions",
		Short: "Pick an active workspace (M-s)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			var rows []WorkspaceRow
			sp := spinner.NewBox("Loading workspaces...")
			sp.Delay = 120 * time.Millisecond
			if err := sp.Run(func() error {
				var e error
				rows, e = BuildWorkspaceList(h)
				return e
			}); err != nil {
				return err
			}

			// Poke a forge sweep so PR rollups are current on the next open.
			workspace.SpawnForgeRefresh()

			lines := make([]string, 0, len(rows))
			for _, r := range rows {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", r.Session, r.Title, r.Display, r.Summary))
			}
			emptyHeader := ""
			if len(rows) == 0 {
				emptyHeader = "No workspaces yet — press M-n to create one, or Esc to dismiss"
			}

			pin := workspace.GetScopePin(h)
			footer := sessionFooter(pin != "", forgeActive())

			opts := []fzfstyle.Opt{
				fzfstyle.WithCustomColor("prompt:red:bold,pointer:red,query:red,hl:red,hl+:red:bold,bg+:#44475a,fg+:#f8f8f2:bold,label:103,border:103,footer:103"),
				fzfstyle.WithDelimiter("\t"),
				fzfstyle.WithNth("3,4"),
				fzfstyle.WithSearchNth("2"),
				fzfstyle.WithHighlightLine(),
				fzfstyle.WithReadZero(),
				fzfstyle.WithPrintZero(),
				fzfstyle.WithBind("alt-x", "transform:"+dispatch.ToolCmd("workspaces", "_delete-prompt", "\"$FZF_PROMPT\"", "{1}")),
				fzfstyle.WithBind("y", confirmBind("y")),
				fzfstyle.WithBind("n", "transform:if [[ \"$FZF_PROMPT\" == Delete* ]]; then echo \"change-prompt(栽 )\"; else echo \"put(n)\"; fi"),
				fzfstyle.WithBind("esc", "transform:if [[ \"$FZF_PROMPT\" == Delete* ]]; then echo \"change-prompt(栽 )\"; else echo \"abort\"; fi"),
				fzfstyle.WithBind("enter", confirmBind("enter")),
				fzfstyle.WithBind("alt-s", "abort"),
				fzfstyle.WithBind("alt-n", "become("+dispatch.ToolCmd("workspaces", "new")+")"),
				fzfstyle.WithBind("alt-c", "become("+dispatch.ToolCmd("workspaces", "changes")+")"),
				fzfstyle.WithBind("alt-r", renameBind()),
				fzfstyle.WithBind("alt-t", tagBind()),
				fzfstyle.WithBind("alt-p", "transform:"+dispatch.ToolCmd("workspaces", "_set-scope-pin", "{q}")),
			}
			if pin != "" {
				opts = append(opts, fzfstyle.WithQuery(pin+" "))
			}
			liveReload := !agentAutoOpenSkipped() && fzf.SupportsLiveReload()
			if liveReload {
				opts = append(opts, fzfstyle.WithBind("start",
					"execute-silent("+dispatch.ToolCmd("workspaces", "_ms-listen", "$FZF_PORT")+")"))
				defer clearMSPickerPort(h)
			}
			opts = append(opts, fzfstyle.WithFooter(footer))
			args := fzfstyle.Args("栽 ", "Select Workspace", "red", opts...)
			if emptyHeader != "" {
				args = append(args, "--header="+emptyHeader)
			}
			if liveReload {
				args = append(args, "--listen", "127.0.0.1:0", "--track")
			}

			debuglog.Logf("workspaces.sessions: opening picker (%d rows)", len(lines))
			picked, err := fzf.Pick(lines, args...)
			if err != nil {
				return err
			}
			if picked == "" {
				return fzf.ErrCancelled
			}
			fields := strings.SplitN(picked, "\t", 2)
			session := fields[0]
			if session == "" {
				return fmt.Errorf("could not parse picked entry: %q", picked)
			}
			return switchToWorkspace(h, session)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// confirmBind builds the y/enter transform: while the delete confirm prompt is
// up, commit the delete + reload; otherwise (enter) accept the selection, (y)
// self-insert.
func confirmBind(key string) string {
	commit := "execute-silent(" + dispatch.ToolCmd("workspaces", "_delete-row", "{1}") + ")+reload(" +
		dispatch.ToolCmd("workspaces", "_session-list") + ")+change-prompt(栽 )"
	if key == "y" {
		return "transform:if [[ \"$FZF_PROMPT\" == Delete* ]]; then echo \"" + commit + "\"; else echo \"put(y)\"; fi"
	}
	return "transform:if [[ \"$FZF_PROMPT\" == Delete* ]]; then echo \"" + commit + "\"; else echo accept; fi"
}

// switchToWorkspace lands the outer client on the workspace's driver window and
// (re)opens the agent when it has resumable state.
func switchToWorkspace(h *tmuxhost.Client, session string) error {
	sid, _ := h.DisplayMessageAt(session, "#{session_id}")
	wid, _ := h.DisplayMessageAt(session+":1", "#{window_id}")
	sid, wid = strings.TrimSpace(sid), strings.TrimSpace(wid)
	if sid != "" && wid != "" {
		ai := integration.Active().AI
		if ai != nil && !agentAutoOpenSkipped() {
			hasPopup, _ := h.HasSession(ai.AgentPopupSession(sid, wid))
			if hasPopup || ai.HasResumableState(h, wid, "") {
				queueAgentOpen(h, sid, wid)
			}
		}
	}
	if err := workspace.LandOuter(h, "="+session, "="+session+":1"); err != nil {
		return err
	}
	workspace.SpawnForgeRefresh()
	return nil
}

// ============================================================================
// M-x — delete a workspace (confirm enumerates worktrees + PRs)
// ============================================================================

// DeletePromptCommand flips the picker prompt to a confirm that ENUMERATES what
// will be destroyed (N worktrees, M PRs). Idempotent while already confirming.
func DeletePromptCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_delete-prompt <fzf-prompt> <session>",
		Short:  "internal: M-x confirm prompt (enumerates worktrees + PRs)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.HasPrefix(args[0], "Delete") {
				return nil // already confirming
			}
			session := args[1]
			if session == "" {
				return nil
			}
			title, wts, prs := deleteEnumeration(session)
			// change-prompt shows what will be destroyed. Keep it one line.
			fmt.Fprintf(cmd.OutOrStdout(),
				"change-prompt(Delete %q — %d worktree(s), %d PR(s)? y/n: )\n", title, wts, prs)
			return nil
		},
	}
}

// deleteEnumeration returns the workspace's title + counts for the confirm line.
func deleteEnumeration(session string) (title string, worktrees, prs int) {
	title = session
	st, err := statestore.Load()
	if err != nil || st == nil {
		return title, 0, 0
	}
	ws := st.FindWorkspace(session)
	if ws == nil {
		return title, 0, 0
	}
	if ws.Title != "" {
		title = ws.Title
	}
	return title, len(ws.Worktrees), len(ws.PRs)
}

// DeleteRowCommand destroys a workspace. The teardown itself (kill session,
// remove worktrees + link tree + root, drop the cache record) is the workspace
// primitive's job — the tool only handles the picker-survival concerns around
// it: land the outer client on a sibling FIRST (so killing the session doesn't
// detach the M-s popup's client), then clean orphaned popups after.
func DeleteRowCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_delete-row <session>",
		Short:  "internal: delete a workspace (delegates teardown to the primitive)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			session := args[0]
			if session == "" {
				return nil
			}
			h := tmuxhost.New("")
			moveOuterToSiblingWorkspace(h, session)
			if err := workspace.DeleteWorkspace(h, session); err != nil {
				debuglog.LogErr("workspaces._delete-row: DeleteWorkspace", err)
			}
			return hostpopup.CleanupOrphanedPopups(h)
		},
	}
}

// moveOuterToSiblingWorkspace lands the outer client on another workspace before
// the target session is killed, so the M-s popup survives instead of the client
// detaching. No-op when the outer isn't on the target, or no sibling exists.
func moveOuterToSiblingWorkspace(h *tmuxhost.Client, victim string) {
	outer, _ := h.ShowGlobalOption("@atelier_outer_client")
	if strings.TrimSpace(outer) == "" {
		return
	}
	curSid, _, err := outerCurrent(h)
	if err != nil {
		return
	}
	victimSid, err := h.DisplayMessageAt(victim, "#{session_id}")
	if err != nil || strings.TrimSpace(curSid) == "" || strings.TrimSpace(curSid) != strings.TrimSpace(victimSid) {
		return
	}
	rows, err := BuildWorkspaceList(h)
	if err != nil {
		return
	}
	for _, r := range rows {
		if r.Session == victim {
			continue
		}
		if err := workspace.LandOuter(h, "="+r.Session, "="+r.Session+":1"); err != nil {
			debuglog.LogErr("workspaces._delete-row: land sibling", err)
		}
		return
	}
}

// ============================================================================
// M-r — rename a workspace (title only; the session name never moves)
// ============================================================================

// renameBind opens the rename picker for the current row, then reloads so the
// new title renders in place.
func renameBind() string {
	return "execute(" + dispatch.ToolCmd("workspaces", "_rename", "{1}") + ")+reload(" +
		dispatch.ToolCmd("workspaces", "_session-list") + ")"
}

// RenameCommand opens a one-field prompt to rename the selected workspace's
// title, then writes @workspace_title + the cache. The tmux session name — the
// switch target — is untouched.
func RenameCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_rename <session>",
		Short:  "internal: rename the selected workspace's title (M-r)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			session := args[0]
			if session == "" {
				return nil
			}
			h := tmuxhost.New(socket)
			current, _ := h.Run("show-option", "-t", session, "-qv", workspace.OptWorkspaceTitle)
			cur := strings.TrimSpace(string(current))
			pickerArgs := fzfstyle.Args("易 ", "Rename Workspace", "111",
				fzfstyle.WithCustomColor("prompt:111:bold,pointer:111,query:111,hl:111,hl+:111:bold,label:103,border:103,header:111,footer:103"),
				fzfstyle.WithNoClear(),
				fzfstyle.WithPrintQuery(),
				fzfstyle.WithExpect("enter"),
				fzfstyle.WithBind("alt-r", "abort"),
				fzfstyle.WithHeader("new title → rename (empty → keep current)"),
				fzfstyle.WithFooter("Enter · rename  |  Esc · cancel"),
				fzfstyle.WithQuery(cur),
			)
			res, err := fzf.PickWithExpect(nil, []string{"enter"}, pickerArgs...)
			if err != nil {
				return nil // Esc → keep current
			}
			if res.Key == "" && res.Query == "" && res.Selection == "" {
				return nil
			}
			title := strings.TrimSpace(res.Query)
			if title == "" || title == cur {
				return nil
			}
			if err := workspace.SetTitle(h, session, title); err != nil {
				debuglog.LogErr("workspaces._rename", err)
				return err
			}
			debuglog.Logf("workspaces._rename: %s → %q", session, title)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// ============================================================================
// M-s live-reload plumbing (unchanged from the per-window era)
// ============================================================================

// SessionListCommand emits the M-s rows for fzf --reload (after delete/tag/rename).
func SessionListCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_session-list",
		Short:  "internal: emit workspace-picker rows (for fzf --reload)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rows, err := BuildWorkspaceList(tmuxhost.New(""))
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s%s", r.Session, r.Title, r.Display, r.Summary, fzf.NUL)
			}
			return nil
		},
	}
}

// MSListenCommand records the M-s picker's fzf --listen port so the refresh loop
// can push a live reload.
func MSListenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_ms-listen <port>",
		Short:  "internal: record the M-s picker's fzf --listen port for live reload",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return tmuxhost.New(socket).SetGlobalOption(optMSPickerPort, strings.TrimSpace(args[0]))
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func clearMSPickerPort(h *tmuxhost.Client) { _ = h.UnsetGlobalOption(optMSPickerPort) }
