package db

import (
	"testing"
)

// TestClearTaskSessionPlacementPreservesResumeID is the contract the idle-suspend
// sweep depends on: tearing down a task's tmux placement must leave the agent
// session ID intact, or the task can never be resumed with `--resume`.
func TestClearTaskSessionPlacementPreservesResumeID(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := &Task{Title: "parked task", Status: StatusBlocked}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := database.UpdateTaskClaudeSessionID(task.ID, "sess-abc123"); err != nil {
		t.Fatalf("UpdateTaskClaudeSessionID: %v", err)
	}
	if err := database.UpdateTaskWindowID(task.ID, "@42"); err != nil {
		t.Fatalf("UpdateTaskWindowID: %v", err)
	}
	if err := database.UpdateTaskPaneIDs(task.ID, "%1", "%2"); err != nil {
		t.Fatalf("UpdateTaskPaneIDs: %v", err)
	}
	if err := database.UpdateTaskDaemonSession(task.ID, "task-daemon-999"); err != nil {
		t.Fatalf("UpdateTaskDaemonSession: %v", err)
	}

	if err := database.ClearTaskSessionPlacement(task.ID); err != nil {
		t.Fatalf("ClearTaskSessionPlacement: %v", err)
	}

	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if got.ClaudeSessionID != "sess-abc123" {
		t.Errorf("claude_session_id = %q, want it preserved as %q", got.ClaudeSessionID, "sess-abc123")
	}
	if got.TmuxWindowID != "" {
		t.Errorf("tmux_window_id = %q, want cleared", got.TmuxWindowID)
	}
	if got.ClaudePaneID != "" {
		t.Errorf("claude_pane_id = %q, want cleared", got.ClaudePaneID)
	}
	if got.ShellPaneID != "" {
		t.Errorf("shell_pane_id = %q, want cleared", got.ShellPaneID)
	}
	if got.DaemonSession != "" {
		t.Errorf("daemon_session = %q, want cleared", got.DaemonSession)
	}
}
