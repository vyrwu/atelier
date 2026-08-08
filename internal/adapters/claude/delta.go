package claude

import (
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The recap is grounded in the worktree's git delta, not just the Claude
// conversation. The transcript says what the session TALKED about; the delta
// says what actually changed on disk — the two can diverge (a session that
// plans but is interrupted before writing code; code written that the chat
// never narrates). Feeding both to the summarizer keeps the recap honest.
// The caps below are NOT a token-budget concern — haiku is cheap and a rich
// delta makes a better recap, so we feed it generously. They exist only to (a)
// avoid buffering a pathologically huge diff into memory (a regenerated
// lockfile / vendored tree can be megabytes) and (b) keep a ONE-LINE recap
// focused — past a point the --stat/name-status already conveys scope and more
// patch text is just noise.
const (
	deltaGitTimeout = 5 * time.Second
	// maxDeltaRunes caps the whole delta block handed to the summarizer.
	maxDeltaRunes = 12000
	// maxPatchRunes caps the raw-patch portion within the delta.
	maxPatchRunes = 10000
	// maxPatchChangedLines skips the raw patch only for genuinely huge diffs
	// (memory guard) — the file list + stat still convey magnitude there.
	maxPatchChangedLines = 20000
)

// worktreeDelta returns a compact, bounded description of what changed in the
// git worktree at cwd: uncommitted work, plus the branch's changes vs its base
// (changed files + a bounded patch when the diff is small). Best-effort — it
// returns "" when cwd is empty, not a git worktree, or has no base to diff
// against (a bare default-branch repo), and the recap then falls back to the
// conversation alone.
func worktreeDelta(cwd string) string {
	if cwd == "" {
		return ""
	}
	var b strings.Builder
	if s := gitOut(cwd, "diff", "--stat", "HEAD"); s != "" {
		b.WriteString("Uncommitted (working tree vs HEAD):\n")
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	if base := branchBase(cwd); base != "" {
		if s := gitOut(cwd, "diff", "--name-status", base+"...HEAD"); s != "" {
			b.WriteString("Branch changes vs " + base + ":\n")
			b.WriteString(s)
			b.WriteString("\n")
		}
		if p := branchPatch(cwd, base); p != "" {
			b.WriteString("\nPatch:\n")
			b.WriteString(p)
			b.WriteString("\n")
		}
	}
	return capRunes(strings.TrimSpace(b.String()), maxDeltaRunes)
}

// branchBase resolves the ref this branch is diffed against: origin/HEAD's
// target if known, else the conventional defaults. "" when none resolve.
func branchBase(cwd string) string {
	if head := gitLine(cwd, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); head != "" {
		return head // e.g. "origin/main"
	}
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		if gitLine(cwd, "rev-parse", "--verify", "--quiet", ref) != "" {
			return ref
		}
	}
	return ""
}

// branchPatch returns a bounded unified patch of the branch vs base, or "" when
// the diff is too large. Two independent bounds, because they catch different
// pathologies: the line-count gate skips a normal-but-huge diff (many files)
// cheaply, while gitOutLimited caps BYTES read — a few megabyte-long lines
// (minified bundles, base64 blobs) sail under the line gate but must not be
// buffered whole just to truncate.
func branchPatch(cwd, base string) string {
	if changedLines(gitOut(cwd, "diff", "--shortstat", base+"...HEAD")) > maxPatchChangedLines {
		return ""
	}
	return capRunes(gitOutLimited(cwd, maxPatchRunes*4, "diff", "-U1", base+"...HEAD"), maxPatchRunes)
}

// changedLines sums insertions + deletions out of a `--shortstat` line like
// " 3 files changed, 12 insertions(+), 4 deletions(-)". 0 when unparseable.
func changedLines(shortstat string) int {
	total := 0
	toks := strings.Fields(shortstat)
	for i, t := range toks {
		if i > 0 && (strings.HasPrefix(t, "insertion") || strings.HasPrefix(t, "deletion")) {
			if n, err := strconv.Atoi(toks[i-1]); err == nil {
				total += n
			}
		}
	}
	return total
}

func capRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n…(truncated)"
}

// gitOut runs git in dir under a short deadline and returns trimmed stdout, or
// "" on any error (not a repo, bad ref, timeout). Best-effort by design — a
// failed delta lookup degrades the recap to conversation-only, never errors.
func gitOut(dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), deltaGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitLine(dir string, args ...string) string {
	s := gitOut(dir, args...)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// gitOutLimited is gitOut that reads AT MOST maxBytes of stdout, then kills git
// — so a pathologically large diff (a few megabyte-long lines) is never fully
// buffered into memory just to be truncated afterward. maxBytes is sized in
// bytes; callers rune-cap the (already small) result.
func gitOutLimited(dir string, maxBytes int, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), deltaGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return ""
	}
	if err := cmd.Start(); err != nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(pipe, int64(maxBytes)))
	// Stop git and reap it. Cancelling kills the process (CommandContext), so a
	// git still streaming a huge diff doesn't block on the now-unread pipe.
	cancel()
	_ = cmd.Wait()
	return strings.TrimSpace(string(data))
}
