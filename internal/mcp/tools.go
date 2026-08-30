package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vyrwu/atelier/internal/core"
)

// createWorktreeSpec is the create_worktree tool catalog entry.
func createWorktreeSpec() map[string]interface{} {
	return map[string]interface{}{
		"name": "create_worktree",
		"description": "Create a git worktree for a repository inside THIS atelier workspace, " +
			"branched off the repository's latest default branch (fetched fresh, never stale). " +
			"Use this instead of `git worktree add` so the worktree lands where atelier can see it. " +
			"Returns the worktree path to work in.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"source": map[string]interface{}{"type": "string", "description": "absolute path to the existing source repository (e.g. ~/code/github/org/repo)"},
				"branch": map[string]interface{}{"type": "string", "description": "name of the new branch to create for this work"},
			},
			"required": []string{"source", "branch"},
		},
	}
}

// createPRSpec is the create_pr tool catalog entry.
func createPRSpec() map[string]interface{} {
	return map[string]interface{}{
		"name": "create_pr",
		"description": "Open a pull request for a worktree in this workspace. Rebases the branch onto the " +
			"latest default branch first (so the PR is never behind), pushes, and opens a DRAFT PR. " +
			"If the rebase conflicts, it reports them — resolve and call again.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path":  map[string]interface{}{"type": "string", "description": "path to the worktree directory to open the PR from"},
				"title": map[string]interface{}{"type": "string", "description": "PR title (optional; derived from commits if omitted)"},
				"body":  map[string]interface{}{"type": "string", "description": "PR body (optional)"},
				"ready": map[string]interface{}{"type": "boolean", "description": "open as ready-for-review instead of draft (default false → draft)"},
			},
			"required": []string{"path"},
		},
	}
}

func handleCreateWorktree(raw json.RawMessage) map[string]interface{} {
	var a struct {
		Source string `json:"source"`
		Branch string `json:"branch"`
	}
	if json.Unmarshal(raw, &a) != nil {
		return textResult("invalid arguments", true)
	}
	a.Source = strings.TrimSpace(a.Source)
	a.Branch = strings.TrimSpace(a.Branch)
	if a.Source == "" || a.Branch == "" {
		return textResult("source and branch are required", true)
	}
	ws, ok := resolveWorkspace()
	if !ok {
		return textResult("no atelier workspace for this session", true)
	}
	source, err := filepath.Abs(expandHome(a.Source))
	if err != nil {
		return textResult("bad source path: "+err.Error(), true)
	}
	if !isGitRepo(source) {
		return textResult(source+" is not a git repository", true)
	}

	def := defaultBranch(source)
	// Never stale: refresh the default branch tip before branching off it.
	if out, err := gitAuthed(source, "fetch", "--quiet", "origin", def); err != nil {
		return textResult("fetch origin "+def+" failed: "+out, true)
	}
	target := filepath.Join(ws.Root(), filepath.Base(source), a.Branch)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return textResult("mkdir failed: "+err.Error(), true)
	}
	if out, err := runGit(source, "worktree", "add", "-b", a.Branch, target, "origin/"+def); err != nil {
		// Branch may already exist — attach a worktree for it instead.
		if _, err2 := runGit(source, "worktree", "add", target, a.Branch); err2 == nil {
			return textResult(fmt.Sprintf("worktree for existing branch %q at %s", a.Branch, target), false)
		}
		return textResult("worktree add failed: "+out, true)
	}
	return textResult(fmt.Sprintf("created worktree at %s — branch %q off latest %s", target, a.Branch, def), false)
}

