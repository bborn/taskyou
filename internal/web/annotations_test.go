package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// 1x1 transparent PNG
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func annotationBody(screenshot bool) string {
	b := map[string]interface{}{
		"url":         "http://localhost:3142/products",
		"title":       "Products",
		"instruction": "fix the button",
		"viewport":    map[string]interface{}{"width": 1440, "height": 900, "dpr": 2},
		"annotations": []map[string]interface{}{
			{"kind": "element", "label": 1, "selector": "#save-btn", "tag": "button",
				"text": "Save", "html": "<button id=\"save-btn\">Save</button>",
				"rect":    map[string]float64{"x": 1, "y": 2, "w": 3, "h": 4},
				"styles":  map[string]string{"color": "rgb(0, 0, 0)"},
				"comment": "make this primary"},
			{"kind": "note", "label": 2, "comment": "general note"},
		},
	}
	if screenshot {
		b["screenshot"] = "data:image/png;base64," + tinyPNG
	}
	out, _ := json.Marshal(b)
	return string(out)
}

func setupAnnotationTask(t *testing.T, database *db.DB, withPane bool) (*db.Task, string) {
	t.Helper()
	wt := t.TempDir()
	task := &db.Task{Title: "Anno task", Status: db.StatusProcessing, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	task.WorktreePath = wt
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("update task: %v", err)
	}
	if withPane {
		database.UpdateTaskPaneIDs(task.ID, "%9", "")
	}
	return task, wt
}

// setupAnnotationServer is setupServer with a coalesce window short enough to
// keep tests fast, but long enough that two back-to-back posts still merge.
func setupAnnotationServer(t *testing.T) (*Server, *db.DB, *mockRunner) {
	t.Helper()
	srv, database, runner := setupServer(t)
	srv.annoWindow = 80 * time.Millisecond
	return srv, database, runner
}

func postAnnotations(t *testing.T, srv *Server, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/annotations", id), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()
	srv.handleTaskAnnotations(w, req)
	return w
}

func TestHandleAnnotations_WritesBundleAndNudges(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, wt := setupAnnotationTask(t, database, true)

	w := postAnnotations(t, srv, task.ID, annotationBody(true))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		OK     bool   `json:"ok"`
		Path   string `json:"path"`
		Nudged bool   `json:"nudged"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.OK || !resp.Nudged {
		t.Fatalf("resp = %+v, want ok+nudged", resp)
	}

	// Bundle on disk
	mdPath := filepath.Join(wt, resp.Path)
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read annotation.md: %v", err)
	}
	for _, want := range []string{"http://localhost:3142/products", "make this primary", "#save-btn", "screenshot.png", "fix the button"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("annotation.md missing %q", want)
		}
	}
	png, err := os.ReadFile(filepath.Join(filepath.Dir(mdPath), "screenshot.png"))
	if err != nil {
		t.Fatalf("read screenshot.png: %v", err)
	}
	wantPNG, _ := base64.StdEncoding.DecodeString(tinyPNG)
	if string(png) != string(wantPNG) {
		t.Error("screenshot.png content mismatch")
	}

	// .gitignore guard
	gi, err := os.ReadFile(filepath.Join(wt, ".taskyou", "annotations", ".gitignore"))
	if err != nil || strings.TrimSpace(string(gi)) != "*" {
		t.Errorf("gitignore = %q err=%v, want *", gi, err)
	}

	// Literal nudge then Enter, once the coalesce window closes.
	calls := runner.waitForCalls(t, 2)
	if len(calls) != 2 {
		t.Fatalf("expected 2 tmux calls, got %d: %v", len(calls), calls)
	}
	first := calls[0]
	if first[0] != "tmux" || first[1] != "send-keys" || first[2] != "-t" || first[3] != "%9" || first[4] != "-l" {
		t.Errorf("first call = %v, want literal send-keys to %%9", first)
	}
	if !strings.Contains(first[5], resp.Path) {
		t.Errorf("nudge %q missing bundle path %q", first[5], resp.Path)
	}
	if strings.ContainsAny(first[5], "\n") {
		t.Error("nudge must be single-line")
	}
	second := calls[1]
	if fmt.Sprint(second) != fmt.Sprint([]string{"tmux", "send-keys", "-t", "%9", "Enter"}) {
		t.Errorf("second call = %v, want bare Enter", second)
	}
}

func TestHandleAnnotations_NoPane_StillWrites(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, wt := setupAnnotationTask(t, database, false)

	w := postAnnotations(t, srv, task.ID, annotationBody(false))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Nudged bool   `json:"nudged"`
		Path   string `json:"path"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Nudged {
		t.Error("nudged = true, want false")
	}
	if _, err := os.Stat(filepath.Join(wt, resp.Path)); err != nil {
		t.Errorf("bundle not written: %v", err)
	}
	// No pane, so the flush has nothing to nudge.
	time.Sleep(200 * time.Millisecond)
	if got := runner.snapshot(); len(got) != 0 {
		t.Errorf("expected no tmux calls, got %v", got)
	}
	md, _ := os.ReadFile(filepath.Join(wt, resp.Path))
	if strings.Contains(string(md), "screenshot.png") {
		t.Error("annotation.md should not reference missing screenshot")
	}
}

