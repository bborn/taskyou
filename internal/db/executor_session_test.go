package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutorSessionCRUD(t *testing.T) {
	// Create temp database
	tmpDir, err := os.MkdirTemp("", "test-executor-session-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create a task first
	task := &Task{
		Title:    "Test task",
		Status:   StatusBacklog,
		Project:  "personal",
		Executor: ExecutorClaude,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Test CreateExecutorSession
	session := &ExecutorSession{
		TaskID:   task.ID,
		Executor: ExecutorClaude,
		Status:   SessionStatusPending,
	}
	if err := database.CreateExecutorSession(session); err != nil {
		t.Fatalf("CreateExecutorSession failed: %v", err)
	}
	if session.ID == 0 {
		t.Error("Expected session ID to be set")
	}

	// Test GetExecutorSession
	retrieved, err := database.GetExecutorSession(session.ID)
	if err != nil {
		t.Fatalf("GetExecutorSession failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected session to be retrieved")
	}
	if retrieved.TaskID != task.ID {
		t.Errorf("Expected TaskID %d, got %d", task.ID, retrieved.TaskID)
	}
	if retrieved.Executor != ExecutorClaude {
		t.Errorf("Expected Executor %s, got %s", ExecutorClaude, retrieved.Executor)
	}

	// Test UpdateExecutorSession
	session.Status = SessionStatusActive
	session.DaemonSession = "task-daemon-123"
	if err := database.UpdateExecutorSession(session); err != nil {
		t.Fatalf("UpdateExecutorSession failed: %v", err)
	}

	updated, _ := database.GetExecutorSession(session.ID)
	if updated.Status != SessionStatusActive {
		t.Errorf("Expected Status %s, got %s", SessionStatusActive, updated.Status)
	}
	if updated.DaemonSession != "task-daemon-123" {
		t.Errorf("Expected DaemonSession %s, got %s", "task-daemon-123", updated.DaemonSession)
	}

	// Test DeleteExecutorSession
	if err := database.DeleteExecutorSession(session.ID); err != nil {
		t.Fatalf("DeleteExecutorSession failed: %v", err)
	}
	deleted, _ := database.GetExecutorSession(session.ID)
	if deleted != nil {
		t.Error("Expected session to be deleted")
	}
}

func TestMultipleExecutorSessions(t *testing.T) {
	// Create temp database
	tmpDir, err := os.MkdirTemp("", "test-multi-session-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create a task
	task := &Task{
		Title:    "Test task",
		Status:   StatusBacklog,
		Project:  "personal",
		Executor: ExecutorClaude,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Create multiple executor sessions
	sessions := []*ExecutorSession{
		{TaskID: task.ID, Executor: ExecutorClaude, Status: SessionStatusCompleted},
		{TaskID: task.ID, Executor: ExecutorCodex, Status: SessionStatusActive},
		{TaskID: task.ID, Executor: ExecutorGemini, Status: SessionStatusPending},
	}

	for _, s := range sessions {
		if err := database.CreateExecutorSession(s); err != nil {
			t.Fatalf("CreateExecutorSession failed: %v", err)
		}
	}

	// Test GetExecutorSessionsForTask
	retrieved, err := database.GetExecutorSessionsForTask(task.ID)
	if err != nil {
		t.Fatalf("GetExecutorSessionsForTask failed: %v", err)
	}
	if len(retrieved) != 3 {
		t.Errorf("Expected 3 sessions, got %d", len(retrieved))
	}

	// Test GetActiveExecutorSession
	active, err := database.GetActiveExecutorSession(task.ID)
	if err != nil {
		t.Fatalf("GetActiveExecutorSession failed: %v", err)
	}
	if active == nil {
		t.Fatal("Expected active session")
	}
	if active.Executor != ExecutorCodex {
		t.Errorf("Expected active executor %s, got %s", ExecutorCodex, active.Executor)
	}

	// Test SetActiveExecutorSession
	if err := database.SetActiveExecutorSession(task.ID, sessions[2].ID); err != nil {
		t.Fatalf("SetActiveExecutorSession failed: %v", err)
	}
	updatedTask, _ := database.GetTask(task.ID)
	if updatedTask.ActiveSessionID != sessions[2].ID {
		t.Errorf("Expected ActiveSessionID %d, got %d", sessions[2].ID, updatedTask.ActiveSessionID)
	}
}

func TestTaskLogsWithSession(t *testing.T) {
	// Create temp database
	tmpDir, err := os.MkdirTemp("", "test-logs-session-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Create a task
	task := &Task{
		Title:    "Test task",
		Status:   StatusBacklog,
		Project:  "personal",
		Executor: ExecutorClaude,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Create executor session
	session := &ExecutorSession{
		TaskID:   task.ID,
		Executor: ExecutorClaude,
		Status:   SessionStatusActive,
	}
	if err := database.CreateExecutorSession(session); err != nil {
		t.Fatalf("CreateExecutorSession failed: %v", err)
	}

	// Add logs with session ID
	if err := database.AppendTaskLogForSession(task.ID, session.ID, "output", "Log with session"); err != nil {
		t.Fatalf("AppendTaskLogForSession failed: %v", err)
	}

	// Add legacy log without session ID
	if err := database.AppendTaskLog(task.ID, "output", "Legacy log"); err != nil {
		t.Fatalf("AppendTaskLog failed: %v", err)
	}

	// Test GetTaskLogsForSession - should return both session-specific and legacy logs
	logs, err := database.GetTaskLogsForSession(task.ID, session.ID, 100)
	if err != nil {
		t.Fatalf("GetTaskLogsForSession failed: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("Expected 2 logs, got %d", len(logs))
	}

	// Verify session ID is set on the session-specific log
	sessionLogFound := false
	for _, l := range logs {
		if l.Content == "Log with session" && l.SessionID == session.ID {
			sessionLogFound = true
		}
	}
	if !sessionLogFound {
		t.Error("Expected to find log with session ID")
	}
}
