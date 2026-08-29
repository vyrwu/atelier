package workspaces

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/debuglog"
	"github.com/vyrwu/atelier/internal/dispatch"
	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/fzfstyle"
	"github.com/vyrwu/atelier/internal/integration"
	"github.com/vyrwu/atelier/internal/notify"
	"github.com/vyrwu/atelier/internal/spinner"
	"github.com/vyrwu/atelier/internal/statestore"
	"github.com/vyrwu/atelier/internal/workspace"
)

// ============================================================================
// M-c — List Changes (cross-repo PR aggregate, actionable)
// ============================================================================
//
// Rows (two lines):
//
//	<FORGE> <REPO> #<PR-NO>  <CI> <APPROVAL>  <N comments>
//	  <title>
//
// Footer: M-o open · M-c close. Replaces per-workspace PR tracking with a
// cross-repo aggregate and makes PRs actionable (open in browser, close).

// ChangeRow is one PR row. Fields emitted as:
//
//	<repo>\t<number>\t<url>\t<display>\t<title-line>
//
// Binds pass {1} {2} {3} (repo, number, url) to the open/close actions.
type ChangeRow struct {
	Repo    string
	Number  int
	URL     string
	Display string
	Title   string
}

func ChangesCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "changes",
		Short: "List changes — cross-repo PR aggregate (M-c)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if !forgeActive() {
				notify.Show(notify.Info, "no forge integration configured ([forge] provider)")
				return fzf.ErrCancelled
			}
			// Poke a sweep so the list is current, then build from the cache.
			workspace.SpawnForgeRefresh()

			var rows []ChangeRow
			sp := spinner.NewBox("Loading changes...")
			sp.Delay = 120 * time.Millisecond
			if err := sp.Run(func() error {
				rows = buildChangeRows()
				return nil
			}); err != nil {
				return err
			}

			lines := make([]string, 0, len(rows))
			for _, r := range rows {
				lines = append(lines, fmt.Sprintf("%s\t%d\t%s\t%s\t%s", r.Repo, r.Number, r.URL, r.Display, r.Title))
			}
			emptyHeader := ""
			if len(rows) == 0 {
				emptyHeader = "No open changes — open a PR from a workspace, or Esc to dismiss"
			}

			closeCommit := "execute-silent(" + dispatch.ToolCmd("workspaces", "_pr-close", "{1}", "{2}", "{3}") +
				")+reload(" + dispatch.ToolCmd("workspaces", "_changes-list") + ")+change-prompt(改 )"
			opts := []fzfstyle.Opt{
				fzfstyle.WithCustomColor("prompt:110:bold,pointer:110,query:110,hl:110,hl+:110:bold,bg+:#44475a,fg+:#f8f8f2:bold,label:103,border:103,footer:103"),
				fzfstyle.WithDelimiter("\t"),
				fzfstyle.WithNth("4,5"),
				fzfstyle.WithSearchNth("4"),
				fzfstyle.WithHighlightLine(),
				fzfstyle.WithReadZero(),
				fzfstyle.WithPrintZero(),
				fzfstyle.WithBind("alt-o", "execute-silent("+dispatch.ToolCmd("workspaces", "_open-forge", "{1}", "{2}", "{3}")+")"),
				fzfstyle.WithBind("enter", "execute-silent("+dispatch.ToolCmd("workspaces", "_open-forge", "{1}", "{2}", "{3}")+")"),
				fzfstyle.WithBind("alt-s", "become("+dispatch.ToolCmd("workspaces", "sessions")+")"),
				fzfstyle.WithBind("alt-n", "become("+dispatch.ToolCmd("workspaces", "new")+")"),
			}
			if forgeWriteAllowed() {
				opts = append(opts,
					fzfstyle.WithBind("alt-c", "transform:"+dispatch.ToolCmd("workspaces", "_pr-close-prompt", "\"$FZF_PROMPT\"", "{1}", "{2}")),
					fzfstyle.WithBind("y", "transform:if [[ \"$FZF_PROMPT\" == Close* ]]; then echo \""+closeCommit+"\"; else echo \"put(y)\"; fi"),
					fzfstyle.WithBind("n", "transform:if [[ \"$FZF_PROMPT\" == Close* ]]; then echo \"change-prompt(改 )\"; else echo \"put(n)\"; fi"),
					fzfstyle.WithBind("esc", "transform:if [[ \"$FZF_PROMPT\" == Close* ]]; then echo \"change-prompt(改 )\"; else echo \"abort\"; fi"),
					fzfstyle.WithFooter("M-o · open  |  M-c · close  |  M-? · help"),
				)
			} else {
				opts = append(opts, fzfstyle.WithFooter("M-o · open  |  M-? · help"))
			}
			args := fzfstyle.Args("改 ", "List Changes", "110", opts...)
			if emptyHeader != "" {
				args = append(args, "--header="+emptyHeader)
			}
			debuglog.Logf("workspaces.changes: opening picker (%d rows)", len(lines))
			picked, err := fzf.Pick(lines, args...)
			if err != nil {
				return err
			}
			// Enter opens in-browser via the bind; nothing to do on return.
			_ = picked
			return nil
		},
	}
	return c
}

