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
		// Status is append-only, so the fixture makes the transitions the daemon
		// would rather than assigning the field: a parked task has genuinely run,
		// which is what gives it the completed_at this test is about.
		if err := database.SetTaskStatus(task.ID, StatusProcessing, ActorDaemon,
			"test fixture: the task started running", NoEvidence); err != nil {
			t.Fatalf("start task: %v", err)
		}
		actor, ev := ActorDaemon, Observedf("the agent finished its turn")
		if status == StatusDone {
			// Only a human closes a task.
			actor, ev = ActorCLI, ByHuman("ran `ty close %d`", task.ID)
		}
		if err := database.SetTaskStatus(task.ID, status, actor,
			"test fixture: the task stopped and parked", ev); err != nil {
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