func handleCreatePR(raw json.RawMessage) map[string]interface{} {
	var a struct {
		Path  string `json:"path"`
		Title string `json:"title"`
		Body  string `json:"body"`
		Ready bool   `json:"ready"`
	}
	if json.Unmarshal(raw, &a) != nil {
		return textResult("invalid arguments", true)
	}
	a.Path = strings.TrimSpace(a.Path)
	if a.Path == "" {
		return textResult("path (the worktree directory) is required", true)
	}
	ws, ok := resolveWorkspace()
	if !ok {
		return textResult("no atelier workspace for this session", true)
	}
	path, err := filepath.Abs(expandHome(a.Path))
	if err != nil || !isGitRepo(path) {
		return textResult("path is not a git worktree", true)
	}

	branch := currentBranch(path)
	if branch == "" || branch == "HEAD" {
		return textResult("worktree is not on a branch", true)
	}
	def := defaultBranch(path)
	if branch == def {
		return textResult("refusing to open a PR from the default branch", true)
	}

	// Never behind: rebase onto the latest default branch before pushing.
	if out, err := gitAuthed(path, "fetch", "--quiet", "origin", def); err != nil {
		return textResult("fetch origin "+def+" failed: "+out, true)
	}
	if out, err := runGit(path, "rebase", "origin/"+def); err != nil {
		_, _ = runGit(path, "rebase", "--abort")
		return textResult("rebase onto origin/"+def+" hit conflicts — resolve them and call create_pr again:\n"+out, true)
	}
	if out, err := gitAuthed(path, "push", "--force-with-lease", "-u", "origin", branch); err != nil {
		return textResult("push failed: "+out, true)
	}

	args := []string{"pr", "create", "--base", def, "--head", branch}
	if a.Ready {
		// ready for review
	} else {
		args = append(args, "--draft")
	}
	if a.Title != "" {
		args = append(args, "--title", a.Title, "--body", a.Body)
	} else {
		args = append(args, "--fill")
	}
	out, err := runGH(path, args...)
	if err != nil {
		return textResult("gh pr create failed: "+out, true)
	}
	url := firstPRURL(out)
	if repo, num, ok := parsePRURL(url); ok {
		_ = registerPR(ws.Slug, core.PR{
			Repo: repo, Number: num, Title: a.Title, URL: url,
			State: draftOrOpen(a.Ready), Registered: true,
		})
	}
	return textResult("opened PR: "+url, false)
}

// --- git / gh helpers -------------------------------------------------------

func runGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// gitAuthed runs a git network operation using gh's token over HTTPS instead of
// whatever the remote is configured for. It rewrites github SSH remotes to HTTPS
// for this one command and supplies credentials via `gh auth git-credential`, so
// atelier's fetch/push/ls-remote never touch the user's SSH key or the macOS
// keychain (which would pop a GUI passphrase prompt from this background
// context). Scoped per-invocation: the repo's stored remote URL is unchanged.
func gitAuthed(dir string, args ...string) (string, error) {
	gh := "gh"
	if p, err := exec.LookPath("gh"); err == nil {
		gh = p
	}
	pre := []string{
		"-c", "url.https://github.com/.insteadOf=ssh://git@github.com/",
		"-c", "url.https://github.com/.insteadOf=git@github.com:",
		"-c", "credential.helper=", // reset (drop osxkeychain) …
		"-c", "credential.helper=!" + gh + " auth git-credential", // … then use gh's token
	}
	return runGit(dir, append(pre, args...)...)
}

func runGH(dir string, args ...string) (string, error) {
	c := exec.Command("gh", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func isGitRepo(dir string) bool {
	_, err := runGit(dir, "rev-parse", "--git-dir")
	return err == nil
}

func currentBranch(dir string) string {
	out, err := runGit(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// defaultBranch reports the repo's default branch, preferring the remote's
// advertised HEAD (authoritative even when origin/HEAD isn't set locally) and
// falling back to the local origin/HEAD, then "main".
func defaultBranch(dir string) string {
	if out, err := gitAuthed(dir, "ls-remote", "--symref", "origin", "HEAD"); err == nil {
		for _, ln := range strings.Split(out, "\n") {
			if strings.HasPrefix(ln, "ref:") {
				if f := strings.Fields(ln); len(f) >= 2 {
					return strings.TrimPrefix(f[1], "refs/heads/")
				}
			}
		}
	}
	if out, err := runGit(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(out, "origin/")
	}
	return "main"
}

// firstPRURL returns the first github PR URL in s (gh prints it on success),
// falling back to the last non-empty line.
func firstPRURL(s string) string {
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "https://") && strings.Contains(f, "/pull/") {
			return f
		}
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// parsePRURL extracts owner/repo and number from a github PR URL.
func parsePRURL(url string) (string, int, bool) {
	i := strings.Index(url, "github.com/")
	if i < 0 {
		return "", 0, false
	}
	parts := strings.Split(url[i+len("github.com/"):], "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return "", 0, false
	}
	n, err := strconv.Atoi(strings.SplitN(parts[3], "?", 2)[0])
	if err != nil {
		return "", 0, false
	}
	return parts[0] + "/" + parts[1], n, true
}

func draftOrOpen(ready bool) core.PRState {
	if ready {
		return core.PROpen
	}
	return core.PRDraft
}

// expandHome resolves a leading ~ so the agent can pass ~/code/... paths.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
