package operator

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/core"
)

const MaxWait = 30 * time.Minute

func DefaultWait(agentName string) time.Duration {
	if agentName == "grok" {
		return 50 * time.Second
	}
	return 5 * time.Minute
}

func statusesOf(ws []core.Workspace, live map[string]bool) map[string]core.AgentStatus {
	out := map[string]core.AgentStatus{}
	for _, w := range ws {
		if !w.IsMaitre() && !w.Retired {
			out[w.Slug] = agent.Status(w.Session, live[w.Session])
		}
	}
	return out
}

// waitChanged compares successive snapshots. Already-idle spaces do not wake
// the caller; new spaces and transitions back to idle or blocked do.
func waitChanged(ctx context.Context, every time.Duration, snap func() (map[string]core.AgentStatus, error)) ([]string, error) {
	before, err := snap()
	if err != nil {
		return nil, err
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return []string{}, nil
		case <-tick.C:
			after, err := snap()
			if err != nil {
				return nil, err
			}
			changed := []string{}
			for slug, status := range after {
				if (status == core.StatusBlocked || status == core.StatusIdle) && before[slug] != status {
					changed = append(changed, slug)
				}
			}
			if len(changed) > 0 {
				sort.Strings(changed)
				return changed, nil
			}
			before = after
		}
	}
}

func Wait(timeout time.Duration) ([]Space, error) {
	if timeout < 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	if timeout == 0 {
		timeout = DefaultWait(os.Getenv("ATELIER_AGENT"))
	}
	timeout = min(timeout, MaxWait)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	changed, err := waitChanged(ctx, time.Second, func() (map[string]core.AgentStatus, error) {
		st, err := core.Read()
		if err != nil {
			return nil, err
		}
		live, err := liveSessions()
		if err != nil {
			return nil, err
		}
		return statusesOf(st.Workspaces, live), nil
	})
	if err != nil {
		return nil, err
	}
	out := []Space{}
	if len(changed) == 0 {
		return out, nil
	}
	spaces, err := List(false)
	if err != nil {
		return nil, err
	}
	for _, s := range spaces {
		if slices.Contains(changed, s.Slug) {
			out = append(out, s)
		}
	}
	return out, nil
}
