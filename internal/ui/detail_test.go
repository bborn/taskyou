package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestNewDetailModel_BacklogTaskDoesNotStartExecutor verifies that when a task
// is in backlog status, viewing it in the TUI should NOT automatically start
// the executor session. This prevents unwanted execution of tasks that the user
// has explicitly chosen not to start yet.
func TestNewDetailModel_BacklogTaskDoesNotStartExecutor(t *testing.T) {
	// Create a task in backlog status (not meant to be executed yet)
	task := &db.Task{
		ID:     1,
		Title:  "Test backlog task",
		Status: db.StatusBacklog,
	}

	// Create DetailModel - when not in tmux (TMUX env var not set),
	// it returns nil command, so we can't test that path easily.
	// But when task is in backlog, we should never start the executor
	// regardless of tmux state.

	// For now, test that backlog tasks are properly identified
	if !shouldSkipAutoExecutor(task) {
		t.Error("backlog tasks should skip auto-executor")
	}
}

// TestNewDetailModel_QueuedTaskCanStartExecutor verifies that tasks in
// queued status CAN start the executor automatically when viewed.
func TestNewDetailModel_QueuedTaskCanStartExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test queued task",
		Status: db.StatusQueued,
	}

	if shouldSkipAutoExecutor(task) {
		t.Error("queued tasks should NOT skip auto-executor")
	}
}

// TestNewDetailModel_ProcessingTaskCanStartExecutor verifies that tasks in
// processing status CAN reconnect to the executor automatically when viewed.
func TestNewDetailModel_ProcessingTaskCanStartExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test processing task",
		Status: db.StatusProcessing,
	}

	if shouldSkipAutoExecutor(task) {
		t.Error("processing tasks should NOT skip auto-executor")
	}
}

// TestNewDetailModel_BlockedTaskCanStartExecutor verifies that tasks in
// blocked status CAN reconnect to the executor automatically when viewed.
func TestNewDetailModel_BlockedTaskCanStartExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test blocked task",
		Status: db.StatusBlocked,
	}

	if shouldSkipAutoExecutor(task) {
		t.Error("blocked tasks should NOT skip auto-executor")
	}
}

// TestNewDetailModel_DoneTaskSkipsAutoExecutor verifies that completed tasks
// should NOT auto-start the executor (they're done).
func TestNewDetailModel_DoneTaskSkipsAutoExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test done task",
		Status: db.StatusDone,
	}

	if !shouldSkipAutoExecutor(task) {
		t.Error("done tasks should skip auto-executor")
	}
}

// TestNewDetailModel_ArchivedTaskSkipsAutoExecutor verifies that archived tasks
// should NOT auto-start the executor.
func TestNewDetailModel_ArchivedTaskSkipsAutoExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test archived task",
		Status: db.StatusArchived,
	}

	if !shouldSkipAutoExecutor(task) {
		t.Error("archived tasks should skip auto-executor")
	}
}