// buildChangeRows flattens every workspace's PRs into deduped, sorted change
// rows (open first, then draft/merged/closed; newest-updated within a state).
func buildChangeRows() []ChangeRow {
	st, err := statestore.Load()
	if err != nil || st == nil {
		return nil
	}
	seen := map[string]bool{}
	var prs []statestore.PR
	for i := range st.Workspaces {
		for _, pr := range st.Workspaces[i].PRs {
			key := pr.Repo + "#" + strconv.Itoa(pr.Number)
			if seen[key] {
				continue
			}
			seen[key] = true
			prs = append(prs, pr)
		}
	}
	sort.SliceStable(prs, func(a, b int) bool {
		ra := forgeStateRank(integration.ForgeState(prs[a].State))
		rb := forgeStateRank(integration.ForgeState(prs[b].State))
		if ra != rb {
			return ra < rb
		}
		return prs[a].UpdatedAt > prs[b].UpdatedAt
	})
	rows := make([]ChangeRow, 0, len(prs))
	for _, pr := range prs {
		rows = append(rows, ChangeRow{
			Repo:    pr.Repo,
			Number:  pr.Number,
			URL:     pr.URL,
			Display: formatChangeDisplay(pr),
			Title:   formatChangeTitle(pr.Title),
		})
	}
	return rows
}

// formatChangeDisplay renders the first row: forge glyph, repo, #num, CI +
// approval glyphs, comment count. Pure.
func formatChangeDisplay(pr statestore.PR) string {
	forge := strings.TrimPrefix(renderForgeBadge(integration.ForgeState(pr.State)), " ")
	if forge == "" {
		forge = " "
	}
	ci := renderCIGlyph(integration.CIStatus(pr.CI))
	review := renderReviewGlyph(integration.ReviewDecision(pr.ReviewDecision))
	comments := ""
	if pr.Comments > 0 {
		comments = fmt.Sprintf(" \033[38;5;103m %d\033[0m", pr.Comments)
	}
	return fmt.Sprintf("%s \033[36m%s\033[0m \033[38;5;103m#%d\033[0m  %s %s%s",
		forge, pr.Repo, pr.Number, ci, review, comments)
}

// formatChangeTitle renders the indented second (title) line. Pure.
func formatChangeTitle(title string) string {
	if title == "" {
		return "\n  " + zeroWidthSpace
	}
	return "\n  \033[3;38;5;103m" + title + "\033[0m"
}

// ChangesListCommand emits change rows for fzf --reload (after a close).
func ChangesListCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_changes-list",
		Short:  "internal: emit Changes rows (for fzf --reload)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, r := range buildChangeRows() {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%d\t%s\t%s\t%s%s",
					r.Repo, r.Number, r.URL, r.Display, r.Title, fzf.NUL)
			}
			return nil
		},
	}
}

// OpenForgeCommand opens a PR in a browser (M-o / Enter).
func OpenForgeCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_open-forge <repo> <number> <url>",
		Short:  "internal: open a PR in a browser (M-o)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			forge := integration.Active().Forge
			if forge == nil {
				return nil
			}
			pr := prFromArgs(args)
			if err := forge.Open(pr); err != nil {
				debuglog.LogErr("workspaces._open-forge", err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// PRClosePromptCommand flips the Changes prompt to a close confirm.
func PRClosePromptCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_pr-close-prompt <fzf-prompt> <repo> <number>",
		Short:  "internal: M-c close confirm prompt",
		Hidden: true,
		Args:   cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.HasPrefix(args[0], "Close") {
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "change-prompt(Close %s #%s? y/n: )\n", args[1], args[2])
			return nil
		},
	}
}

// CloseForgeCommand closes a PR (the first mutating forge op; confirm-gated at
// the picker + [forge] allow_write in helpers). Marks the cached PR closed.
func CloseForgeCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_pr-close <repo> <number> <url>",
		Short:  "internal: close a PR (M-c, confirm-gated)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			if !forgeWriteAllowed() {
				return nil
			}
			forge := integration.Active().Forge
			if forge == nil {
				return nil
			}
			pr := prFromArgs(args)
			if err := forge.Close(pr); err != nil {
				debuglog.LogErr("workspaces._pr-close", err)
				return err
			}
			markPRClosed(pr.Repo, pr.Number)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// prFromArgs builds a PullRequest from the {1}{2}{3} bind args (repo, number, url).
func prFromArgs(args []string) integration.PullRequest {
	pr := integration.PullRequest{Repo: args[0]}
	if len(args) > 1 {
		pr.Number, _ = strconv.Atoi(strings.TrimSpace(args[1]))
	}
	if len(args) > 2 {
		pr.URL = args[2]
	}
	return pr
}

// markPRClosed updates the cached PR's state to closed so the reloaded list
// reflects the action immediately (before the next sweep).
func markPRClosed(repo string, number int) {
	st, err := statestore.Load()
	if err != nil || st == nil {
		return
	}
	for i := range st.Workspaces {
		ws := &st.Workspaces[i]
		changed := false
		for j := range ws.PRs {
			if ws.PRs[j].Repo == repo && ws.PRs[j].Number == number {
				ws.PRs[j].State = string(integration.ForgeClosed)
				changed = true
			}
		}
		if changed {
			prs := ws.PRs
			_ = statestore.UpdateWorkspace(ws.SessionName, func(w *statestore.Workspace) { w.PRs = prs })
		}
	}
}
