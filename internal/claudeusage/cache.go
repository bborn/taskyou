package claudeusage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// The usage endpoint rate-limits, and routing calls it on every spawn — a busy
// board would otherwise walk straight into a 429 and lose the very numbers it
// spawns on. So snapshots are cached on disk, briefly.
//
// On disk rather than in memory because the caller that matters is a shell
// script: a routing plugin shells out to `ty usage`, so every probe is a fresh
// process and an in-process cache would never be read.
const (
	// CacheTTL is how long a snapshot is served without asking the API again.
	// Rate-limit utilization moves in percentage points over minutes; a minute
	// of staleness cannot flip a routing decision that wasn't already marginal.
	CacheTTL = 60 * time.Second

	// StaleTTL is how old a cached snapshot may be and still be used when a
	// live fetch fails. Routing on a 10-minute-old number beats routing on
	// nothing, which is what a transient 429 would otherwise leave us with.
	StaleTTL = 30 * time.Minute
)

// DefaultCacheDir is where snapshots are stored.
func DefaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "ty", "claude-usage")
}

func (c *Client) cacheDir() string {
	if c.CacheDir != "" {
		return c.CacheDir
	}
	return DefaultCacheDir()
}

// cachePath names a profile's cache file by a hash of its config dir, so the
// path is flat and safe regardless of what the dir itself is called.
func (c *Client) cachePath(configDir string) string {
	dir := c.cacheDir()
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalizeDir(configDir)))
	return filepath.Join(dir, hex.EncodeToString(sum[:])[:16]+".json")
}

// readCache returns a cached snapshot if one exists and is younger than maxAge.
// Any problem — no file, unreadable, corrupt — reads as a miss; a cache is never
// a reason to fail.
func (c *Client) readCache(configDir string, maxAge time.Duration) (*Snapshot, bool) {
	if c.NoCache {
		return nil, false
	}
	path := c.cachePath(configDir)
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is a hash under our own cache dir
	if err != nil {
		return nil, false
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, false
	}
	if snap.FetchedAt.IsZero() || time.Since(snap.FetchedAt) > maxAge {
		return nil, false
	}
	return &snap, true
}

// writeCache stores a snapshot. Failures are ignored: a cache that can't be
// written costs an extra API call, nothing more.
func (c *Client) writeCache(configDir string, snap *Snapshot) {
	if c.NoCache {
		return
	}
	path := c.cachePath(configDir)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return
	}
	// Write-then-rename so a concurrent reader never sees a half-written file:
	// several spawns can probe the same profile at once.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}
