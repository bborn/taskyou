package github

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"newer patch", "v0.1.0", "v0.1.1", true},
		{"newer minor", "v0.1.0", "v0.2.0", true},
		{"newer major", "v0.1.0", "v1.0.0", true},
		{"same version", "v0.1.0", "v0.1.0", false},
		{"older version", "v0.2.0", "v0.1.0", false},
		{"no v prefix", "0.1.0", "0.2.0", true},
		{"mixed prefix", "v0.1.0", "0.2.0", true},
		{"dev version", "dev", "v0.1.0", false},
		{"empty current", "", "v0.1.0", false},
		{"empty latest", "v0.1.0", "", false},
		{"pre-release latest", "v0.1.0", "v0.2.0-rc1", true},
		{"pre-release current", "v0.1.0-beta", "v0.1.1", true},
		{"multi-digit", "v1.9.0", "v1.10.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNewerVersion(tt.current, tt.latest)
			if got != tt.want {
				t.Errorf("IsNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"v1.2.3", true},
		{"1.2.3", true},
		{"v0.0.0", true},
		{"v1.2.3-rc1", true},
		{"invalid", false},
		{"v1.2", false},
		{"v1.2.3.4", false},
		{"v1.a.3", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseVersion(tt.input)
			if tt.valid && result == nil {
				t.Errorf("parseVersion(%q) returned nil, expected valid", tt.input)
			}
			if !tt.valid && result != nil {
				t.Errorf("parseVersion(%q) returned %v, expected nil", tt.input, result)
			}
		})
	}
}

func TestCLIVersionCheck_DevSkipped(t *testing.T) {
	if release := CLIVersionCheck("dev"); release != nil {
		t.Error("expected nil for dev version")
	}
	if release := CLIVersionCheck(""); release != nil {
		t.Error("expected nil for empty version")
	}
}

func TestCLIVersionCheck_CachedNewerVersion(t *testing.T) {
	// Set up a temp directory for the cache
	tmp := t.TempDir()
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(tmp, "tasks.db"))

	// Write a fresh cache entry with a "newer" version
	c := versionCache{
		Version:   "v99.0.0",
		URL:       "https://github.com/bborn/taskyou/releases/tag/v99.0.0",
		CheckedAt: time.Now(),
	}
	data, _ := json.Marshal(c)
	_ = os.WriteFile(filepath.Join(tmp, cacheFileName), data, 0o644)

	release := CLIVersionCheck("v1.0.0")
	if release == nil {
		t.Fatal("expected non-nil release for cached newer version")
	}
	if release.Version != "v99.0.0" {
		t.Errorf("got version %q, want v99.0.0", release.Version)
	}
}

func TestCLIVersionCheck_CachedSameVersion(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(tmp, "tasks.db"))

	c := versionCache{
		Version:   "v1.0.0",
		URL:       "https://github.com/bborn/taskyou/releases/tag/v1.0.0",
		CheckedAt: time.Now(),
	}
	data, _ := json.Marshal(c)
	_ = os.WriteFile(filepath.Join(tmp, cacheFileName), data, 0o644)

	release := CLIVersionCheck("v1.0.0")
	if release != nil {
		t.Error("expected nil when cached version equals current")
	}
}

func TestReadWriteCache(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(tmp, "tasks.db"))

	// No cache yet
	if c := readCache(); c != nil {
		t.Error("expected nil for missing cache")
	}

	// Write cache
	writeCache(&LatestRelease{Version: "v2.0.0", URL: "https://example.com"})

	c := readCache()
	if c == nil {
		t.Fatal("expected non-nil cache after write")
	}
	if c.Version != "v2.0.0" {
		t.Errorf("got version %q, want v2.0.0", c.Version)
	}
	if time.Since(c.CheckedAt) > time.Minute {
		t.Error("cache CheckedAt should be recent")
	}
}
