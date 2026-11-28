package ui

import (
	"os"
	"testing"
)

// The lists count the worktrees under each space's directory; no test may walk
// the real spaces.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "atelier-ui-test")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("ATELIER_ROOT", root)
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
