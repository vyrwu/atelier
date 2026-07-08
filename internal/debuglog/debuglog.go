// Package debuglog is atelier's always-on debug trace log. Every
// atelier-* binary writes to the same file (`$XDG_CACHE_HOME/atelier/debug.log`,
// default `~/.cache/atelier/debug.log`) so popup-spawned subprocesses
// interleave cleanly with their parents.
//
// Format:
//
//	2026-06-10T19:45:23.123 [atelier-workspaces:12345] <event> <fields...>
//
// Four categories of records:
//
//   - cmd: every `tmux <args>` (via tmuxhost.Client.Run) and instrumented
//     `git <args>` invocation. Records args, exit code, stdout/stderr
//     (truncated to 1KB), and wall-clock duration (`dur=Nms`).
//   - log: ad-hoc `Logf` calls from any package — decision points,
//     resolved state, branch markers.
//   - err: errors that propagate up but are otherwise discarded.
//   - perf: operation-level timing rollups from package perf — total
//     wall-clock plus a tmux/git/self breakdown for one logical op.
//
// Always-on, no env-var gate. Rotation: file is renamed to `.1` (and
// `.1` → `.2`) when it exceeds maxBytes. The rotated history is enough
// to debug the most recent reproduction without manual cleanup.
package debuglog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultRelPath = "atelier/debug.log"
	maxBytes       = 10 * 1024 * 1024 // 10MB
	keepRotations  = 2
)

var (
	mu      sync.Mutex
	out     *os.File
	binary  string
	pid     int
	initErr error
)

func ensureOpen() *os.File {
	mu.Lock()
	defer mu.Unlock()

	if out != nil {
		// Rotate if necessary.
		if info, err := out.Stat(); err == nil && info.Size() > maxBytes {
			path := out.Name()
			_ = out.Close()
			rotate(path)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				initErr = err
				out = nil
				return nil
			}
			out = f
		}
		return out
	}
	if initErr != nil {
		return nil
	}

	dir := cacheRoot()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		initErr = err
		return nil
	}
	path := filepath.Join(dir, "debug.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		initErr = err
		return nil
	}
	out = f
	binary = baseName(os.Args[0])
	pid = os.Getpid()
	return out
}

func cacheRoot() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "atelier")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "atelier")
}

// Path returns the resolved debug.log path (used by `atelier debug tail`).
func Path() string {
	return filepath.Join(cacheRoot(), "debug.log")
}

func rotate(path string) {
	for i := keepRotations; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", path, i-1)
		if i == 1 {
			from = path
		}
		to := fmt.Sprintf("%s.%d", path, i)
		if _, err := os.Stat(from); err == nil {
			_ = os.Rename(from, to)
		}
	}
}

// Logf writes an ad-hoc decision-point trace. Use for explaining branch
// choices: "outerClient=X, taking -c path".
func Logf(format string, args ...interface{}) {
	if isHotPathBinary() {
		return
	}
	f := ensureOpen()
	if f == nil {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(f, "%s [%s:%d] log %s\n", now, binary, pid, msg)
}

// isHotPathBinary returns true for invocations that run on tmux's
// status-line refresh tick (~1Hz per cell), which would otherwise
// flood the log and bury launcher / restore / hook traces. Detected
// via argv[1] == "status" — the `atelier status *` family.
//
// Override with ATELIER_STATUSLINE_TRACE=1 to re-enable status logging
// when actively debugging the statusline pipeline itself.
func isHotPathBinary() bool {
	if os.Getenv("ATELIER_STATUSLINE_TRACE") != "" {
		return false
	}
	return len(os.Args) >= 2 && os.Args[1] == "status"
}

// LogCmd records a tmux invocation: args (joined with single spaces), the
// combined output (truncated to 1KB), the exit code derived from err, and
// how long the call took (for perf diagnosis of slow operations).
func LogCmd(args []string, output []byte, err error, dur time.Duration) {
	logExec("tmux", strings.Join(args, " "), output, err, dur)
}

// LogGitCmd records a git invocation the same way as LogCmd. dir (when
// non-empty) is shown as a leading `-C <dir>` so the log reflects which
// repo the call hit — the session-list picker fans out one-to-many git
// calls across repos and that context is what makes a slow build legible.
func LogGitCmd(dir string, args []string, output []byte, err error, dur time.Duration) {
	argStr := strings.Join(args, " ")
	if dir != "" {
		argStr = "-C " + dir + " " + argStr
	}
	logExec("git", argStr, output, err, dur)
}

// logExec is the shared formatter behind LogCmd/LogGitCmd.
func logExec(tool, argStr string, output []byte, err error, dur time.Duration) {
	if isHotPathBinary() {
		return
	}
	f := ensureOpen()
	if f == nil {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000")
	outStr := string(output)
	if len(outStr) > 1024 {
		outStr = outStr[:1024] + "…(truncated)"
	}
	outStr = strings.ReplaceAll(outStr, "\n", "\\n")
	mu.Lock()
	defer mu.Unlock()
	status := "ok"
	if err != nil {
		status = "ERR " + err.Error()
		if len(status) > 200 {
			status = status[:200] + "…"
		}
	}
	fmt.Fprintf(f, "%s [%s:%d] cmd %s %s → %s dur=%dms | %s\n",
		now, binary, pid, tool, argStr, status, dur.Milliseconds(), outStr)
}

// LogPerf records a perf-span rollup (formatted by package perf). The
// message already carries the operation name and its timing breakdown;
// this just stamps the shared prefix and `perf` category.
func LogPerf(msg string) {
	if isHotPathBinary() {
		return
	}
	f := ensureOpen()
	if f == nil {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000")
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(f, "%s [%s:%d] perf %s\n", now, binary, pid, msg)
}

// LogErr records an error with a context label. Use at error-discard
// sites (`_, _ = h.Run(...)`) so silent failures still leave a trace.
func LogErr(context string, err error) {
	if err == nil {
		return
	}
	if isHotPathBinary() {
		return
	}
	f := ensureOpen()
	if f == nil {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000")
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(f, "%s [%s:%d] err %s: %v\n", now, binary, pid, context, err)
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
