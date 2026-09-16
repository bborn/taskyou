package db

import (
	"path/filepath"
	"testing"
)

// TestHasSessionEnded: the agent's own exit report only counts for the session
// that is running now. A task relaunched after an earlier exit has a newer
// start line, so the old exit must stop counting — otherwise every retry would
// look like an agent that had already walked away.
func TestHasSessionEnded(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	task := &Task{Title: "session lifecycle", Status: StatusQueued, Type: TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	ended := func() bool {
		t.Helper()
		got, err := database.HasSessionEnded(task.ID)
		if err != nil {
			t.Fatalf("HasSessionEnded: %v", err)
		}
		return got
	}

	if ended() {
		t.Error("a task with no logs has not reported an exit")
	}

	database.AppendTaskLog(task.ID, "system", "Starting new claude session")
	if ended() {
		t.Error("a running session has not reported an exit")
	}

	database.AppendTaskLog(task.ID, "system", SessionEndedLogPrefix+" (other)")
	if !ended() {
		t.Error("the agent reported its exit and it was not seen")
	}

	database.AppendTaskLog(task.ID, "system", "Resuming existing session abc")
	if ended() {
		t.Error("an exit from before the relaunch must not count against the new session")
	}

	database.AppendTaskLog(task.ID, "system", SessionEndedLogPrefix)
	if !ended() {
		t.Error("the relaunched session's own exit should count")
	}
}
