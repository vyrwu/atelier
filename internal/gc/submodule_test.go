package gc

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/core"
)

func TestUnsavedSubmoduleCommits(t *testing.T) {
	root := sandbox(t)
	src, moduleSrc := repo(t), repo(t)
	wt := space(t, root, "modules", src, "feat-modules")
	moduleOrigin := run(t, moduleSrc, "remote", "get-url", "origin")
	run(t, wt, "-c", "protocol.file.allow=always", "submodule", "add", "-q", moduleOrigin, "module with spaces")
	module := filepath.Join(wt, "module with spaces")
	run(t, module, "commit", "-q", "--allow-empty", "-m", "private submodule work")
	run(t, wt, "add", ".")
	run(t, wt, "commit", "-q", "-m", "point at submodule work")
	// Pushing a superproject does not publish the commits its gitlinks name.
	run(t, wt, "-c", "push.recurseSubmodules=no", "push", "-q", "origin", "feat-modules")
	w := core.Workspace{Slug: "modules", Session: "modules"}
	if got := Unsaved(w); len(got) != 1 || !strings.Contains(got[0], "submodule") || !strings.Contains(got[0], "unpushed") {
		t.Fatalf("unpublished submodule commit: %v; want unsaved work", got)
	}
	// A cached PR head protects the superproject, not unpublished work in its
	// modules, even after the parent branch has been deleted remotely.
	w.PRs = []core.PR{{HeadOID: run(t, wt, "rev-parse", "HEAD")}}
	run(t, wt, "push", "-q", "origin", "--delete", "feat-modules")
	if got := Unsaved(w); len(got) != 1 || !strings.Contains(got[0], "submodule") {
		t.Fatalf("published PR with unpublished submodule commit: %v", got)
	}
	run(t, module, "push", "-q", "origin", "HEAD:main")
	if got := Unsaved(w); len(got) != 0 {
		t.Fatalf("published submodule: %v", got)
	}
	run(t, wt, "submodule", "deinit", "-f", "--", "module with spaces")
	if got := Unsaved(w); len(got) != 0 {
		t.Fatalf("uninitialized published submodule: %v", got)
	}
}
