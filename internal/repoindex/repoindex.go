// Package repoindex scans a code root for git repositories, producing a
// compact index for the intent-first workspace creator (WS-3 option a:
// AI-selected repos from a repo index).
//
// The scan walks two levels deep (owner/repo) to match atelier's
// ~/code/github/<owner>/<repo> convention, reusing the same depth==2
// filepath.WalkDir shape as workspaces.PickCommand so behavior matches.
package repoindex

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Repo is one discovered repository.
type Repo struct {
	Slug string // "owner/repo" (the path relative to the code root)
	Path string // absolute path to the repo
}

// Scan walks codeRoot two levels deep (owner/repo, matching atelier's
// ~/code/github/<owner>/<repo> convention) and returns every dir that is a
// git repo (has a .git entry). Sorted by Slug. Best-effort: unreadable
// subdirs are skipped, a missing codeRoot returns (nil, nil).
func Scan(codeRoot string) ([]Repo, error) {
	if _, err := os.Stat(codeRoot); err != nil {
		// Missing (or otherwise unstat-able) root: best-effort nil.
		return nil, nil
	}

	var repos []Repo
	err := filepath.WalkDir(codeRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// Unreadable subdir: skip it, keep walking.
			return nil
		}
		rel, _ := filepath.Rel(codeRoot, p)
		if rel == "." {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		depth := strings.Count(rel, string(os.PathSeparator)) + 1
		if depth == 2 {
			// owner/repo candidate: keep only if it's a git repo.
			if isGitRepo(p) {
				abs, aerr := filepath.Abs(p)
				if aerr != nil {
					abs = p
				}
				repos = append(repos, Repo{Slug: filepath.ToSlash(rel), Path: abs})
			}
			return filepath.SkipDir
		}
		if depth >= 2 {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(repos, func(i, j int) bool { return repos[i].Slug < repos[j].Slug })
	return repos, nil
}

// isGitRepo reports whether dir contains a .git entry (dir or file, to cover
// worktrees/submodules whose .git is a gitlink file).
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// Format renders the index as a compact newline-delimited "owner/repo" list
// suitable for feeding to an AI naming call as context. Pure.
func Format(repos []Repo) string {
	if len(repos) == 0 {
		return ""
	}
	slugs := make([]string, len(repos))
	for i, r := range repos {
		slugs[i] = r.Slug
	}
	return strings.Join(slugs, "\n")
}

// Match resolves a list of AI-proposed repo slugs against the index,
// returning the matched Repo records (case-insensitive exact match on Slug,
// and a suffix match so "repo" matches "owner/repo" when unambiguous).
// Unmatched names are returned separately so the caller can log them.
func Match(repos []Repo, names []string) (matched []Repo, unmatched []string) {
	// Index by lowercased slug for exact lookup, and by lowercased final
	// path segment for suffix lookup.
	byExact := make(map[string]Repo, len(repos))
	bySuffix := make(map[string][]Repo, len(repos))
	for _, r := range repos {
		lower := strings.ToLower(r.Slug)
		byExact[lower] = r
		seg := lower
		if i := strings.LastIndex(lower, "/"); i >= 0 {
			seg = lower[i+1:]
		}
		bySuffix[seg] = append(bySuffix[seg], r)
	}

	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if r, ok := byExact[key]; ok {
			matched = append(matched, r)
			continue
		}
		// Suffix match on the "repo" segment, only when unambiguous.
		if cands, ok := bySuffix[key]; ok && len(cands) == 1 {
			matched = append(matched, cands[0])
			continue
		}
		unmatched = append(unmatched, name)
	}
	return matched, unmatched
}
