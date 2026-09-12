package db

import (
	"path/filepath"
	"testing"
)

func TestGetLatestLogPerTask(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	tasks := make([]*Task, 3)
	for i := range tasks {
		tasks[i] = &Task{Title: "Review checkout coverage", Status: StatusBlocked}
		if err := database.CreateTask(tasks[i]); err != nil {
			t.Fatal(err)
		}
	}
	// Equal timestamps must still select the newest ID, independently per task.
	for _, entry := range []struct {
		task    int
		content string
	}{{0, "older"}, {1, "other task"}, {0, "latest"}} {
		if _, err := database.Exec("INSERT INTO task_logs(task_id,line_type,content,created_at) VALUES(?,'output',?,'2026-09-05 12:00:00')", tasks[entry.task].ID, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	for _, ids := range [][]int64{nil, {}, {tasks[0].ID, tasks[1].ID, tasks[2].ID, -1, tasks[0].ID}} {
		got, err := database.GetLatestLogPerTask(ids)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			if len(got) != 0 {
				t.Fatalf("empty request returned %v", got)
			}
			continue
		}
		if len(got) != 2 {
			t.Fatalf("got %d tasks, want two tasks with logs", len(got))
		}
		for i, want := range []string{"latest", "other task"} {
			log := got[tasks[i].ID]
			if log == nil || log.Content != want || log.LineType != "output" || log.TaskID != tasks[i].ID {
				t.Fatalf("task %d: got %+v, want %q", tasks[i].ID, log, want)
			}
		}
	}
}
