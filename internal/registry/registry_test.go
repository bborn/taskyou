package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBundled_IsUsableCatalog(t *testing.T) {
	c, err := Bundled()
	if err != nil {
		t.Fatalf("Bundled: %v", err)
	}
	if c.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", c.SchemaVersion, SchemaVersion)
	}
	if len(c.Plugins) == 0 {
		t.Fatal("bundled catalog is empty; discovery would show nothing on a fresh install")
	}
	seen := map[string]bool{}
	for _, e := range c.Plugins {
		if !e.Valid() {
			t.Errorf("entry %q is missing a required field: %+v", e.ID, e)
		}
		if seen[e.ID] {
			t.Errorf("duplicate entry id %q", e.ID)
		}
		seen[e.ID] = true
	}
}

// The catalog published on the docs site must be byte-identical to the snapshot
// compiled into the binary — two copies of one document is only safe if drift
// fails the build.
func TestBundledCatalogMatchesPublishedCopy(t *testing.T) {
	embedded, err := bundled.ReadFile("catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	published, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry.json"))
	if err != nil {
		t.Fatalf("read docs/registry.json: %v", err)
	}
	if string(embedded) != string(published) {
		t.Error("internal/registry/catalog.json and docs/registry.json differ; " +
			"run `make sync-registry` (or copy the former over the latter)")
	}
}

func testLoader(t *testing.T, urls ...string) *Loader {
	t.Helper()
	return &Loader{
		URLs:     urls,
		CacheDir: t.TempDir(),
		TTL:      time.Hour,
		HTTP:     &http.Client{Timeout: 2 * time.Second},
		Now:      time.Now,
	}
}

const remoteCatalog = `{
  "schema_version": 1,
  "plugins": [
    {"id": "brand-new", "name": "Brand New", "description": "Only in the remote catalog.", "source": "https://example.test/x"},
    {"id": "slack", "name": "Slack (updated)", "description": "A newer description.", "source": "https://example.test/y"}
  ]
}`

// A remote catalog overlays the bundled snapshot by ID: new entries appear, known
// ones are corrected, and everything else the binary shipped with survives.
func TestLoad_RemoteOverlaysBundled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(remoteCatalog))
	}))
	defer srv.Close()

	res := testLoader(t, srv.URL).Load(context.Background())
	if _, ok := Find(res.Entries, "brand-new"); !ok {
		t.Error("remote-only entry missing from the merged catalog")
	}
	slack, ok := Find(res.Entries, "slack")
	if !ok {
		t.Fatal("bundled entry slack missing after overlay")
	}
	if slack.Name != "Slack (updated)" {
		t.Errorf("slack name = %q, want the remote copy to win", slack.Name)
	}
	if _, ok := Find(res.Entries, "rpi"); !ok {
		t.Error("bundled entry rpi disappeared; the overlay must not replace the whole catalog")
	}
	if res.Stale() {
		t.Error("Stale() = true after a successful fetch")
	}
}

// A second Load inside the TTL is served from the cache without another request.
func TestLoad_ServesCacheWithinTTL(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(remoteCatalog))
	}))
	defer srv.Close()

	l := testLoader(t, srv.URL)
	l.Load(context.Background())
	l.Load(context.Background())
	if hits != 1 {
		t.Errorf("server hit %d times, want 1 (second load should come from cache)", hits)
	}

	// Refresh bypasses the cache on purpose.
	l.Refresh(context.Background())
	if hits != 2 {
		t.Errorf("server hit %d times after Refresh, want 2", hits)
	}
}

// A dead network degrades to the cache, and the result says so.
func TestLoad_FallsBackToCacheThenBundle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(remoteCatalog))
	}))
	l := testLoader(t, srv.URL)
	l.Load(context.Background()) // warms the cache
	srv.Close()

	l.TTL = 0 // force a re-fetch, which will now fail
	res := l.Load(context.Background())
	if _, ok := Find(res.Entries, "brand-new"); !ok {
		t.Error("cached remote entry lost when the fetch failed")
	}
	if !res.Stale() {
		t.Error("Stale() = false when every remote layer fell back to cache")
	}

	// With no cache at all, the bundled snapshot still carries discovery.
	l2 := testLoader(t, "http://127.0.0.1:1/never")
	res2 := l2.Load(context.Background())
	if len(res2.Entries) == 0 {
		t.Error("an unreachable catalog and an empty cache must still yield the bundled entries")
	}
}

// A remote catalog that is not valid JSON must not wipe out discovery.
func TestLoad_IgnoresMalformedRemote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	defer srv.Close()

	res := testLoader(t, srv.URL).Load(context.Background())
	if len(res.Entries) == 0 {
		t.Fatal("a malformed remote catalog emptied the merged catalog")
	}
	var sawErr bool
	for _, o := range res.Origins {
		if o.URL == srv.URL && o.Err != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("the malformed fetch should be reported in Origins")
	}
}

// Entries that could never be installed are dropped at parse time rather than
// shown as broken rows.
func TestParse_DropsInvalidEntries(t *testing.T) {
	c, err := Parse([]byte(`{"schema_version":1,"plugins":[
		{"id":"ok","description":"d","source":"s"},
		{"id":"no-source","description":"d"},
		{"description":"no id","source":"s"}
	]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(c.Plugins) != 1 || c.Plugins[0].ID != "ok" {
		t.Errorf("plugins = %+v, want only the complete entry", c.Plugins)
	}
}

func TestConfiguredURLs(t *testing.T) {
	t.Setenv(CatalogEnv, "")
	if got := ConfiguredURLs(); len(got) != 0 {
		t.Errorf("an explicitly empty %s should disable remote catalogs, got %v", CatalogEnv, got)
	}
	t.Setenv(CatalogEnv, "https://a.test/c.json, https://b.test/c.json")
	if got := ConfiguredURLs(); len(got) != 2 || got[1] != "https://b.test/c.json" {
		t.Errorf("ConfiguredURLs = %v, want both URLs trimmed", got)
	}
	os.Unsetenv(CatalogEnv)
	if got := ConfiguredURLs(); len(got) != 1 || got[0] != DefaultCatalogURL {
		t.Errorf("ConfiguredURLs = %v, want the default catalog", got)
	}
}
