package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// A saved view targeting rows outside the 100 most-recent must still report
// them in its `tasks`/`task_count`: status:backlog against a board whose 100
// most-recent rows are all done returns 5, not the pre-fix 0.
func TestHandleGetViewReturnsMatchingTasksWhenRecentDoneCrowdsOutBacklog(t *testing.T) {
	srv, database, _ := setupServer(t)
	seedCrowdedBoard(t, database)
	if _, err := database.SaveView("BacklogView", "status:backlog"); err != nil {
		t.Fatalf("save view: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/views/BacklogView", nil)
	req.SetPathValue("name", "BacklogView")
	w := httptest.NewRecorder()
	srv.handleGetView(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		TaskCount int         `json:"task_count"`
		Tasks     []*taskJSON `json:"tasks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.TaskCount != 5 || len(res.Tasks) != 5 {
		t.Fatalf("task_count=%d len(tasks)=%d, want 5/5 (the 5 backlog rows outside the 100-row SQL window); body=%s",
			res.TaskCount, len(res.Tasks), w.Body.String())
	}
	for _, task := range res.Tasks {
		if task.Status != db.StatusBacklog {
			t.Errorf("view matched a %q task under a status:backlog view — wrong match", task.Status)
		}
	}
}
