package claudeusage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// usageBody is a trimmed but structurally faithful /api/oauth/usage response.
const usageBody = `{
  "five_hour": {"utilization": 3.0, "resets_at": "2026-08-15T13:59:59.981513+00:00"},
  "seven_day": {"utilization": 7.0, "resets_at": "2026-08-20T20:59:59.981535+00:00"},
  "limits": [
    {"kind": "session", "group": "session", "percent": 3, "severity": "normal",
     "resets_at": "2026-08-15T13:59:59.981513+00:00", "scope": null, "is_active": false},
    {"kind": "weekly_all", "group": "weekly", "percent": 71, "severity": "normal",
     "resets_at": "2026-08-20T20:59:59.981535+00:00", "scope": null, "is_active": true},
    {"kind": "weekly_scoped", "group": "weekly", "percent": 12, "severity": "normal",
     "resets_at": null, "scope": {"model": {"id": null, "display_name": "Opus"}}, "is_active": false}
  ]
}`

// writeCredsDir makes a config dir holding a .credentials.json, which is the
// non-macOS credential path and the one a test can exercise hermetically.
func writeCredsDir(t *testing.T, token string, expiresAt time.Time) string {
	t.Helper()
	dir := t.TempDir()
	writeCredsInto(t, dir, token, expiresAt)
	return dir
}

// writeCredsInto (re)writes a credential blob into an existing config dir, so a
// test can age a profile's token without changing its identity.
func writeCredsInto(t *testing.T, dir, token string, expiresAt time.Time) {
	t.Helper()
	blob := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      token,
			"subscriptionType": "max",
		},
	}
	if !expiresAt.IsZero() {
		blob["claudeAiOauth"].(map[string]any)["expiresAt"] = expiresAt.UnixMilli()
	}
	data, err := json.Marshal(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, credentialsFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// testClient points the client at a test server and an isolated cache dir, so
// no test can be helped (or hurt) by another test's cached snapshot, nor leave
// anything behind in the real user cache.
func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return &Client{BaseURL: srv.URL, HTTP: srv.Client(), CacheDir: t.TempDir()}
}

func TestFetch_ParsesLimitsAndAuthenticates(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(usageBody))
	}))
	defer srv.Close()

	dir := writeCredsDir(t, "tok-123", time.Now().Add(time.Hour))
	client := testClient(t, srv)

	snap, err := client.Fetch(context.Background(), dir)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotPath != "/api/oauth/usage" {
		t.Errorf("path = %q", gotPath)
	}
	if len(snap.Limits) != 3 {
		t.Fatalf("got %d limits, want 3", len(snap.Limits))
	}
	if snap.Limits[2].Scope != "Opus" {
		t.Errorf("scoped limit scope = %q, want Opus", snap.Limits[2].Scope)
	}
	if snap.Limits[0].ResetsAt == nil {
		t.Error("session limit lost its resets_at")
	}
}

func TestUsedPercentIsTheWorstWindow(t *testing.T) {
	// The point of routing is to avoid the window that stops work first. An
	// account 3% into its session but 71% into its week has 29% of headroom,
	// not 97% — averaging or taking the session window alone would send tasks
	// to an account about to run out.
	snap := Snapshot{Limits: []Limit{
		{Kind: "session", Percent: 3},
		{Kind: "weekly_all", Percent: 71},
		{Kind: "weekly_scoped", Percent: 12},
	}}
	if got := snap.UsedPercent(); got != 71 {
		t.Errorf("UsedPercent = %v, want 71", got)
	}
	if got := snap.Headroom(); got != 29 {
		t.Errorf("Headroom = %v, want 29", got)
	}
	binding, ok := snap.BindingLimit()
	if !ok || binding.Kind != "weekly_all" {
		t.Errorf("BindingLimit = %+v, ok=%v, want weekly_all", binding, ok)
	}
}

func TestUsedPercentOnEmptySnapshot(t *testing.T) {
	var snap Snapshot
	if got := snap.UsedPercent(); got != 0 {
		t.Errorf("UsedPercent = %v, want 0", got)
	}
	if _, ok := snap.BindingLimit(); ok {
		t.Error("BindingLimit should report not-found with no limits")
	}
}

