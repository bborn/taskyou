package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestPlacementAPIChangesHostAndReportsHealth(t *testing.T) {
	s, d, _ := setupServer(t)
	task := &db.Task{Title: "Ship remote worker", Executor: "claude"}
	if err := d.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/tasks/1/placement", strings.NewReader(`{"target":"local"}`))
	w := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("placement: %d %s", w.Code, w.Body.String())
	}
	if err := d.SetTaskPlacementDecision(task.ID, "build", "most available memory", "/srv/app"); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordHostHealth("build", "", true); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/tasks/1/placement", nil))
	var got struct {
		Target string        `json:"target"`
		Health db.HostHealth `json:"health"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || got.Target != "build" || got.Health.State != "online" {
		t.Fatalf("health: %s", w.Body.String())
	}
}

func TestPlacementAPIRejectsUnsupportedExecutorWithoutMoving(t *testing.T) {
	s, d, _ := setupServer(t)
	task := &db.Task{Title: "Remote work", Executor: "gemini"}
	if err := d.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/tasks/1/placement", strings.NewReader(`{"target":"build","workdir":"/srv/app"}`)))
	if w.Code != 409 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	p, _ := d.GetTaskPlacementDecision(task.ID)
	if p.Decided {
		t.Fatal("unsupported executor was placed")
	}
}