func TestHandleAnnotations_FallsBackToProjectPath(t *testing.T) {
	srv, database, _ := setupServer(t)
	projDir := t.TempDir()
	database.CreateProject(&db.Project{Name: "annoproj", Path: projDir})
	task := &db.Task{Title: "No worktree", Status: db.StatusProcessing, Project: "annoproj"}
	database.CreateTask(task)

	w := postAnnotations(t, srv, task.ID, annotationBody(false))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Path string `json:"path"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if _, err := os.Stat(filepath.Join(projDir, resp.Path)); err != nil {
		t.Errorf("bundle not in project dir: %v", err)
	}
}

func TestHandleAnnotations_MissingRootDir(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := &db.Task{Title: "Rootless", Status: db.StatusProcessing, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	task.WorktreePath = "/nonexistent/ty-chrome-test-root"
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("update task: %v", err)
	}

	w := postAnnotations(t, srv, task.ID, annotationBody(false))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAnnotations_EmptyAnnotations(t *testing.T) {
	srv, database, _ := setupServer(t)
	task, _ := setupAnnotationTask(t, database, true)

	w := postAnnotations(t, srv, task.ID, `{"url":"http://x","annotations":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// Two submissions landing inside the coalesce window belong to one thought:
// one bundle, one prompt for the executor.
func TestHandleAnnotations_CoalescesRapidSubmissions(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, wt := setupAnnotationTask(t, database, true)

	first := postAnnotations(t, srv, task.ID, annotationBody(true))
	second := postAnnotations(t, srv, task.ID, annotationBody(true))

	var r1, r2 struct {
		Path string `json:"path"`
	}
	json.NewDecoder(first.Body).Decode(&r1)
	json.NewDecoder(second.Body).Decode(&r2)
	if r1.Path != r2.Path {
		t.Errorf("submissions went to different bundles: %q vs %q", r1.Path, r2.Path)
	}

	entries, _ := os.ReadDir(filepath.Join(wt, ".taskyou", "annotations"))
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs != 1 {
		t.Errorf("expected 1 bundle dir, got %d", dirs)
	}

	// Both screenshots kept, both submissions rendered.
	md, _ := os.ReadFile(filepath.Join(wt, r1.Path))
	for _, want := range []string{"2 submissions", "Submission 1", "Submission 2", "screenshot-2.png"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("annotation.md missing %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(wt, filepath.Dir(r1.Path), "screenshot-2.png")); err != nil {
		t.Errorf("second screenshot not written: %v", err)
	}

	// Exactly one nudge, not two.
	calls := runner.waitForCalls(t, 2)
	time.Sleep(150 * time.Millisecond)
	if got := runner.snapshot(); len(got) != 2 {
		t.Errorf("expected 1 nudge (2 tmux calls), got %d: %v", len(got), got)
	}
	if !strings.Contains(calls[0][5], "2 submissions") {
		t.Errorf("nudge should say how many submissions: %q", calls[0][5])
	}
}

// Submissions spaced beyond the window are separate thoughts: separate bundles,
// separate nudges.
func TestHandleAnnotations_SeparateBundlesWhenSpaced(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, _ := setupAnnotationTask(t, database, true)

	postAnnotations(t, srv, task.ID, annotationBody(false))
	runner.waitForCalls(t, 2)
	postAnnotations(t, srv, task.ID, annotationBody(false))
	calls := runner.waitForCalls(t, 4)

	if calls[0][5] == calls[2][5] {
		t.Errorf("both nudges point at the same bundle: %q", calls[0][5])
	}
}

// The literal text and its Enter must stay adjacent: interleaved nudges would
// concatenate two prompts into one garbled line.
func TestAnnotationNudges_DoNotInterleave(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	// Without a per-nudge cost the two calls almost never get preempted, and
	// the test would pass even with the serialization removed.
	runner.mu.Lock()
	runner.delay = 5 * time.Millisecond
	runner.mu.Unlock()

	const tasks = 6
	ids := make([]int64, 0, tasks)
	for i := 0; i < tasks; i++ {
		task, _ := setupAnnotationTask(t, database, false)
		// Distinct pane per task so interleaving is visible in the transcript.
		database.UpdateTaskPaneIDs(task.ID, fmt.Sprintf("%%%d", 100+i), "")
		ids = append(ids, task.ID)
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			postAnnotations(t, srv, id, annotationBody(false))
		}(id)
	}
	wg.Wait()

	calls := runner.waitForCalls(t, tasks*2)
	if len(calls) != tasks*2 {
		t.Fatalf("expected %d tmux calls, got %d", tasks*2, len(calls))
	}
	for i := 0; i < len(calls); i += 2 {
		text, enter := calls[i], calls[i+1]
		if text[4] != "-l" {
			t.Fatalf("call %d is not a literal nudge: %v", i, text)
		}
		if enter[len(enter)-1] != "Enter" {
			t.Errorf("nudge at %d was not immediately followed by Enter: %v", i, enter)
		}
		if text[3] != enter[3] {
			t.Errorf("nudge/Enter pair split across panes: %s then %s", text[3], enter[3])
		}
	}
}
