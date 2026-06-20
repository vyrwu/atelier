package pg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/vyrwu/atelier/internal/config"
)

// PasswordCache stores SSM-fetched Postgres passwords keyed by their SSM
// parameter path. File: $XDG_CACHE_HOME/atelier/pg/configs.yaml (mode 0600,
// directory mode 0700). Matches the bash setup's gitignored configs.yaml.
//
// Cached indefinitely until manually wiped — matches bash behavior. If a
// password rotates in AWS, the user deletes the cache file or the specific
// entry and atelier refetches.
type PasswordCache struct {
	Passwords map[string]string `yaml:"passwords"`
}

var cacheMu sync.Mutex

func cacheFile() string {
	return filepath.Join(config.XDGCacheHome(), "atelier", "pg", "configs.yaml")
}

// LoadCache returns the on-disk cache, or an empty cache if the file doesn't exist.
func LoadCache() (*PasswordCache, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return loadCacheLocked()
}

func loadCacheLocked() (*PasswordCache, error) {
	path := cacheFile()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &PasswordCache{Passwords: map[string]string{}}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c PasswordCache
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Passwords == nil {
		c.Passwords = map[string]string{}
	}
	return &c, nil
}

// SaveCache atomically writes the cache to disk with 0600 permissions.
func SaveCache(c *PasswordCache) error {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return saveCacheLocked(c)
}

func saveCacheLocked(c *PasswordCache) error {
	path := cacheFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// GetCachedPassword returns the cached password for ssmPath, or ("", false).
func GetCachedPassword(ssmPath string) (string, bool) {
	if ssmPath == "" {
		return "", false
	}
	c, err := LoadCache()
	if err != nil {
		return "", false
	}
	pw, ok := c.Passwords[ssmPath]
	return pw, ok && pw != ""
}

// SetCachedPassword writes one password into the cache and saves it.
// Acquires the lock once for the load+update+save sequence.
func SetCachedPassword(ssmPath, password string) error {
	if ssmPath == "" || password == "" {
		return nil
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	c, err := loadCacheLocked()
	if err != nil {
		return err
	}
	c.Passwords[ssmPath] = password
	return saveCacheLocked(c)
}

// GetCachedByKey looks up a password by the bash-style "<context>:<endpoint>"
// key. Returns ("", false) on miss. Stored under PasswordCache.Passwords
// (same file) — bash keyed by this composite, atelier by SSM path; both
// share the cache because keys never collide.
func GetCachedByKey(key string) (string, bool) {
	return GetCachedPassword(key)
}

// SetCachedByKey stores a password under the bash-style key.
func SetCachedByKey(key, password string) error {
	return SetCachedPassword(key, password)
}
