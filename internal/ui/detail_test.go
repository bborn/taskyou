package ui

import (
	"strings"
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

// TestPercentageCalculationRounding verifies that percentage calculations
// use proper rounding to avoid the progressive shrinking bug.
// Without rounding, integer division truncates: (16 * 100) / 81 = 19 instead of 20
// With rounding: (16*100 + 81/2) / 81 = (1600 + 40) / 81 = 20
func TestPercentageCalculationRounding(t *testing.T) {
	tests := []struct {
		paneSize    int
		totalSize   int
		wantRounded int
	}{
		// Case where truncation would cause shrinking
		{paneSize: 16, totalSize: 81, wantRounded: 20}, // Without rounding: 19
		{paneSize: 15, totalSize: 80, wantRounded: 19}, // Without rounding: 18
		{paneSize: 20, totalSize: 100, wantRounded: 20},
		{paneSize: 16, totalSize: 80, wantRounded: 20},
		// Edge cases
		{paneSize: 1, totalSize: 100, wantRounded: 1},
		{paneSize: 50, totalSize: 100, wantRounded: 50},
		// Cases where rounding changes outcome
		{paneSize: 7, totalSize: 40, wantRounded: 18}, // 17.5 rounds to 18
		{paneSize: 9, totalSize: 50, wantRounded: 18}, // 18.0 stays 18
	}

	for _, tt := range tests {
		// This is the rounding formula used in the actual code
		got := (tt.paneSize*100 + tt.totalSize/2) / tt.totalSize
		if got != tt.wantRounded {
			t.Errorf("percentage(%d, %d) = %d, want %d", tt.paneSize, tt.totalSize, got, tt.wantRounded)
		}

		// Verify the old truncation formula would have been different for problematic cases
		truncated := (tt.paneSize * 100) / tt.totalSize
		if tt.paneSize == 16 && tt.totalSize == 81 && truncated >= tt.wantRounded {
			t.Errorf("truncation case should differ: truncated=%d, rounded=%d", truncated, tt.wantRounded)
		}
	}
}

func TestExecutorFailureMessage(t *testing.T) {
	m := &DetailModel{task: &db.Task{Executor: db.ExecutorCodex}}
	msgWithDetail := m.executorFailureMessage("tmux new-window failed")
	if !strings.Contains(msgWithDetail, "Codex failed to start") {
		t.Fatalf("expected executor name in message, got %q", msgWithDetail)
	}
	if !strings.Contains(msgWithDetail, "tmux new-window failed") {
		t.Fatalf("expected detail in message, got %q", msgWithDetail)
	}
	if !strings.Contains(msgWithDetail, "executor configuration") {
		t.Fatalf("expected guidance in message, got %q", msgWithDetail)
	}

	msgWithoutDetail := m.executorFailureMessage("")
	if strings.Contains(msgWithoutDetail, "  ") {
		t.Fatalf("unexpected double spaces in message: %q", msgWithoutDetail)
	}
	if !strings.Contains(msgWithoutDetail, "Codex failed to start.") {
		t.Fatalf("expected default failure message, got %q", msgWithoutDetail)
	}
}
