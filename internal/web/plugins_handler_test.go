package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupPluginsDir isolates the plugins directory for a test and returns it.
func setupPluginsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TY_PLUGINS_DIR", dir)
	return dir
}

// writeActionPlugin creates a plugin with a single action script.
func writeActionPlugin(t *testing.T, root, name, actionID, script string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: " + name + "\nactions:\n  - id: " + actionID + "\n    label: " + actionID + "\n    command: run.sh\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestHandleListPluginActions(t *testing.T) {
	dir := setupPluginsDir(t)
	srv, _, _ := setupServer(t)
	writeActionPlugin(t, dir, "notify", "test", "#!/bin/sh\necho hi\n")

	req := httptest.NewRequest("GET", "/api/plugins/actions", nil)
	w := httptest.NewRecorder()
	srv.handleListPluginActions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var actions []pluginActionJSON
	if err := json.NewDecoder(w.Body).Decode(&actions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(actions) != 1 || actions[0].Plugin != "notify" || actions[0].ID != "test" {
		t.Fatalf("unexpected actions: %+v", actions)
	}
}

func TestHandleRunPluginAction(t *testing.T) {
	dir := setupPluginsDir(t)
	srv, _, _ := setupServer(t)
	writeActionPlugin(t, dir, "notify", "test",
		"#!/bin/sh\necho \"plugin=$TASK_PLUGIN_NAME\"\n")

	body := strings.NewReader(`{"plugin":"notify","action":"test"}`)
	req := httptest.NewRequest("POST", "/api/plugins/actions/run", body)
	w := httptest.NewRecorder()
	srv.handleRunPluginAction(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Output string `json:"output"`
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK || resp.Error != "" {
		t.Fatalf("run not ok: %+v", resp)
	}
	if strings.TrimSpace(resp.Output) != "plugin=notify" {
		t.Errorf("output = %q, want plugin=notify", strings.TrimSpace(resp.Output))
	}
}

func TestHandleRunPluginAction_UnknownAction(t *testing.T) {
	setupPluginsDir(t)
	srv, _, _ := setupServer(t)

	body := strings.NewReader(`{"plugin":"nope","action":"nope"}`)
	req := httptest.NewRequest("POST", "/api/plugins/actions/run", body)
	w := httptest.NewRecorder()
	srv.handleRunPluginAction(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRunPluginAction_MissingFields(t *testing.T) {
	setupPluginsDir(t)
	srv, _, _ := setupServer(t)

	body := strings.NewReader(`{"plugin":"notify"}`)
	req := httptest.NewRequest("POST", "/api/plugins/actions/run", body)
	w := httptest.NewRecorder()
	srv.handleRunPluginAction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
