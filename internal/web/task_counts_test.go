package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func getTaskCounts(t *testing.T, srv *Server, filter string) taskCountsJSON {
	t.Helper()
	v := url.Values{}
	if filter != "" {
		v.Set("filter", filter)
	}
	w := httptest.NewRecorder()
	srv.handleTaskCounts(w, httptest.NewRequest("GET", "/api/tasks/counts?"+v.Encode(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got taskCountsJSON
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

// More tasks than the web UI loads (1000): the counts must cover all of them,
// not the page. Before, the filter sheet counted what it had loaded.
func TestHandleTaskCountsCoversEveryTaskNotAPage(t *testing.T) {
	srv, database, _ := setupServer(t)
	for _, name := range []string{"alpha", "beta"} {
		if err := database.CreateProject(&db.Project{Name: name, Path: t.TempDir()}); err != nil {
			t.Fatalf("create project: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := database.CreateTask(&db.Task{Title: "stuck", Status: db.StatusBlocked, Project: "alpha"}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	for i := 0; i < 1100; i++ {
		project := "alpha"
		if i%2 == 1 {
			project = "beta"
		}
		if err := database.CreateTask(&db.Task{Title: "shipped", Status: db.StatusDone, Project: project}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	if err := database.CreateTask(&db.Task{Title: "old", Status: db.StatusArchived, Project: "alpha"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got := getTaskCounts(t, srv, "")
	if got.Total != 1103 {
		t.Errorf("total = %d, want 1103 (every task but the archived one)", got.Total)
	}
	if got.Status[db.StatusDone] != 1100 || got.Status[db.StatusBlocked] != 3 {
		t.Errorf("status counts = %v, want done 1100 and blocked 3", got.Status)
	}
	if _, ok := got.Status[db.StatusArchived]; ok {
		t.Errorf("archived tasks were counted: %v", got.Status)
	}
	if got.Project["alpha"] != 553 || got.Project["beta"] != 550 {
		t.Errorf("project counts = %v, want alpha 553 and beta 550", got.Project)
	}

	blocked := getTaskCounts(t, srv, "status:blocked")
	if blocked.Total != 3 || blocked.Project["alpha"] != 3 {
		t.Errorf("status:blocked counts = %+v, want 3, all in alpha", blocked)
	}
}
