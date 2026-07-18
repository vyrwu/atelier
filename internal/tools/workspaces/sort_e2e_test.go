//go:build e2e

package workspaces_test

import (
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/testtmux"
)

// TestSortNext_CyclesGlobalAndEmitsActions locks in the Tab-cycle
// contract: _sort-next advances the persisted @ms_sort global to the next
// mode and prints the fzf action string (reload + change-footer with the
// new mode's legend) that the transform bind feeds back to fzf.
func TestSortNext_CyclesGlobalAndEmitsActions(t *testing.T) {
	srv := testtmux.New(t)
	srv.NewSession("main")

	// Unset global → the picker defaults to Attention; the first Tab
	// advances to Age.
	steps := []struct {
		wantGlobal string
		wantLegend string
	}{
		{"age", "Sort: Age"},
		{"repo", "Sort: Repo"},
		{"tag", "Sort: Tag"},
		{"forge", "Sort: Forge"},
		{"attention", "Sort: Attention"}, // wraps back to the default
	}
	for i, step := range steps {
		out, err := srv.RunAtelier("tools", "workspaces", "_sort-next")
		if err != nil {
			t.Fatalf("step %d _sort-next: %v\n%s", i, err, out)
		}
		got := string(out)
		if !strings.Contains(got, "reload(") {
			t.Errorf("step %d: output missing reload action; got %q", i, got)
		}
		// The footer carries a yellow ANSI prefix between change-footer( and
		// the legend, so assert the two parts independently.
		if !strings.Contains(got, "change-footer(") || !strings.Contains(got, step.wantLegend) {
			t.Errorf("step %d: output missing change-footer with %q; got %q", i, step.wantLegend, got)
		}
		v, err := srv.Client.ShowGlobalOption("@ms_sort")
		if err != nil {
			t.Fatalf("step %d ShowGlobalOption: %v", i, err)
		}
		if v != step.wantGlobal {
			t.Errorf("step %d: @ms_sort = %q, want %q", i, v, step.wantGlobal)
		}
	}
}
