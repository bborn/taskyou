package claudeusage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// keychainPrefix is the macOS Keychain service name Claude Code stores its
// OAuth credentials under. For a non-default CLAUDE_CONFIG_DIR the service is
// suffixed with a short hash of the config dir, so every profile gets its own
// entry (see KeychainService).
const keychainPrefix = "Claude Code-credentials"

// credentialsFile is the on-disk credential store Claude Code uses where no OS
// keychain is available (Linux, containers). It lives inside the config dir.
const credentialsFile = ".credentials.json"

// Credentials is the subset of a Claude Code credential blob we need: the OAuth
// access token used for api.anthropic.com/api/oauth/* calls, plus enough
// metadata to tell a stale token from a missing one.
type Credentials struct {
	AccessToken  string
	ExpiresAt    time.Time // zero when the store didn't record one
	Subscription string
}

// Expired reports whether the access token's recorded expiry has passed. A
// zero ExpiresAt is treated as not expired — we'd rather attempt the request
// and let the API decide than refuse on missing metadata.
func (c Credentials) Expired() bool {
	return !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt)
}

// credentialsBlob mirrors the JSON shape of a Claude Code credential store. We
// only decode the claudeAiOauth object; the sibling mcpOAuth map holds
// per-MCP-server tokens that are none of our business.
type credentialsBlob struct {
	ClaudeAIOAuth struct {
		AccessToken      string `json:"accessToken"`
		ExpiresAt        int64  `json:"expiresAt"` // epoch milliseconds
		SubscriptionType string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

// KeychainService returns the macOS Keychain service name holding the
// credentials for a given CLAUDE_CONFIG_DIR.
//
// Claude Code namespaces each config dir's credentials by appending the first 8
// hex digits of the SHA-256 of the *absolute, cleaned* config-dir path to the
// base service name — that is what makes two logged-in profiles able to coexist
// on one machine. Reproducing the derivation here is what lets ty read a
// profile's usage without shelling out to `claude` (which has no usage command)
// or making the user paste a token anywhere.
func KeychainService(configDir string) string {
	sum := sha256.Sum256([]byte(normalizeDir(configDir)))
	return keychainPrefix + "-" + hex.EncodeToString(sum[:])[:8]
}

// normalizeDir expands a leading ~ and cleans the path, so the hash we compute
// matches the one Claude Code computed from its own resolved config dir.
func normalizeDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if strings.HasPrefix(dir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[1:])
		}
	}
	return filepath.Clean(dir)
}

// defaultConfigDir is ~/.claude — the config dir Claude Code uses when
// CLAUDE_CONFIG_DIR is unset. Credentials for it may still live under the
// unsuffixed keychain service from before per-profile namespacing existed.
func defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// LoadCredentials finds the OAuth credentials for one Claude profile.
//
// Lookup order, first hit wins:
//  1. the per-config-dir macOS Keychain entry (the normal case on a Mac);
//  2. <configDir>/.credentials.json (Linux, containers, and anywhere the
//     keychain isn't used);
//  3. for the default ~/.claude only, the legacy unsuffixed keychain entry.
//
// A store that exists but holds an empty access token counts as a miss, so a
// half-migrated profile falls through to the next candidate instead of failing
// with a confusing 401 later.
func LoadCredentials(configDir string) (Credentials, error) {
	dir := normalizeDir(configDir)

	if runtime.GOOS == "darwin" {
		if c, ok := readKeychain(KeychainService(dir)); ok {
			return c, nil
		}
	}

	if c, ok := readCredentialsFile(filepath.Join(dir, credentialsFile)); ok {
		return c, nil
	}

	if runtime.GOOS == "darwin" && dir == defaultConfigDir() {
		if c, ok := readKeychain(keychainPrefix); ok {
			return c, nil
		}
	}

	return Credentials{}, fmt.Errorf("no Claude credentials found for %s (log in once with CLAUDE_CONFIG_DIR=%s claude)", dir, dir)
}

// readKeychain pulls a credential blob out of the macOS Keychain. A missing
// entry, a locked keychain, or an entry without an access token all report
// "not found" rather than an error: every one of them means "try the next
// candidate", and none is worth failing the whole lookup over.
func readKeychain(service string) (Credentials, bool) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return Credentials{}, false
	}
	return parseCredentials(out)
}

func readCredentialsFile(path string) (Credentials, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from the caller's own config dir
	if err != nil {
		return Credentials{}, false
	}
	return parseCredentials(data)
}

func parseCredentials(data []byte) (Credentials, bool) {
	var blob credentialsBlob
	if err := json.Unmarshal(data, &blob); err != nil {
		return Credentials{}, false
	}
	token := strings.TrimSpace(blob.ClaudeAIOAuth.AccessToken)
	if token == "" {
		return Credentials{}, false
	}
	c := Credentials{AccessToken: token, Subscription: blob.ClaudeAIOAuth.SubscriptionType}
	if ms := blob.ClaudeAIOAuth.ExpiresAt; ms > 0 {
		c.ExpiresAt = time.UnixMilli(ms)
	}
	return c, true
}
