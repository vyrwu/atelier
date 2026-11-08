package operator

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/vyrwu/atelier/internal/agent"
	"github.com/vyrwu/atelier/internal/gc"
)

func firstLine(r io.Reader, timeout time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		sc := bufio.NewScanner(r)
		if sc.Scan() && strings.TrimSpace(sc.Text()) != "" {
			done <- result{line: strings.TrimSpace(sc.Text())}
			return
		}
		err := sc.Err()
		if err == nil {
			err = fmt.Errorf("create exited without a space slug")
		}
		done <- result{err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.line, r.err
	case <-timer.C:
		return "", fmt.Errorf("timed out waiting for a space to start")
	}
}

func Create(intent, agentName string) (string, error) {
	if strings.TrimSpace(intent) == "" {
		return "", fmt.Errorf("intent is required")
	}
	if agentName != "" && !agent.Valid(agentName) {
		return "", fmt.Errorf("unknown agent %q", agentName)
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	args := []string{"create", intent}
	if agentName != "" {
		args = append(args, "--agent", agentName)
	}
	// #nosec G204 G702 -- Runs this executable with an argument array, never a shell.
	c := exec.Command(self, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	stdout, err := c.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := c.Start(); err != nil {
		return "", err
	}
	slug, err := firstLine(stdout, time.Minute)
	if err != nil {
		_ = c.Process.Kill()
		_ = stdout.Close()
		_ = c.Wait()
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	// Naming continues detached; drain and reap without delaying the tool result.
	go func() { _, _ = io.Copy(io.Discard, stdout); _ = c.Wait() }()
	return slug, nil
}

func run(args ...string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	// #nosec G204 G702 -- Runs this executable with validated CLI arguments, without a shell.
	out, err := exec.Command(self, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("atelier %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func Swap(slug, to string) (string, error) {
	if !agent.Valid(to) {
		return "", fmt.Errorf("unknown agent %q", to)
	}
	if _, err := find(slug, wantActive); err != nil {
		return "", err
	}
	return run("swap", slug, "--to", to)
}

func Purge(olderThan string, all bool) (string, error) {
	if _, err := gc.ParseAge(olderThan); err != nil {
		return "", err
	}
	args := []string{"purge", "--older-than", olderThan, "--yes"}
	if all {
		args = append(args, "--all")
	}
	return run(args...)
}