func TestLimitsFallBackToLegacyWindows(t *testing.T) {
	// If the API ever drops back to the older shape (no `limits` array), routing
	// must keep working off five_hour/seven_day rather than seeing 0% used and
	// happily piling work onto an exhausted account.
	body := `{"five_hour": {"utilization": 88.0, "resets_at": null},
	          "seven_day": {"utilization": 40.0, "resets_at": null}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dir := writeCredsDir(t, "tok", time.Time{})
	client := testClient(t, srv)
	snap, err := client.Fetch(context.Background(), dir)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Limits) != 2 {
		t.Fatalf("got %d limits, want 2 synthesized", len(snap.Limits))
	}
	if got := snap.UsedPercent(); got != 88 {
		t.Errorf("UsedPercent = %v, want 88", got)
	}
}

func TestFetch_ExpiredTokenIsNotSentToTheAPI(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	dir := writeCredsDir(t, "stale", time.Now().Add(-time.Hour))
	client := testClient(t, srv)
	_, err := client.Fetch(context.Background(), dir)
	if err == nil {
		t.Fatal("expected an error for an expired token")
	}
	if called {
		t.Error("expired token should not reach the API")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should say the credentials expired: %v", err)
	}
}

func TestFetch_UnauthorizedIsExplainedNotDumped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()

	dir := writeCredsDir(t, "tok", time.Time{})
	client := testClient(t, srv)
	_, err := client.Fetch(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "fresh login") {
		t.Errorf("401 should point at re-login, got %v", err)
	}
}

func TestFetch_MissingCredentialsErrors(t *testing.T) {
	client := &Client{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: time.Second}, CacheDir: t.TempDir()}
	_, err := client.Fetch(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no Claude credentials") {
		t.Errorf("want a missing-credentials error, got %v", err)
	}
}

func TestLoadCredentials_EmptyTokenCountsAsMissing(t *testing.T) {
	// A half-migrated profile leaves a credential blob with an empty token.
	// Treating it as found would surface as a baffling 401 later.
	dir := writeCredsDir(t, "", time.Time{})
	if _, err := LoadCredentials(dir); err == nil {
		t.Error("an empty access token should not count as credentials")
	}
}

func TestKeychainServiceDerivation(t *testing.T) {
	// Claude Code namespaces each config dir's keychain entry by the first 8 hex
	// digits of the SHA-256 of the absolute path. Pinning a known value here is
	// what catches the derivation drifting: get it wrong and every profile
	// silently reports "no credentials" on a Mac.
	if got, want := KeychainService("/Users/bruno/.claude-ik"), "Claude Code-credentials-eaf7266a"; got != want {
		t.Errorf("KeychainService = %q, want %q", got, want)
	}
	if got, want := KeychainService("/Users/bruno/.claude"), "Claude Code-credentials-5561fe67"; got != want {
		t.Errorf("KeychainService = %q, want %q", got, want)
	}
	// A trailing slash or a redundant segment is the same dir, so it must hash
	// the same — Claude Code hashed its own cleaned path.
	if KeychainService("/Users/bruno/.claude-ik/") != KeychainService("/Users/bruno/.claude-ik") {
		t.Error("trailing slash changed the keychain service name")
	}
	if KeychainService("/Users/bruno/foo/../.claude-ik") != KeychainService("/Users/bruno/.claude-ik") {
		t.Error("uncleaned path changed the keychain service name")
	}
}

func TestKeychainServiceExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if KeychainService("~/.claude-x") != KeychainService(filepath.Join(home, ".claude-x")) {
		t.Error("~ was not expanded before hashing")
	}
}

func TestDescribe(t *testing.T) {
	reset := time.Date(2026, 8, 20, 20, 0, 0, 0, time.UTC)
	snap := Snapshot{Limits: []Limit{
		{Kind: "session", Percent: 3},
		{Kind: "weekly_all", Percent: 71, ResetsAt: &reset},
	}}
	got := snap.Describe()
	for _, want := range []string{"71% used", "weekly", "29% headroom", "resets"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe() = %q, missing %q", got, want)
		}
	}
}

func TestDescribeNamesTheScopedModel(t *testing.T) {
	snap := Snapshot{Limits: []Limit{{Kind: "weekly_scoped", Percent: 95, Scope: "Opus"}}}
	if got := snap.Describe(); !strings.Contains(got, "weekly Opus") {
		t.Errorf("Describe() = %q, want the model named", got)
	}
}
