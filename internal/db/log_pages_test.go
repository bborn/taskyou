package db

import (
	"path/filepath"
	"testing"
)

func TestLogPagesPreserveHistoryAndStreamingCursor(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := &Task{Title: "Log history", Status: StatusProcessing}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1201) INSERT INTO task_logs(task_id,line_type,content,created_at) SELECT ?,'output',x,datetime('now') FROM n`, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defaultPage, err := database.GetTaskLogs(task.ID, 0)
	if err != nil || len(defaultPage) != 1000 {
		t.Fatalf("default log limit changed: %d, %v", len(defaultPage), err)
	}
	page, err := database.GetTaskLogs(task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 100 {
		t.Fatal(len(page))
	}
	before := page[len(page)-1].ID
	total := len(page)
	for {
		page, err = database.GetTaskLogsBefore(task.ID, before, 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if page[0].ID >= before {
			t.Fatal("overlapping history page")
		}
		total += len(page)
		before = page[len(page)-1].ID
	}
	if total != 1201 {
		t.Fatalf("history lost rows: %d", total)
	}
	var since int64
	total = 0
	for {
		page, err = database.GetTaskLogsSinceLimit(task.ID, since, 500)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > 500 || page[0].ID <= since {
			t.Fatal("unbounded or replayed stream page")
		}
		total += len(page)
		since = page[len(page)-1].ID
	}
	if total != 1201 {
		t.Fatalf("stream lost rows: %d", total)
	}
}
