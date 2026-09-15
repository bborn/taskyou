package db

import (
	"testing"
	"time"
)

func TestRestartIdleClock(t *testing.T) {
	database := setupTestDB(t)

	newTask := func(status string) *Task {
		task := &Task{Title: "idle", Status: StatusBacklog, Project: "personal"}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		if err := database.UpdateTaskStatus(task.ID, StatusProcessing); err != nil {
			t.Fatalf("start task: %v", err)
		}
		if err := database.UpdateTaskStatus(task.ID, status); err != nil {
			t.Fatalf("set status: %v", err)
		}
		// Parked well past the default six-hour idle timeout.
		if _, err := database.Exec(`UPDATE tasks SET completed_at = datetime('now', '-1 day') WHERE id = ?`, task.ID); err != nil {
			t.Fatalf("backdate completed_at: %v", err)
		}
		return task
	}
	completedAt := func(id int64) time.Time {
		task, err := database.GetTask(id)
		if err != nil || task == nil || task.CompletedAt == nil {
			t.Fatalf("get task %d: %v", id, err)
		}
		return task.CompletedAt.Time
	}

	blocked := newTask(StatusBlocked)
	if err := database.RestartIdleClock(blocked.ID); err != nil {
		t.Fatalf("RestartIdleClock: %v", err)
	}
	if idle := time.Since(completedAt(blocked.ID)); idle > time.Hour {
		t.Errorf("blocked task still idle for %v after restart", idle)
	}

	// A done task's completed_at is when it finished; it is not an idle clock.
	done := newTask(StatusDone)
	if err := database.RestartIdleClock(done.ID); err != nil {
		t.Fatalf("RestartIdleClock: %v", err)
	}
	if idle := time.Since(completedAt(done.ID)); idle < 12*time.Hour {
		t.Errorf("done task's completed_at moved: idle %v", idle)
	}
}
