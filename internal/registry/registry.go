// Package registry is the discovery half of TaskYou's plugin story.
//
// Installing a plugin used to require knowing that github.com/taskyou/plugins
// existed and typing its URL. A registry fixes that: it is a small JSON catalog
// of installable plugins, so every surface can offer *search* ("what can I
// install that posts to Slack?") before it offers *install*.
//
// Three layers, most-trusted-last:
//
//  1. A snapshot bundled into the binary (catalog.json, embedded). Discovery
//     therefore works on a fresh install, offline, with zero configuration.
//  2. The on-disk cache of each remote catalog, refreshed in the background.
//  3. A live fetch of each remote catalog when the cache is older than the TTL.
//
// Later layers overlay earlier ones by entry ID, so a published catalog can
// correct or extend what a shipped binary knows without a release. A failed
// fetch is never fatal — it degrades to the cache, then to the bundle.
package registry

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the catalog format this build reads and writes. Readers
// accept anything at or below it; a newer catalog is still parsed (unknown
// fields are ignored) so an old binary keeps working against a new catalog.
const SchemaVersion = 1

// DefaultCatalogURL is the community catalog published from the TaskYou docs
// site (docs/registry.json in the main repo). It is the same document embedded
// in the binary, so the fetch only ever brings news.
const DefaultCatalogURL = "https://taskyou.dev/registry.json"

// CatalogEnv overrides the remote catalog list with a comma-separated set of
// URLs. Set it to "" (explicitly empty) to use only the bundled snapshot —
// which is what the tests and air-gapped installs want.
const CatalogEnv = "TY_PLUGIN_REGISTRY"

// DefaultTTL is how long a cached catalog is served before it is re-fetched.
const DefaultTTL = 6 * time.Hour

//go:embed catalog.json
var bundled embed.FS

// Entry is one installable plugin in a catalog.
//
// It is deliberately *not* a plugin manifest: a manifest describes a plugin that
// is already on disk, an entry describes how to get one and why you would want
// it. Only ID, Name, Description and Source are required.
type Entry struct {
	// ID is the stable, unique handle users type: `ty plugins add slack`.
	ID string `json:"id"`
	// Name is the human-facing title.
	Name string `json:"name"`
	// Description is one or two sentences on what the plugin does.
	Description string `json:"description"`
	// Author is a display name or GitHub handle.
	Author string `json:"author,omitempty"`
	// Source is the git URL to clone.
	Source string `json:"source"`
	// Subdir is the plugin's path inside Source, for collection repos that hold
	// many plugins. Empty means the repo root is the plugin.
	Subdir string `json:"subdir,omitempty"`
	// Homepage is a URL to read more (README, docs page).
	Homepage string `json:"homepage,omitempty"`
	// Category groups entries in the browser ("workflows", "notifications", …).
	Category string `json:"category,omitempty"`
	// Tags are free-form search keywords.
	Tags []string `json:"tags,omitempty"`
	// Provides lists the plugin's capability kinds ("workflow", "hook",
	// "action", "service", "routine") so a browser can say what you get.
	Provides []string `json:"provides,omitempty"`
	// Requires lists external prerequisites in plain words ("jq", "an ARC API
	// key") — the things that make an install fail confusingly if missing.
	Requires []string `json:"requires,omitempty"`
}

// DisplayName returns Name, falling back to the ID.
func (e Entry) DisplayName() string {
	if strings.TrimSpace(e.Name) != "" {
		return e.Name
	}
	return e.ID
}

// CloneURL is the git URL to clone for this entry.
func (e Entry) CloneURL() string { return e.Source }

// Valid reports whether the entry has everything an install needs.
func (e Entry) Valid() bool {
	return e.ID != "" && e.Source != "" && e.Description != ""
}

// Catalog is a published set of installable plugins.
type Catalog struct {
	SchemaVersion int     `json:"schema_version"`
	Name          string  `json:"name,omitempty"`
	UpdatedAt     string  `json:"updated_at,omitempty"`
	Plugins       []Entry `json:"plugins"`
}

