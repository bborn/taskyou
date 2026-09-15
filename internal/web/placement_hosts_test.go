package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// installHostsPlugin writes a placement plugin that answers the hosts question
// with body, and points the plugin loader at it for this test only.
func installHostsPlugin(t *testing.T, body string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "ty-on")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"),
		[]byte("name: ty-on\nhooks:\n  task.hosts: hosts.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hosts.sh"),
		[]byte("#!/bin/sh\ncat <<'JSON'\n"+body+"\nJSON\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TY_PLUGINS_DIR", root)
}

// project creates the project these tasks belong to; CreateTask refuses one it
// does not know.
func project(t *testing.T, d *db.DB) {
	t.Helper()
	if err := d.CreateProject(&db.Project{Name: "taskyou", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func hostsFor(t *testing.T, s *Server, query string) []map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/placement/hosts?"+query, nil))
	if w.Code != 200 {
		t.Fatalf("hosts: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Hosts []map[string]any `json:"hosts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("hosts response: %v (%s)", err, w.Body.String())
	}
	if got.Hosts == nil {
		t.Fatalf("hosts came back as null rather than a list: %s", w.Body.String())
	}
	return got.Hosts
}

// The GUI asks this before it decides whether to show a host picker at all, so
// the answer has to be a list either way.
func TestPlacementHostsAPIListsWhatThePluginOffers(t *testing.T) {
	s, _, _ := setupServer(t)
	installHostsPlugin(t, `{"hosts":[{"name":"mona","target":"mona","workdir":"~/Projects/taskyou","detail":"agent"}]}`)

	hosts := hostsFor(t, s, "project=taskyou&executor=claude")

	if len(hosts) != 1 || hosts[0]["target"] != "mona" {
		t.Fatalf("hosts = %+v, want the offered host", hosts)
	}
	if hosts[0]["workdir"] != "~/Projects/taskyou" {
		t.Errorf("workdir = %v, want the checkout on that host", hosts[0]["workdir"])
	}
}

func TestPlacementHostsAPIIsEmptyWithNoPlugin(t *testing.T) {
	s, _, _ := setupServer(t)
	t.Setenv("TY_PLUGINS_DIR", t.TempDir())

	if hosts := hostsFor(t, s, "project=taskyou"); len(hosts) != 0 {
		t.Fatalf("hosts = %+v, want none", hosts)
	}
}

// Creating a task with a host records it as the task's placement, so the
// resolver is never asked for that task.
func TestCreateTaskWithAChosenHost(t *testing.T) {
	s, d, _ := setupServer(t)
	project(t, d)
	installHostsPlugin(t, `{"hosts":[{"name":"mona","target":"mona","workdir":"~/Projects/taskyou"}]}`)

	w := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/tasks", strings.NewReader(
		`{"title":"Run it over there","project":"taskyou","executor":"claude","placement":"mona"}`)))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	got, err := d.GetTaskPlacementDecision(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Decided || got.Target != "mona" {
		t.Fatalf("placement = %+v, want a decided placement on mona", got)
	}
	// The directory was never sent: it came from the same plugin that offered
	// the host, so the form only has to send the name.
	if got.WorkDir != "~/Projects/taskyou" {
		t.Errorf("workdir = %q, want the checkout the host list named", got.WorkDir)
	}
}

// Creating without one is the old behaviour exactly: nothing is decided, and
// the resolver answers at spawn.
func TestCreateTaskWithoutAHostDecidesNothing(t *testing.T) {
	s, d, _ := setupServer(t)
	project(t, d)

	w := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/tasks", strings.NewReader(
		`{"title":"Wherever you like","project":"taskyou"}`)))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	got, _ := d.GetTaskPlacementDecision(created.ID)
	if got.Decided {
		t.Fatalf("placement = %+v, want the resolver left to answer", got)
	}
}

// "This machine" is a decision: it pins the task here rather than leaving a
// later resolver free to ship it off.
func TestCreateTaskPinnedToThisMachine(t *testing.T) {
	s, d, _ := setupServer(t)
	project(t, d)

	w := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/tasks", strings.NewReader(
		`{"title":"Keep it here","project":"taskyou","placement":"local"}`)))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	got, _ := d.GetTaskPlacementDecision(created.ID)
	if !got.Decided || got.Target != "" {
		t.Fatalf("placement = %+v, want a decided local placement", got)
	}
}

// A host nothing can name a directory for is reported rather than recorded —
// and the task still exists, because the user's words are not worth losing over
// a placement.
func TestCreateTaskWithAnUnknownHostReportsItAndKeepsTheTask(t *testing.T) {
	s, d, _ := setupServer(t)
	project(t, d)
	t.Setenv("TY_PLUGINS_DIR", t.TempDir())

	w := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/tasks", strings.NewReader(
		`{"title":"Somewhere unknown","project":"taskyou","placement":"nowhere"}`)))
	if w.Code != 400 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	tasks, err := d.ListTasks(db.ListTasksOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want the task kept despite the failed placement", len(tasks))
	}
	got, _ := d.GetTaskPlacementDecision(tasks[0].ID)
	if got.Decided {
		t.Errorf("placement = %+v, want nothing recorded", got)
	}
}
