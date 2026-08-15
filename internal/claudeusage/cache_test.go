package claudeusage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countingServer serves the usage fixture and reports how many times it was hit.
func countingServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(usageBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetch_SecondCallIsServedFromCache(t *testing.T) {
	// This is the whole reason the cache exists: routing probes every profile on
	// every spawn, and the usage endpoint rate-limits. Without this, a busy
	// board 429s itself out of the numbers it routes on.
	var hits atomic.Int32
	srv := countingServer(t, &hits)
	client := testClient(t, srv)
	dir := writeCredsDir(t, "tok", time.Now().Add(time.Hour))

	for i := 0; i < 3; i++ {
		if _, err := client.Fetch(context.Background(), dir); err != nil {
			t.Fatalf("Fetch %d: %v", i, err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("hit the API %d times across 3 fetches, want 1", got)
	}
}

func TestFetch_NoCacheAlwaysReadsLive(t *testing.T) {
	var hits atomic.Int32
	srv := countingServer(t, &hits)
	client := testClient(t, srv)
	client.NoCache = true
	dir := writeCredsDir(t, "tok", time.Now().Add(time.Hour))

	for i := 0; i < 2; i++ {
		if _, err := client.Fetch(context.Background(), dir); err != nil {
			t.Fatalf("Fetch %d: %v", i, err)
		}
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("hit the API %d times with NoCache, want 2", got)
	}
}

func TestFetch_CacheIsPerProfile(t *testing.T) {
	// One profile's snapshot must never be served for another — that would route
	// tasks to an account based on a different account's headroom.
	var hits atomic.Int32
	srv := countingServer(t, &hits)
	client := testClient(t, srv)
	a := writeCredsDir(t, "tok-a", time.Now().Add(time.Hour))
	b := writeCredsDir(t, "tok-b", time.Now().Add(time.Hour))

	snapA, err := client.Fetch(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	snapB, err := client.Fetch(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Errorf("hit the API %d times for 2 profiles, want 2", hits.Load())
	}
	if snapA.ConfigDir == snapB.ConfigDir {
		t.Errorf("both snapshots claim config dir %q", snapA.ConfigDir)
	}
}

func TestFetch_StaleCacheRescuesAFailedRequest(t *testing.T) {
	// A 429 from the usage endpoint should not leave routing blind: a snapshot
	// from a few minutes ago is a far better basis for a decision than none.
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(usageBody))
	}))
	defer srv.Close()

	client := testClient(t, srv)
	dir := writeCredsDir(t, "tok", time.Now().Add(time.Hour))

	first, err := client.Fetch(context.Background(), dir)
	if err != nil {
		t.Fatalf("priming fetch: %v", err)
	}
	// Age the cached entry past CacheTTL but well inside StaleTTL.
	first.FetchedAt = time.Now().Add(-5 * time.Minute)
	client.writeCache(dir, first)

	fail.Store(true)
	snap, err := client.Fetch(context.Background(), dir)
	if err != nil {
		t.Fatalf("Fetch should have fallen back to cache: %v", err)
	}
	if !snap.Stale {
		t.Error("a cache-rescued snapshot must be marked stale")
	}
	if got := snap.UsedPercent(); got != 71 {
		t.Errorf("UsedPercent = %v, want the cached 71", got)
	}
}

func TestFetch_CacheTooOldToRescue(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(usageBody))
	}))
	defer srv.Close()

	client := testClient(t, srv)
	dir := writeCredsDir(t, "tok", time.Now().Add(time.Hour))

	first, err := client.Fetch(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first.FetchedAt = time.Now().Add(-2 * StaleTTL)
	client.writeCache(dir, first)

	fail.Store(true)
	_, err = client.Fetch(context.Background(), dir)
	if err == nil {
		t.Fatal("a snapshot older than StaleTTL should not rescue a failed fetch")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should surface the 429, got %v", err)
	}
}

func TestFetch_ExpiredCredentialsIgnoreTheCache(t *testing.T) {
	// A stale *credential* is a configuration problem, not a transient one.
	// Serving a cached number would keep sending tasks to a profile that can no
	// longer run them.
	var hits atomic.Int32
	srv := countingServer(t, &hits)
	client := testClient(t, srv)
	dir := writeCredsDir(t, "tok", time.Now().Add(time.Hour))

	if _, err := client.Fetch(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	// Same profile, same warm cache entry — but its token has since expired.
	writeCredsInto(t, dir, "tok", time.Now().Add(-time.Hour))
	if _, err := client.Fetch(context.Background(), dir); err == nil {
		t.Error("expired credentials should error even with a warm cache")
	}
}

func TestCorruptCacheEntryIsAMiss(t *testing.T) {
	var hits atomic.Int32
	srv := countingServer(t, &hits)
	client := testClient(t, srv)
	dir := writeCredsDir(t, "tok", time.Now().Add(time.Hour))

	if _, err := client.Fetch(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(client.cachePath(dir), "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background(), dir); err != nil {
		t.Fatalf("a corrupt cache entry must not fail the fetch: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("API hits = %d, want 2 (the corrupt entry should have been refetched)", hits.Load())
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
