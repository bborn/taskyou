package web

import (
	"context"
	"fmt"
	"github.com/bborn/workflow/internal/db"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type logStreamRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *logStreamRecorder) Flush() {
	w.ResponseRecorder.Flush()
	if strings.Contains(w.Body.String(), "event: log") {
		w.cancel()
	}
}

func TestLogStreamResumesAndBoundsCatchup(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := &db.Task{Title: "Stream cursor", Status: db.StatusProcessing}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<510) INSERT INTO task_logs(task_id,line_type,content,created_at) SELECT ?,'output',x,datetime('now') FROM n`, task.ID); err != nil {
		t.Fatal(err)
	}
	logs, err := database.GetTaskLogsSince(task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "/api/tasks/1/stream?since=0", nil).WithContext(ctx)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	req.Header.Set("Last-Event-ID", fmt.Sprint(logs[4].ID))
	w := &logStreamRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	(&Server{db: database}).handleTaskStream(w, req)
	body := w.Body.String()
	if strings.Count(body, "event: log\n") != 500 {
		t.Fatal("catch-up page was not bounded")
	}
	if strings.Contains(body, fmt.Sprintf("id: %d\n", logs[4].ID)) {
		t.Fatal("reconnect replayed acknowledged row")
	}
	if !strings.Contains(body, fmt.Sprintf("id: %d\n", logs[5].ID)) || !strings.Contains(body, fmt.Sprintf("id: %d\n", logs[504].ID)) {
		t.Fatal("stream skipped a row or omitted event cursor")
	}
	if strings.Contains(body, fmt.Sprintf("id: %d\n", logs[505].ID)) {
		t.Fatal("stream exceeded batch limit")
	}
}
