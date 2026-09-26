package git

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vyrwu/atelier/internal/core"
)

// AheadBehindCached returns a worktree's ahead/behind against its default branch,
// reusing a per-path on-disk cache within ttl so consecutive reads (e.g. reopening
// the worktree view) don't re-spawn git. A commit or fetch does move ahead/behind,
// so the cached glyph can lag by up to ttl after one — an acceptable trade for an
// at-a-glance indicator, and it self-heals. Mirrors the PR-status cache. On a
// miss it computes AheadBehind and records the result.
func AheadBehindCached(path string, ttl time.Duration) (ahead, behind int, ok bool) {
	if a, b, o, ts, found := readFreshness(path); found && time.Since(ts) < ttl {
		return a, b, o
	}
	a, b, o := AheadBehind(path)
	writeFreshness(path, a, b, o)
	return a, b, o
}

// freshnessFile is the cache path for a worktree, keyed by a hash of its path.
func freshnessFile(path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(core.CacheDir(), "worktrees", hex.EncodeToString(sum[:8]))
}

// writeFreshness records "ahead behind ok unix-ts" for path (best-effort).
func writeFreshness(path string, ahead, behind int, ok bool) {
	if err := os.MkdirAll(filepath.Dir(freshnessFile(path)), 0o755); err != nil {
		return
	}
	oki := 0
	if ok {
		oki = 1
	}
	body := fmt.Sprintf("%d %d %d %d", ahead, behind, oki, time.Now().Unix())
	_ = os.WriteFile(freshnessFile(path), []byte(body), 0o644)
}

// readFreshness parses a cached "ahead behind ok unix-ts"; found is false when
// the cache is missing or unparseable.
func readFreshness(path string) (ahead, behind int, ok bool, ts time.Time, found bool) {
	data, err := os.ReadFile(freshnessFile(path))
	if err != nil {
		return 0, 0, false, time.Time{}, false
	}
	f := strings.Fields(string(data))
	if len(f) != 4 {
		return 0, 0, false, time.Time{}, false
	}
	ahead, _ = strconv.Atoi(f[0])
	behind, _ = strconv.Atoi(f[1])
	oki, _ := strconv.Atoi(f[2])
	sec, _ := strconv.ParseInt(f[3], 10, 64)
	return ahead, behind, oki == 1, time.Unix(sec, 0), true
}