// Origin records where one layer of the loaded catalog came from, so a UI can
// say "3 sources, one stale" instead of silently serving month-old data.
type Origin struct {
	// URL is the remote catalog, or "bundled" for the embedded snapshot.
	URL string
	// From is "bundled", "cache", or "network".
	From string
	// Entries is how many entries this layer contributed (before overlay).
	Entries int
	// Age is how old the served copy is; zero for a fresh fetch.
	Age time.Duration
	// Err is why a remote fetch was not used, if it failed.
	Err error
}

// Result is a loaded catalog plus the provenance of each layer.
type Result struct {
	Entries []Entry
	Origins []Origin
}

// Stale reports whether every remote layer fell back to cache or bundle — the
// state worth telling the user about ("catalog may be out of date").
func (r Result) Stale() bool {
	sawRemote := false
	for _, o := range r.Origins {
		if o.URL == BundledOrigin {
			continue
		}
		sawRemote = true
		if o.From == "network" {
			return false
		}
	}
	return sawRemote
}

// BundledOrigin is the Origin.URL value for the embedded snapshot.
const BundledOrigin = "bundled"

// Loader loads and caches catalogs. The zero value is not usable; call
// NewLoader (or Default) instead.
type Loader struct {
	// URLs are the remote catalogs to overlay onto the bundled snapshot, in
	// increasing precedence order.
	URLs []string
	// CacheDir is where fetched catalogs are cached.
	CacheDir string
	// TTL is how long a cached copy is served before re-fetching.
	TTL time.Duration
	// HTTP is the client used for fetches.
	HTTP *http.Client
	// Now is the clock, injectable for tests.
	Now func() time.Time
}

// Default returns the loader every surface uses: the configured remote catalogs
// (TY_PLUGIN_REGISTRY, else the community catalog) over the bundled snapshot,
// cached under the user cache dir.
func Default() *Loader {
	return &Loader{
		URLs:     ConfiguredURLs(),
		CacheDir: DefaultCacheDir(),
		TTL:      DefaultTTL,
		HTTP:     &http.Client{Timeout: 8 * time.Second},
		Now:      time.Now,
	}
}

// ConfiguredURLs returns the remote catalogs to consult. TY_PLUGIN_REGISTRY
// replaces the default list; setting it to an empty string disables remote
// catalogs entirely (bundled snapshot only).
func ConfiguredURLs() []string {
	raw, set := os.LookupEnv(CatalogEnv)
	if !set {
		return []string{DefaultCatalogURL}
	}
	var urls []string
	for _, u := range strings.Split(raw, ",") {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	return urls
}

// DefaultCacheDir returns where fetched catalogs are cached.
func DefaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ty", "plugin-registry")
}

// Bundled returns the catalog snapshot compiled into this binary.
func Bundled() (*Catalog, error) {
	data, err := bundled.ReadFile("catalog.json")
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes a catalog document and drops entries that could never be
// installed, so one bad row can't break discovery for the rest.
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	kept := c.Plugins[:0]
	for _, e := range c.Plugins {
		if e.Valid() {
			kept = append(kept, e)
		}
	}
	c.Plugins = kept
	return &c, nil
}

// Load returns the merged catalog: the bundled snapshot with each configured
// remote catalog overlaid by entry ID. It never fails — a dead network, a
// corrupt cache, or a malformed remote document all degrade to what is known
// locally, and the reason lands in the returned Origins.
func (l *Loader) Load(ctx context.Context) Result {
	var res Result
	merged := map[string]Entry{}
	order := []string{}

	add := func(entries []Entry) {
		for _, e := range entries {
			if _, seen := merged[e.ID]; !seen {
				order = append(order, e.ID)
			}
			merged[e.ID] = e
		}
	}

	if c, err := Bundled(); err == nil {
		add(c.Plugins)
		res.Origins = append(res.Origins, Origin{URL: BundledOrigin, From: BundledOrigin, Entries: len(c.Plugins)})
	} else {
		res.Origins = append(res.Origins, Origin{URL: BundledOrigin, From: BundledOrigin, Err: err})
	}

	for _, url := range l.URLs {
		c, origin := l.loadRemote(ctx, url, false)
		res.Origins = append(res.Origins, origin)
		if c != nil {
			add(c.Plugins)
		}
	}

	res.Entries = make([]Entry, 0, len(order))
	for _, id := range order {
		res.Entries = append(res.Entries, merged[id])
	}
	sortEntries(res.Entries)
	return res
}

