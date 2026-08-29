package repoindex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mkRepo creates root/slug/.git so the dir looks like a git repo.
func mkRepo(t *testing.T, root, slug string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(slug))
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkRepo %s: %v", slug, err)
	}
	return dir
}

// mkPlainDir creates root/rel with no .git (a non-repo dir).
func mkPlainDir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
		t.Fatalf("mkPlainDir %s: %v", rel, err)
	}
}

func TestScan(t *testing.T) {
	t.Run("discovers owner/repo git dirs sorted by slug", func(t *testing.T) {
		root := t.TempDir()
		wantVyrwu := mkRepo(t, root, "vyrwu/atelier")
		wantWawa := mkRepo(t, root, "wawafertility/wawa-clinic")
		// Non-repo owner/repo dir: skipped.
		mkPlainDir(t, root, "vyrwu/not-a-repo")
		// Loose owner-level dir (depth 1) with no repo under it: yields nothing.
		mkPlainDir(t, root, "empty-owner")

		got, err := Scan(root)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		want := []Repo{
			{Slug: "vyrwu/atelier", Path: wantVyrwu},
			{Slug: "wawafertility/wawa-clinic", Path: wantWawa},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Scan =\n %+v\nwant\n %+v", got, want)
		}
	})

	t.Run("missing codeRoot returns nil,nil", func(t *testing.T) {
		got, err := Scan(filepath.Join(t.TempDir(), "does-not-exist"))
		if err != nil {
			t.Fatalf("Scan missing: err = %v, want nil", err)
		}
		if got != nil {
			t.Errorf("Scan missing: got %+v, want nil", got)
		}
	})

	t.Run("does not descend below depth 2", func(t *testing.T) {
		root := t.TempDir()
		mkRepo(t, root, "owner/repo")
		// A nested git dir three levels deep must not appear.
		mkRepo(t, root, "owner/repo/nested")

		got, err := Scan(root)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(got) != 1 || got[0].Slug != "owner/repo" {
			t.Errorf("Scan depth: got %+v, want single owner/repo", got)
		}
	})
}

func TestFormat(t *testing.T) {
	tests := []struct {
		name  string
		repos []Repo
		want  string
	}{
		{"empty", nil, ""},
		{"single", []Repo{{Slug: "vyrwu/atelier"}}, "vyrwu/atelier"},
		{
			"multiple newline-delimited",
			[]Repo{{Slug: "vyrwu/atelier"}, {Slug: "wawafertility/wawa-clinic"}},
			"vyrwu/atelier\nwawafertility/wawa-clinic",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(tc.repos); got != tc.want {
				t.Errorf("Format = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	index := []Repo{
		{Slug: "vyrwu/atelier", Path: "/code/vyrwu/atelier"},
		{Slug: "wawafertility/wawa-clinic", Path: "/code/wawafertility/wawa-clinic"},
		{Slug: "vyrwu/cnd2", Path: "/code/vyrwu/cnd2"},
		// Two repos share the "shared" suffix -> ambiguous.
		{Slug: "vyrwu/shared", Path: "/code/vyrwu/shared"},
		{Slug: "wawafertility/shared", Path: "/code/wawafertility/shared"},
	}

	tests := []struct {
		name          string
		names         []string
		wantMatched   []Repo
		wantUnmatched []string
	}{
		{
			name:        "exact slug match",
			names:       []string{"vyrwu/atelier"},
			wantMatched: []Repo{{Slug: "vyrwu/atelier", Path: "/code/vyrwu/atelier"}},
		},
		{
			name:        "case-insensitive exact match",
			names:       []string{"VYRWU/Atelier"},
			wantMatched: []Repo{{Slug: "vyrwu/atelier", Path: "/code/vyrwu/atelier"}},
		},
		{
			name:        "unambiguous suffix match",
			names:       []string{"cnd2"},
			wantMatched: []Repo{{Slug: "vyrwu/cnd2", Path: "/code/vyrwu/cnd2"}},
		},
		{
			name:        "case-insensitive suffix match",
			names:       []string{"WAWA-CLINIC"},
			wantMatched: []Repo{{Slug: "wawafertility/wawa-clinic", Path: "/code/wawafertility/wawa-clinic"}},
		},
		{
			name:          "ambiguous suffix -> unmatched",
			names:         []string{"shared"},
			wantUnmatched: []string{"shared"},
		},
		{
			name:          "unknown name -> unmatched",
			names:         []string{"nope/nothere"},
			wantUnmatched: []string{"nope/nothere"},
		},
		{
			name:  "mixed: exact + suffix + unmatched, order preserved",
			names: []string{"vyrwu/atelier", "cnd2", "ghost"},
			wantMatched: []Repo{
				{Slug: "vyrwu/atelier", Path: "/code/vyrwu/atelier"},
				{Slug: "vyrwu/cnd2", Path: "/code/vyrwu/cnd2"},
			},
			wantUnmatched: []string{"ghost"},
		},
		{
			name:        "whitespace trimmed before matching",
			names:       []string{"  vyrwu/atelier  "},
			wantMatched: []Repo{{Slug: "vyrwu/atelier", Path: "/code/vyrwu/atelier"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matched, unmatched := Match(index, tc.names)
			if !reflect.DeepEqual(matched, tc.wantMatched) {
				t.Errorf("matched =\n %+v\nwant\n %+v", matched, tc.wantMatched)
			}
			if !reflect.DeepEqual(unmatched, tc.wantUnmatched) {
				t.Errorf("unmatched = %+v, want %+v", unmatched, tc.wantUnmatched)
			}
		})
	}
}
