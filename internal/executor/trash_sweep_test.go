package executor

import (
	"os"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// trashTask creates a task, soft-deletes it, and (optionally) backdates its
// deleted_at so it looks older than the retention window.
func trashTask(t *testing.T, database *db.DB, title string, trashedAge time.Duration) int64 {
	t.Helper()
	task := &db.Task{Title: title, Project: "personal", Type: db.TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	if err := database.SoftDeleteTask(task.ID); err != nil {
		t.Fatalf("soft-delete %q: %v", title, err)
	}
	if trashedAge > 0 {
		if _, err := database.Exec("UPDATE tasks SET deleted_at = ? WHERE id = ?",
			time.Now().Add(-trashedAge).UTC(), task.ID); err != nil {
			t.Fatalf("backdate %q: %v", title, err)
		}
	}
	return task.ID
}

func rowExists(t *testing.T, database *db.DB, id int64) bool {
	t.Helper()
	task, err := database.GetTask(id)
	if err != nil {
		t.Fatalf("get task %d: %v", id, err)
	}
	return task != nil
}

func newSweepExecutor(t *testing.T) (*Executor, *db.DB) {
	t.Helper()
	tmp, err := os.CreateTemp("", "trash-sweep-*.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmp.Name()) })
	tmp.Close()

	database, err := db.Open(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database, &config.Config{}), database
}

// TestSweepTrashedTasksRespectsRetention: only trash older than the retention is
// hard-deleted; fresh trash survives.
func TestSweepTrashedTasksRespectsRetention(t *testing.T) {
	exec, database := newSweepExecutor(t)

	old := trashTask(t, database, "old trash", 30*24*time.Hour)
	fresh := trashTask(t, database, "fresh trash", 0)

	exec.sweepTrashedTasks() // default 14d retention

	if rowExists(t, database, old) {
		t.Error("trash older than retention should be hard-deleted")
	}
	if !rowExists(t, database, fresh) {
		t.Error("freshly trashed task must survive the sweep")
	}
}

// TestSweepTrashedTasksDisabled: retention "0" turns the sweep off entirely.
func TestSweepTrashedTasksDisabled(t *testing.T) {
	exec, database := newSweepExecutor(t)
	if err := database.SetSetting(config.SettingTrashRetention, "0"); err != nil {
		t.Fatal(err)
	}

	old := trashTask(t, database, "ancient trash", 90*24*time.Hour)
	exec.sweepTrashedTasks()

	if !rowExists(t, database, old) {
		t.Error("sweep must be a no-op when retention is disabled")
	}
}
