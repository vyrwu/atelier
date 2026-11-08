package operator

import (
	"encoding/json"
	"github.com/vyrwu/atelier/internal/core"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	now := time.Now()
	ws := []core.Workspace{{Slug: "a", Created: now.Add(-3 * time.Hour)}, {Slug: "b", Created: now.Add(-time.Hour)}, {Slug: "c", Created: now.Add(-2 * time.Hour)}, {Slug: "r", Retired: true}, {Slug: core.MaitreSlug}}
	status := func(w core.Workspace) core.AgentStatus {
		return map[string]core.AgentStatus{"a": core.StatusIdle, "b": core.StatusBlocked, "c": core.StatusWorking}[w.Slug]
	}
	wt := func(w core.Workspace) []string {
		if w.Slug == "a" {
			return []string{"repo/fix"}
		}
		return nil
	}
	got := Build(ws, false, status, wt, now)
	var slugs []string
	for _, s := range got {
		slugs = append(slugs, s.Slug)
	}
	if !reflect.DeepEqual(slugs, []string{"b", "a", "c"}) {
		t.Fatal(slugs)
	}
	if got[1].Worktrees[0] != "repo/fix" || got[1].IdleSeconds != 10800 {
		t.Fatal(got[1])
	}
	data, err := json.Marshal(got[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"prs":[]`) || !strings.Contains(string(data), `"worktrees":[]`) {
		t.Fatal(string(data))
	}
	trash := Build(ws, true, status, wt, now)
	if len(trash) != 1 || trash[0].Slug != "r" {
		t.Fatal(trash)
	}
}
