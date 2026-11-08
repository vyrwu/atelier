package operator

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
	"github.com/vyrwu/atelier/internal/gc"
	"github.com/vyrwu/atelier/internal/git"
	"github.com/vyrwu/atelier/internal/tmux"
)

type Space struct {
	Slug        string           `json:"slug"`
	Title       string           `json:"title"`
	Intent      string           `json:"intent"`
	Agent       string           `json:"agent"`
	Status      core.AgentStatus `json:"status"`
	Retired     bool             `json:"retired,omitempty"`
	IdleSeconds int64            `json:"idle_seconds"`
	Worktrees   []string         `json:"worktrees"`
	PRs         []core.PR        `json:"prs"`
}

// Build assembles a local snapshot, ordered by attention and then newest first.
func Build(ws []core.Workspace, trash bool, status func(core.Workspace) core.AgentStatus, worktrees func(core.Workspace) []string, now time.Time) []Space {
	out := []Space{}
	created := map[string]time.Time{}
	for _, w := range ws {
		if w.IsMaitre() || w.Retired != trash {
			continue
		}
		idle := max(int64(0), int64(now.Sub(gc.LastActive(w)).Seconds()))
		out = append(out, Space{Slug: w.Slug, Title: w.Title, Intent: w.Intent, Agent: string(agent.Of(w.Agent)), Status: status(w), Retired: w.Retired, IdleSeconds: idle, Worktrees: append([]string{}, worktrees(w)...), PRs: append([]core.PR{}, w.PRs...)})
		created[w.Slug] = w.Created
	}
	rank := map[core.AgentStatus]int{core.StatusBlocked: 0, core.StatusIdle: 1, core.StatusWorking: 2, core.StatusGone: 3}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Status] != rank[out[j].Status] {
			return rank[out[i].Status] < rank[out[j].Status]
		}
		return created[out[i].Slug].After(created[out[j].Slug])
	})
	return out
}

func liveSessions() (map[string]bool, error) {
	sessions, err := tmux.ReadSessions()
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, s := range sessions {
		live[s] = true
	}
	return live, nil
}

func List(trash bool) ([]Space, error) {
	st, err := core.Read()
	if err != nil {
		return nil, err
	}
	live, err := liveSessions()
	if err != nil {
		return nil, err
	}
	return Build(st.Workspaces, trash, func(w core.Workspace) core.AgentStatus {
		return agent.Status(w.Session, live[w.Session])
	}, func(w core.Workspace) []string {
		var out []string
		for _, path := range git.WorktreeDirs(w.Root()) {
			if rel, err := filepath.Rel(w.Root(), path); err == nil {
				out = append(out, rel)
			}
		}
		return out
	}, time.Now()), nil
}

func FormatTable(out io.Writer, spaces []Space) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SPACE\tSTATUS\tAGENT\tIDLE\tPRS\tTITLE")
	for _, s := range spaces {
		prs := []string{}
		for _, p := range s.PRs {
			prs = append(prs, fmt.Sprintf("%s#%d:%s/%s", p.Repo, p.Number, p.State, p.CI))
		}
		// State may contain an old multiline generated title; keep one row per space.
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Slug, s.Status, s.Agent, FormatAge(time.Duration(s.IdleSeconds)*time.Second), strings.Join(prs, ","), strings.Join(strings.Fields(s.Title), " "))
	}
	return tw.Flush()
}

// FormatAge renders idle age compactly for the CLI tables.
func FormatAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
