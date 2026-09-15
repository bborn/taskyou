package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCountRecentExecutorStarts(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	res, err := database.Exec(`INSERT INTO tasks (title) VALUES ('t')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	for _, content := range []string{
		"Starting new claude session",
		"Reconnecting to claude session 94a90d73",
		"Starting new codex session",
		"Claude session: 94a90d73", // not a launch
	} {
		if err := database.AppendTaskLog(id, "system", content); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO task_logs (task_id, line_type, content, created_at)
		VALUES (?, 'system', 'Starting new claude session', datetime('now', '-10 minutes'))`, id); err != nil {
		t.Fatal(err)
	}

	n, err := database.CountRecentExecutorStarts(id, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("CountRecentExecutorStarts = %d, want 3 (the old launch and the non-launch line excluded)", n)
	}
}
