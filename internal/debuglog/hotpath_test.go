package debuglog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHotPathBinary_SuppressesStatusEmitters locks the contract:
// when os.Args[1]=="status" (the high-frequency statusline emitter
// path that fires ~1Hz per cell), Logf/LogCmd/LogErr write NOTHING
// to debug.log. Without this filter the launch trace was buried
// under per-tick noise — unusable for diagnostics.
func TestHotPathBinary_SuppressesStatusEmitters(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("ATELIER_STATUSLINE_TRACE", "")

	resetState(t)
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })
	os.Args = []string{"atelier", "status", "freshness"}

	Logf("should not appear")
	LogCmd([]string{"list-windows"}, []byte("zsh"), nil)
	LogErr("ctx", os.ErrNotExist)

	logPath := filepath.Join(tmp, "atelier", "debug.log")
	if _, err := os.Stat(logPath); err == nil {
		data, _ := os.ReadFile(logPath)
		if len(data) > 0 {
			t.Fatalf("status-emitter wrote to debug.log:\n%s", data)
		}
	}
}

// TestHotPathBinary_PassesThroughNonStatusBinaries: the default
// atelier launch (argv[1] != "status") and `atelier internal *`
// hook paths MUST still log normally.
func TestHotPathBinary_PassesThroughNonStatusBinaries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	resetState(t)
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })
	os.Args = []string{"atelier"}

	Logf("launch trace must appear")

	logPath := filepath.Join(tmp, "atelier", "debug.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "launch trace must appear") {
		t.Fatalf("expected log entry missing; got:\n%s", data)
	}
}

// TestHotPathBinary_TraceEnvOverridesSuppression: when the user
// sets ATELIER_STATUSLINE_TRACE=1, they explicitly want the
// statusline path traced — suppression must yield.
func TestHotPathBinary_TraceEnvOverridesSuppression(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("ATELIER_STATUSLINE_TRACE", "1")

	resetState(t)
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })
	os.Args = []string{"atelier", "status", "freshness"}

	Logf("trace-mode entry")

	logPath := filepath.Join(tmp, "atelier", "debug.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "trace-mode entry") {
		t.Fatalf("trace-mode entry suppressed despite ATELIER_STATUSLINE_TRACE=1:\n%s", data)
	}
}

// resetState clears the package-level singleton so each test gets
// a fresh log file under its own XDG_CACHE_HOME. The debuglog
// package memoizes the file handle on first use; without this
// reset, the second test in a run would write to the first's tmp.
func resetState(t *testing.T) {
	t.Helper()
	mu.Lock()
	if out != nil {
		_ = out.Close()
	}
	out = nil
	initErr = nil
	mu.Unlock()
}