// Refresh is Load with every cache entry bypassed — what a "refresh catalog"
// button does.
func (l *Loader) Refresh(ctx context.Context) Result {
	var res Result
	merged := map[string]Entry{}
	order := []string{}
	add := func(entries []Entry) {
		for _, e := range entries {
			if _, seen := merged[e.ID]; !seen {
				order = append(order, e.ID)
			}
			merged[e.ID] = e
		}
	}
	if c, err := Bundled(); err == nil {
		add(c.Plugins)
		res.Origins = append(res.Origins, Origin{URL: BundledOrigin, From: BundledOrigin, Entries: len(c.Plugins)})
	}
	for _, url := range l.URLs {
		c, origin := l.loadRemote(ctx, url, true)
		res.Origins = append(res.Origins, origin)
		if c != nil {
			add(c.Plugins)
		}
	}
	res.Entries = make([]Entry, 0, len(order))
	for _, id := range order {
		res.Entries = append(res.Entries, merged[id])
	}
	sortEntries(res.Entries)
	return res
}

// loadRemote returns one remote catalog, serving a fresh-enough cache without
// touching the network unless force is set.
func (l *Loader) loadRemote(ctx context.Context, url string, force bool) (*Catalog, Origin) {
	now := l.now()
	path := l.cachePath(url)

	if !force && path != "" {
		if data, mod, err := readCache(path); err == nil {
			if age := now.Sub(mod); age < l.ttl() {
				if c, perr := Parse(data); perr == nil {
					return c, Origin{URL: url, From: "cache", Entries: len(c.Plugins), Age: age}
				}
			}
		}
	}

	data, err := l.fetch(ctx, url)
	if err == nil {
		c, perr := Parse(data)
		if perr == nil {
			l.writeCache(path, data)
			return c, Origin{URL: url, From: "network", Entries: len(c.Plugins)}
		}
		err = perr
	}

	// Network (or the document) let us down: any cache at all beats nothing.
	if path != "" {
		if cached, mod, cerr := readCache(path); cerr == nil {
			if c, perr := Parse(cached); perr == nil {
				return c, Origin{URL: url, From: "cache", Entries: len(c.Plugins), Age: now.Sub(mod), Err: err}
			}
		}
	}
	return nil, Origin{URL: url, From: "unavailable", Err: err}
}

func (l *Loader) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	client := l.HTTP
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	// Bound the read: a catalog is a few KB, and a redirect to something huge
	// should not be able to fill memory or the cache dir.
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func (l *Loader) cachePath(url string) string {
	if l.CacheDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(l.CacheDir, hex.EncodeToString(sum[:8])+".json")
}

func readCache(path string) ([]byte, time.Time, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	return data, fi.ModTime(), nil
}

func (l *Loader) writeCache(path string, data []byte) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func (l *Loader) ttl() time.Duration {
	if l.TTL <= 0 {
		return DefaultTTL
	}
	return l.TTL
}

func (l *Loader) now() time.Time {
	if l.Now == nil {
		return time.Now()
	}
	return l.Now()
}

// sortEntries orders entries by category then display name, so a browse listing
// reads as a grouped catalog rather than JSON order.
func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Category != entries[j].Category {
			return entries[i].Category < entries[j].Category
		}
		return strings.ToLower(entries[i].DisplayName()) < strings.ToLower(entries[j].DisplayName())
	})
}

// Find returns the entry with the given ID (case-insensitive).
func Find(entries []Entry, id string) (Entry, bool) {
	for _, e := range entries {
		if strings.EqualFold(e.ID, id) {
			return e, true
		}
	}
	return Entry{}, false
}

// Categories returns the distinct categories present, sorted.
func Categories(entries []Entry) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.Category == "" || seen[e.Category] {
			continue
		}
		seen[e.Category] = true
		out = append(out, e.Category)
	}
	sort.Strings(out)
	return out
}
