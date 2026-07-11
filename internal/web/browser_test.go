package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func browserReq(t *testing.T, srv *Server, method, path string, id int64, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestBrowserExec_NoSession(t *testing.T) {
	srv, database, _ := setupServer(t)
	task, _ := setupAnnotationTask(t, database, false)

	w := browserReq(t, srv, "POST", "/api/tasks/1/browser", task.ID, `{"action":"screenshot"}`, srv.handleBrowserExec)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBrowserRelay_RoundTrip(t *testing.T) {
	srv, database, _ := setupServer(t)
	task, wt := setupAnnotationTask(t, database, false)

	// Extension connects: first poll (no command yet) writes HOWTO and marks session
	pollDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		pollDone <- browserReq(t, srv, "GET", "/api/tasks/1/browser/poll", task.ID, "", srv.handleBrowserPoll)
	}()

	// Wait for the session to register
	deadline := time.Now().Add(2 * time.Second)
	for !srv.relay.connected(task.ID) {
		if time.Now().After(deadline) {
			t.Fatal("session never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// HOWTO dropped into the worktree
	howto, err := os.ReadFile(filepath.Join(wt, ".taskyou", "browser", "HOWTO.md"))
	if err != nil {
		t.Fatalf("HOWTO.md not written: %v", err)
	}
	if !strings.Contains(string(howto), fmt.Sprintf("/api/tasks/%d/browser", task.ID)) {
		t.Error("HOWTO missing task-specific endpoint")
	}

	// Executor sends a command
	execDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		execDone <- browserReq(t, srv, "POST", "/api/tasks/1/browser", task.ID, `{"action":"click","params":{"selector":"#go"}}`, srv.handleBrowserExec)
	}()

	// Poll returns the command
	pw := <-pollDone
	if pw.Code != http.StatusOK {
		t.Fatalf("poll: expected 200, got %d: %s", pw.Code, pw.Body.String())
	}
	var cmd struct {
		ID     string          `json:"id"`
		Action string          `json:"action"`
		Params json.RawMessage `json:"params"`
	}
	json.NewDecoder(pw.Body).Decode(&cmd)
	if cmd.Action != "click" || cmd.ID == "" {
		t.Fatalf("bad command: %+v", cmd)
	}

	// Extension posts the result
	rw := browserReq(t, srv, "POST", "/api/tasks/1/browser/result", task.ID,
		fmt.Sprintf(`{"id":%q,"result":{"ok":true,"clicked":"button"}}`, cmd.ID), srv.handleBrowserResult)
	if rw.Code != http.StatusOK {
		t.Fatalf("result: expected 200, got %d: %s", rw.Code, rw.Body.String())
	}

	// Executor gets it back
	ew := <-execDone
	if ew.Code != http.StatusOK {
		t.Fatalf("exec: expected 200, got %d: %s", ew.Code, ew.Body.String())
	}
	if !strings.Contains(ew.Body.String(), `"clicked":"button"`) {
		t.Errorf("exec body = %s", ew.Body.String())
	}
}

func TestBrowserRelay_ScreenshotWritesFile(t *testing.T) {
	srv, database, _ := setupServer(t)
	task, wt := setupAnnotationTask(t, database, false)

	pollDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		pollDone <- browserReq(t, srv, "GET", "/api/tasks/1/browser/poll", task.ID, "", srv.handleBrowserPoll)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !srv.relay.connected(task.ID) {
		if time.Now().After(deadline) {
			t.Fatal("session never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	execDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		execDone <- browserReq(t, srv, "POST", "/api/tasks/1/browser", task.ID, `{"action":"screenshot"}`, srv.handleBrowserExec)
	}()

	// The held poll picks up the command; post a data-URL result for it
	pw := <-pollDone
	var cmd struct {
		ID string `json:"id"`
	}
	json.NewDecoder(pw.Body).Decode(&cmd)
	browserReq(t, srv, "POST", "/api/tasks/1/browser/result", task.ID,
		fmt.Sprintf(`{"id":%q,"result":{"data":"data:image/png;base64,%s"}}`, cmd.ID, tinyPNG), srv.handleBrowserResult)

	ew := <-execDone
	if ew.Code != http.StatusOK {
		t.Fatalf("exec: expected 200, got %d: %s", ew.Code, ew.Body.String())
	}
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Path string `json:"path"`
		} `json:"result"`
	}
	json.NewDecoder(ew.Body).Decode(&resp)
	if resp.Result.Path == "" || !strings.HasSuffix(resp.Result.Path, ".png") {
		t.Fatalf("expected png path, got %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(wt, resp.Result.Path)); err != nil {
		t.Errorf("screenshot file missing: %v", err)
	}
	if strings.Contains(ew.Body.String(), "base64") {
		t.Error("raw base64 should not be returned to the executor")
	}
}

func TestBrowserHowto_DocumentsTabGroupActions(t *testing.T) {
	srv, database, _ := setupServer(t)
	task, wt := setupAnnotationTask(t, database, false)

	srv.ensureBrowserHowto(task)

	howto, err := os.ReadFile(filepath.Join(wt, ".taskyou", "browser", "HOWTO.md"))
	if err != nil {
		t.Fatalf("HOWTO.md not written: %v", err)
	}
	got := string(howto)
	// The bridge now advertises tab-group control and external navigation.
	for _, want := range []string{
		`"action":"tabs"`,
		`"action":"open"`,
		`"action":"activate"`,
		`"action":"close"`,
		`https://`, // navigate is no longer localhost-only
	} {
		if !strings.Contains(got, want) {
			t.Errorf("HOWTO missing %q", want)
		}
	}
}

func TestAnnotationNudge_MentionsBrowserWhenConnected(t *testing.T) {
	srv, database, runner := setupServer(t)
	task, _ := setupAnnotationTask(t, database, true)

	srv.relay.touch(task.ID) // simulate connected extension

	w := postAnnotations(t, srv, task.ID, annotationBody(false))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	nudge := runner.calls[0][5]
	if !strings.Contains(nudge, "HOWTO.md") {
		t.Errorf("nudge should mention browser HOWTO when connected: %q", nudge)
	}
}
