package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestHandleListViews(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/views", nil)
	w := httptest.NewRecorder()
	srv.handleListViews(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var views []*db.SavedView
	if err := json.NewDecoder(w.Body).Decode(&views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) == 0 {
		t.Fatal("expected the seeded starter views")
	}
}

func TestHandleCreateAndDeleteView(t *testing.T) {
	srv, database, _ := setupServer(t)

	body := strings.NewReader(`{"name":"Mine","query":"status:blocked"}`)
	req := httptest.NewRequest("POST", "/api/views", body)
	w := httptest.NewRecorder()
	srv.handleCreateView(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	stored, err := database.GetSavedView("Mine")
	if err != nil || stored == nil {
		t.Fatalf("view not stored: %v %+v", err, stored)
	}

	req = httptest.NewRequest("DELETE", "/api/views/Mine", nil)
	req.SetPathValue("name", "Mine")
	w = httptest.NewRecorder()
	srv.handleDeleteView(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if v, _ := database.GetSavedView("Mine"); v != nil {
		t.Error("view should be gone")
	}
}

func TestHandleCreateViewRejectsBlankName(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("POST", "/api/views", strings.NewReader(`{"name":"  ","query":"is:pinned"}`))
	w := httptest.NewRecorder()
	srv.handleCreateView(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// The API must resolve a view the same way the board does, or a client that
// trusts it would render a different set of tasks under the same name.
func TestHandleGetViewMatchesTasks(t *testing.T) {
	srv, database, _ := setupServer(t)

	blocked := &db.Task{Title: "Waiting on review", Project: "personal", Status: db.StatusBlocked}
	if err := database.CreateTask(blocked); err != nil {
		t.Fatal(err)
	}
	done := &db.Task{Title: "Shipped", Project: "personal", Status: db.StatusDone}
	if err := database.CreateTask(done); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveView("Blocked", "status:blocked"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/views/Blocked", nil)
	req.SetPathValue("name", "Blocked")
	w := httptest.NewRecorder()
	srv.handleGetView(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var payload struct {
		Name      string      `json:"name"`
		Query     string      `json:"query"`
		TaskCount int         `json:"task_count"`
		Tasks     []*taskJSON `json:"tasks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.TaskCount != 1 || len(payload.Tasks) != 1 {
		t.Fatalf("expected exactly the blocked task, got %d: %s", payload.TaskCount, w.Body.String())
	}
	if payload.Tasks[0].ID != blocked.ID {
		t.Errorf("matched the wrong task: #%d", payload.Tasks[0].ID)
	}
}

func TestHandleGetViewNotFound(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/views/nope", nil)
	req.SetPathValue("name", "nope")
	w := httptest.NewRecorder()
	srv.handleGetView(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUpdateViewRenames(t *testing.T) {
	srv, database, _ := setupServer(t)
	if _, err := database.SaveView("Old", "is:pinned"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("PATCH", "/api/views/Old", strings.NewReader(`{"name":"New","query":"status:done"}`))
	req.SetPathValue("name", "Old")
	w := httptest.NewRecorder()
	srv.handleUpdateView(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if v, _ := database.GetSavedView("Old"); v != nil {
		t.Error("the old name should be gone after a rename")
	}
	v, _ := database.GetSavedView("New")
	if v == nil || v.Query != "status:done" {
		t.Errorf("renamed view = %+v", v)
	}
}
