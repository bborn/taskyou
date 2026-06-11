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
	"testing"

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

func postAnnotations(t *testing.T, srv *Server, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/annotations", id), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()
	srv.handleTaskAnnotations(w, req)
	return w
}

func TestHandleAnnotations_WritesBundleAndNudges(t *testing.T) {
	srv, database, runner := setupServer(t)
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

	// Literal nudge then Enter
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 tmux calls, got %d: %v", len(runner.calls), runner.calls)
	}
	first := runner.calls[0]
	if first[0] != "tmux" || first[1] != "send-keys" || first[2] != "-t" || first[3] != "%9" || first[4] != "-l" {
		t.Errorf("first call = %v, want literal send-keys to %%9", first)
	}
	if !strings.Contains(first[5], resp.Path) {
		t.Errorf("nudge %q missing bundle path %q", first[5], resp.Path)
	}
	if strings.ContainsAny(first[5], "\n") {
		t.Error("nudge must be single-line")
	}
	second := runner.calls[1]
	if fmt.Sprint(second) != fmt.Sprint([]string{"tmux", "send-keys", "-t", "%9", "Enter"}) {
		t.Errorf("second call = %v, want bare Enter", second)
	}
}

func TestHandleAnnotations_NoPane_StillWrites(t *testing.T) {
	srv, database, runner := setupServer(t)
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
	if len(runner.calls) != 0 {
		t.Errorf("expected no tmux calls, got %v", runner.calls)
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
