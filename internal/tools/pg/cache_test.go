package pg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPasswordCache_GetSet(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, ok := GetCachedPassword("/atlas/db/password"); ok {
		t.Fatalf("expected cache miss on empty cache")
	}
	if err := SetCachedPassword("/atlas/db/password", "s3cret"); err != nil {
		t.Fatalf("SetCachedPassword: %v", err)
	}
	pw, ok := GetCachedPassword("/atlas/db/password")
	if !ok {
		t.Fatalf("expected cache hit after set")
	}
	if pw != "s3cret" {
		t.Fatalf("got %q want s3cret", pw)
	}
}

func TestPasswordCache_PersistsAcrossReload(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if err := SetCachedPassword("/k1", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := SetCachedPassword("/k2", "v2"); err != nil {
		t.Fatal(err)
	}
	// Reset in-process state by reloading from disk via fresh LoadCache().
	c, err := LoadCache()
	if err != nil {
		t.Fatal(err)
	}
	if c.Passwords["/k1"] != "v1" || c.Passwords["/k2"] != "v2" {
		t.Fatalf("cache lost entries: %+v", c.Passwords)
	}
}

func TestPasswordCache_FileModeIs0600(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if err := SetCachedPassword("/test", "secret"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cache, "atelier", "pg", "configs.yaml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("expected cache mode 0600, got %o", mode)
	}
}

func TestPasswordCache_EmptyKeyIsNoOp(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := SetCachedPassword("", "x"); err != nil {
		t.Fatalf("empty key should not error: %v", err)
	}
	if _, ok := GetCachedPassword(""); ok {
		t.Fatalf("empty key should not return a hit")
	}
}

func TestPasswordCache_EmptyValueIsNoOp(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := SetCachedPassword("/x", ""); err != nil {
		t.Fatalf("empty value should not error: %v", err)
	}
	if _, ok := GetCachedPassword("/x"); ok {
		t.Fatalf("empty value should not be cached")
	}
}
