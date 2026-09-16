package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// A rejected rename used to destroy the original: the handler deleted the old
// row before SaveView validated the replacement, so an over-long name returned
// 500 and took the view with it.
func TestHandleUpdateViewRejectsBadNameWithoutLosingTheView(t *testing.T) {
	srv, database, _ := setupServer(t)
	if _, err := database.SaveView("Keeper", "is:pinned"); err != nil {
		t.Fatal(err)
	}

	long := strings.Repeat("x", db.MaxSavedViewName+1)
	req := httptest.NewRequest("PATCH", "/api/views/Keeper",
		strings.NewReader(`{"name":"`+long+`","query":"status:done"}`))
	req.SetPathValue("name", "Keeper")
	w := httptest.NewRecorder()
	srv.handleUpdateView(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for an over-long name, got %d: %s", w.Code, w.Body.String())
	}
	got, err := database.GetSavedView("Keeper")
	if err != nil || got == nil {
		t.Fatalf("the original view must survive a rejected rename: %v %+v", err, got)
	}
	if got.Query != "is:pinned" {
		t.Errorf("the original query changed: %q", got.Query)
	}
}

// SaveView upserts by name, so renaming onto another view would overwrite it.
// Losing a view to a rename is the same data loss in a different costume.
func TestHandleUpdateViewRefusesToRenameOntoAnother(t *testing.T) {
	srv, database, _ := setupServer(t)
	for _, name := range []string{"Alpha", "Beta"} {
		if _, err := database.SaveView(name, "is:pinned"); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest("PATCH", "/api/views/Alpha", strings.NewReader(`{"name":"Beta","query":"status:done"}`))
	req.SetPathValue("name", "Alpha")
	w := httptest.NewRecorder()
	srv.handleUpdateView(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"Alpha", "Beta"} {
		v, _ := database.GetSavedView(name)
		if v == nil {
			t.Errorf("%s should still exist", name)
		} else if v.Query != "is:pinned" {
			t.Errorf("%s query changed to %q", name, v.Query)
		}
	}
}

// The browser client filters through this parameter rather than carrying its
// own grammar, so a saved view and a typed query mean the same thing.
func TestHandleListTasksFilter(t *testing.T) {
	srv, database, _ := setupServer(t)

	blocked := &db.Task{Title: "Fix checkout", Project: "personal", Status: db.StatusBlocked, Pinned: true}
	done := &db.Task{Title: "Fix checkout", Project: "personal", Status: db.StatusDone}
	for _, task := range []*db.Task{blocked, done} {
		if err := database.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}
	// CreateTask does not carry pinned through, so set it explicitly.
	if err := database.UpdateTaskPinned(blocked.ID, true); err != nil {
		t.Fatal(err)
	}

	ids := func(query string) []int64 {
		req := httptest.NewRequest("GET", "/api/tasks?all=true&filter="+url.QueryEscape(query), nil)
		w := httptest.NewRecorder()
		srv.handleListTasks(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("filter %q: expected 200, got %d: %s", query, w.Code, w.Body.String())
		}
		var out []*taskJSON
		if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var got []int64
		for _, task := range out {
			got = append(got, task.ID)
		}
		return got
	}

	// The two cases from the review: both titled the same, so only the grammar
	// separates them.
	if got := ids("status:in-progress status:blocked checkout"); len(got) != 1 || got[0] != blocked.ID {
		t.Errorf("status filter + keyword = %v, want just the blocked task #%d", got, blocked.ID)
	}
	if got := ids("is:pinned checkout"); len(got) != 1 || got[0] != blocked.ID {
		t.Errorf("is:pinned + keyword = %v, want just the pinned task #%d", got, blocked.ID)
	}
	if got := ids(""); len(got) != 2 {
		t.Errorf("no filter should return both, got %v", got)
	}
}
