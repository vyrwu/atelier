package toolmain

import (
	"os"
	"os/exec"
	"testing"

	"github.com/spf13/cobra"

	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/manifest"
)

// TestMain re-invokes the test binary as a toolmain subprocess when the
// helper env var is set. This is how we test exit codes of toolmain.Run
// without spawning a separate binary build.
func TestMain(m *testing.M) {
	switch os.Getenv("ATELIER_TOOLMAIN_TEST_MODE") {
	case "cancel":
		Run(&manifest.Manifest{APIVersion: manifest.APIVersion, Name: "testtool"},
			func(root *cobra.Command) {
				root.AddCommand(&cobra.Command{
					Use: "do",
					RunE: func(*cobra.Command, []string) error {
						return fzf.ErrCancelled
					},
				})
			})
		return // unreachable: Run calls os.Exit
	case "ok":
		Run(&manifest.Manifest{APIVersion: manifest.APIVersion, Name: "testtool"},
			func(root *cobra.Command) {
				root.AddCommand(&cobra.Command{
					Use:  "do",
					RunE: func(*cobra.Command, []string) error { return nil },
				})
			})
		return
	}
	os.Exit(m.Run())
}

// TestRun_ExitsCancelledOn130 locks in the cancellation propagation fix:
// when RunE returns fzf.ErrCancelled, toolmain MUST exit with code 130
// (fzf's cancel status) so a parent fzf.Pick reading our exit status
// up the become() chain returns ErrCancelled and the chain unwinds
// cleanly. Returning nil (exit 0) caused upstream callers to see "valid
// pick of empty output" and proceed with phantom state.
func TestRun_ExitsCancelledOn130(t *testing.T) {
	cmd := exec.Command(os.Args[0], "do")
	cmd.Env = append(os.Environ(), "ATELIER_TOOLMAIN_TEST_MODE=cancel")
	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %v", err)
	}
	if exitErr.ExitCode() != 130 {
		t.Errorf("ErrCancelled should map to exit 130, got %d", exitErr.ExitCode())
	}
}

// TestRun_ExitsZeroOnNil sanity-checks that the new cancellation
// handling didn't accidentally elevate every successful run to 130.
func TestRun_ExitsZeroOnNil(t *testing.T) {
	cmd := exec.Command(os.Args[0], "do")
	cmd.Env = append(os.Environ(), "ATELIER_TOOLMAIN_TEST_MODE=ok")
	if err := cmd.Run(); err != nil {
		t.Errorf("nil RunE should exit 0, got %v", err)
	}
}
