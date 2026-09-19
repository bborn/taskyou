package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// catalogResponse mirrors what handlePluginCatalog returns.
type catalogResponse struct {
	Plugins []catalogEntryJSON `json:"plugins"`
	Stale   bool               `json:"stale"`
}

func getCatalog(t *testing.T, srv *Server, query string) catalogResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/plugins/catalog"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/plugins/catalog%s = %d: %s", query, rec.Code, rec.Body.String())
	}
	var resp catalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return resp
}

func findEntry(rows []catalogEntryJSON, id string) *catalogEntryJSON {
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i]
		}
	}
	return nil
}

// The catalog is served from the snapshot bundled into the binary, so the GUI has
// something to show with no network and no configuration.
func TestHandlePluginCatalog_ServesBundledEntries(t *testing.T) {
	setupPluginsDir(t)
	t.Setenv("TY_PLUGIN_REGISTRY", "") // bundled only; no network in tests
	srv, _, _ := setupServer(t)

	resp := getCatalog(t, srv, "")
	if len(resp.Plugins) == 0 {
		t.Fatal("catalog is empty; the GUI would have nothing to browse")
	}
	slack := findEntry(resp.Plugins, "slack")
	if slack == nil {
		t.Fatalf("slack missing from the catalog: %+v", resp.Plugins)
	}
	if slack.Installed {
		t.Error("nothing is installed in this test's plugins dir")
	}
	if !slack.InCatalog || slack.Source == "" {
		t.Errorf("slack entry = %+v, want a catalogued entry with a source", slack)
	}
}

func TestHandlePluginCatalog_SearchAndScope(t *testing.T) {
	dir := setupPluginsDir(t)
	t.Setenv("TY_PLUGIN_REGISTRY", "")
	srv, _, _ := setupServer(t)
	writeActionPlugin(t, dir, "slack", "ping", "#!/bin/sh\necho hi\n")

	resp := getCatalog(t, srv, "?q=slack")
	if len(resp.Plugins) != 1 || resp.Plugins[0].ID != "slack" {
		t.Fatalf("q=slack returned %d rows: %+v", len(resp.Plugins), resp.Plugins)
	}
	if !resp.Plugins[0].Installed {
		t.Error("a plugin whose manifest name matches the entry should read as installed")
	}

	installed := getCatalog(t, srv, "?scope=installed")
	if len(installed.Plugins) != 1 || installed.Plugins[0].ID != "slack" {
		t.Errorf("scope=installed = %+v, want just slack", installed.Plugins)
	}
	available := getCatalog(t, srv, "?scope=available")
	if findEntry(available.Plugins, "slack") != nil {
		t.Error("scope=available must not include an installed plugin")
	}
	if len(available.Plugins) == 0 {
		t.Error("scope=available should still list the rest of the catalog")
	}
}

// A plugin nobody catalogued still shows up, so the browser can manage it.
func TestHandlePluginCatalog_IncludesUncataloguedInstalls(t *testing.T) {
	dir := setupPluginsDir(t)
	t.Setenv("TY_PLUGIN_REGISTRY", "")
	srv, _, _ := setupServer(t)
	writeActionPlugin(t, dir, "my-own-thing", "go", "#!/bin/sh\necho hi\n")

	row := findEntry(getCatalog(t, srv, "").Plugins, "my-own-thing")
	if row == nil {
		t.Fatal("hand-installed plugin missing from the catalog listing")
	}
	if row.InCatalog {
		t.Error("in_catalog should be false for a plugin with no catalog entry")
	}
	if !row.Installed {
		t.Error("it is on disk; installed should be true")
	}
}

func TestHandleListPlugins(t *testing.T) {
	dir := setupPluginsDir(t)
	srv, _, _ := setupServer(t)
	writeActionPlugin(t, dir, "notify", "test", "#!/bin/sh\necho hi\n")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/plugins", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/plugins = %d", rec.Code)
	}
	var out []installedPluginJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Name != "notify" {
		t.Fatalf("installed plugins = %+v, want notify", out)
	}
	if len(out[0].Actions) != 1 {
		t.Errorf("notify actions = %v, want one", out[0].Actions)
	}
}

// Install and remove go through the same resolution as the CLI, including a
// local path as the source.
func TestHandleInstallAndRemovePlugin(t *testing.T) {
	pluginsDir := setupPluginsDir(t)
	t.Setenv("TY_PLUGIN_REGISTRY", "")
	srv, _, _ := setupServer(t)

	// A local git repo stands in for a remote one: no network in tests.
	repo := filepath.Join(t.TempDir(), "hello-plugin")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "plugin.yaml"),
		[]byte("name: hello-plugin\ndescription: d\nactions:\n  - id: hi\n    command: hi.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "hi.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t.local"},
		{"config", "user.name", "t"}, {"add", "-A"}, {"commit", "-qm", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"source":"` + repo + `"}`)
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plugins/install", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("install = %d: %s", rec.Code, rec.Body.String())
	}
	var install map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &install); err != nil {
		t.Fatal(err)
	}
	if install["ok"] != true {
		t.Fatalf("install response = %v", install)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "hello-plugin", "plugin.yaml")); err != nil {
		t.Fatalf("plugin not on disk after install: %v", err)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plugins/remove",
		strings.NewReader(`{"name":"hello-plugin"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("remove = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "hello-plugin")); !os.IsNotExist(err) {
		t.Error("plugin dir survived the remove")
	}
}

// A failed install answers 200 with ok:false and the reason, so the GUI can show
// the git error rather than a bare HTTP failure.
func TestHandleInstallPlugin_ReportsFailure(t *testing.T) {
	setupPluginsDir(t)
	t.Setenv("TY_PLUGIN_REGISTRY", "")
	srv, _, _ := setupServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plugins/install",
		strings.NewReader(`{"id":"no-such-plugin"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown id = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no-such-plugin") {
		t.Errorf("error should name what was asked for: %s", rec.Body.String())
	}
}
