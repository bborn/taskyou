package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// seedCrowdedBoard inserts 5 backlog rows and then 105 done rows. Because
// ListTasks orders by recency then id DESC, the 100-row SQL window the bug
// added holds the 100 most-recent done rows; the 5 older backlog rows sit
// outside it.
func seedCrowdedBoard(t *testing.T, database *db.DB) {
	t.Helper()
	for i := 0; i < 5; i++ {
		if err := database.CreateTask(&db.Task{Title: "backlog", Status: db.StatusBacklog, Project: "personal"}); err != nil {
			t.Fatalf("create backlog task: %v", err)
		}
	}
	for i := 0; i < 105; i++ {
		if err := database.CreateTask(&db.Task{Title: "done", Status: db.StatusDone, Project: "personal"}); err != nil {
			t.Fatalf("create done task: %v", err)
		}
	}
}

// listTasksFilterReq builds a GET /api/tasks request encoding `filter` and
// `limit` as separate query params (so `&` in the filter grammar never
// collides with `limit`). `all=true` makes the done rows that drive the
// recency crowding visible to ListTasks.
func listTasksFilterReq(filter string, limit int) *http.Request {
	v := url.Values{}
	if filter != "" {
		v.Set("filter", filter)
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	v.Set("all", "true")
	return httptest.NewRequest("GET", "/api/tasks?"+v.Encode(), nil)
}

// A filter targeting rows outside the 100 most-recent must still match them:
// status:backlog against a board whose 100 most-recent rows are all done
// returns the 5 backlog rows, not the pre-fix 0.
func TestHandleListTasksFilterReturnsMatchingTasksWhenRecentDoneCrowdsOutBacklog(t *testing.T) {
	srv, database, _ := setupServer(t)
	seedCrowdedBoard(t, database)

	req := listTasksFilterReq("status:backlog", 1000)
	w := httptest.NewRecorder()
	srv.handleListTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got []*taskJSON
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len(tasks) = %d, want 5 (the 5 backlog rows outside the 100-row SQL window); body=%s",
			len(got), w.Body.String())
	}
	for _, task := range got {
		if task.Status != db.StatusBacklog {
			t.Errorf("matched a %q task under a status:backlog filter — wrong match", task.Status)
		}
	}
}

// The request's `limit` is the only post-match trim: a small `limit` against
// a wide filter is honoured even when the match extends past the 100-row SQL
// window.
func TestHandleListTasksFilterHonorsRequestLimitAfterRematching(t *testing.T) {
	srv, database, _ := setupServer(t)
	for i := 0; i < 110; i++ {
		if err := database.CreateTask(&db.Task{Title: "task", Status: db.StatusBacklog, Project: "personal"}); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}

	req := listTasksFilterReq("status:backlog", 20)
	w := httptest.NewRecorder()
	srv.handleListTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got []*taskJSON
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 20 {
		t.Fatalf("len(tasks) = %d, want 20 (the request's limit), not the full 110-row match", len(got))
	}
}
