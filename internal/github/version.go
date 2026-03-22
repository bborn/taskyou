// Package github provides GitHub integration.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ghReleaseResponse is the JSON response from GitHub releases API.
type ghReleaseResponse struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// LatestRelease holds information about the latest GitHub release.
type LatestRelease struct {
	Version string // e.g. "v0.2.0"
	URL     string // release page URL
}

const (
	releaseRepo     = "bborn/taskyou"
	releaseTimeout  = 5 * time.Second
	versionCacheTTL = 24 * time.Hour
	cacheFileName   = "version-check.json"
)

// versionCache is the on-disk cache for the latest release check.
type versionCache struct {
	Version   string    `json:"version"`
	URL       string    `json:"url"`
	CheckedAt time.Time `json:"checked_at"`
}

// FetchLatestRelease queries the GitHub API for the latest release.
// Returns nil if the request fails or no release exists.
func FetchLatestRelease() *LatestRelease {
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", releaseRepo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var release ghReleaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil
	}

	if release.TagName == "" {
		return nil
	}

	return &LatestRelease{
		Version: release.TagName,
		URL:     release.HTMLURL,
	}
}

// IsNewerVersion returns true if latest is a newer version than current.
// Both should be semver strings, optionally prefixed with "v".
func IsNewerVersion(current, latest string) bool {
	if current == "" || latest == "" || current == "dev" {
		return false
	}

	cur := parseVersion(current)
	lat := parseVersion(latest)
	if cur == nil || lat == nil {
		return false
	}

	if lat[0] != cur[0] {
		return lat[0] > cur[0]
	}
	if lat[1] != cur[1] {
		return lat[1] > cur[1]
	}
	return lat[2] > cur[2]
}

// parseVersion parses a "v1.2.3" or "1.2.3" string into [major, minor, patch].
// Returns nil on failure.
func parseVersion(v string) []int {
	v = strings.TrimPrefix(v, "v")
	// Strip pre-release suffixes (e.g. "-rc1", "-beta")
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}

	result := make([]int, 3)
	for i, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil
			}
			n = n*10 + int(c-'0')
		}
		result[i] = n
	}
	return result
}

// cacheDir returns the directory used for caching version check results.
// Defaults to ~/.local/share/task/ but respects WORKTREE_DB_PATH if set.
func cacheDir() string {
	if p := os.Getenv("WORKTREE_DB_PATH"); p != "" {
		return filepath.Dir(p)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "task")
}

// readCache loads the version check cache from disk. Returns nil if missing or unreadable.
func readCache() *versionCache {
	data, err := os.ReadFile(filepath.Join(cacheDir(), cacheFileName))
	if err != nil {
		return nil
	}
	var c versionCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}

// writeCache persists the version check result to disk.
func writeCache(release *LatestRelease) {
	c := versionCache{
		Version:   release.Version,
		URL:       release.URL,
		CheckedAt: time.Now(),
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	dir := cacheDir()
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, cacheFileName), data, 0o644)
}

// CLIVersionCheck checks if a newer version is available, using a 24h on-disk cache.
// Returns the latest release if an upgrade is available, nil otherwise.
// Skips the check entirely for "dev" builds.
func CLIVersionCheck(currentVersion string) *LatestRelease {
	if currentVersion == "" || currentVersion == "dev" {
		return nil
	}

	// Try the cache first
	if c := readCache(); c != nil && time.Since(c.CheckedAt) < versionCacheTTL {
		release := &LatestRelease{Version: c.Version, URL: c.URL}
		if IsNewerVersion(currentVersion, release.Version) {
			return release
		}
		return nil
	}

	// Cache miss or stale — fetch from GitHub
	release := FetchLatestRelease()
	if release != nil {
		writeCache(release)
		if IsNewerVersion(currentVersion, release.Version) {
			return release
		}
	}
	return nil
}
