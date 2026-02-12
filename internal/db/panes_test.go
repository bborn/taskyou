package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTaskPanes(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create the test project first
	if err := db.CreateProject(&Project{Name: "test", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create test project: %v", err)
	}

	// Create a test task
	task := &Task{
		Title:   "Test Task",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "test",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Test creating a Claude pane
	claudePane := &TaskPane{
		TaskID:   task.ID,
		PaneID:   "%1234",
		PaneType: PaneTypeClaude,
		Title:    "Claude",
	}
	if err := db.CreateTaskPane(claudePane); err != nil {
		t.Fatalf("CreateTaskPane failed: %v", err)
	}

	// Test creating a Shell pane
	shellPane := &TaskPane{
		TaskID:   task.ID,
		PaneID:   "%1235",
		PaneType: PaneTypeShell,
		Title:    "Shell",
	}
	if err := db.CreateTaskPane(shellPane); err != nil {
		t.Fatalf("CreateTaskPane failed: %v", err)
	}

	// Test creating an extra pane
	extraPane := &TaskPane{
		TaskID:   task.ID,
		PaneID:   "%1236",
		PaneType: PaneTypeShellExtra,
		Title:    "Extra Shell",
	}
	if err := db.CreateTaskPane(extraPane); err != nil {
		t.Fatalf("CreateTaskPane failed: %v", err)
	}

	// Test GetTaskPanes
	panes, err := db.GetTaskPanes(task.ID)
	if err != nil {
		t.Fatalf("GetTaskPanes failed: %v", err)
	}
	if len(panes) != 3 {
		t.Errorf("Expected 3 panes, got %d", len(panes))
	}

	// Test GetPrimaryPanes
	claude, shell, err := db.GetPrimaryPanes(task.ID)
	if err != nil {
		t.Fatalf("GetPrimaryPanes failed: %v", err)
	}
	if claude == nil {
		t.Error("Expected Claude pane, got nil")
	} else if claude.PaneID != "%1234" {
		t.Errorf("Expected Claude pane ID %%1234, got %s", claude.PaneID)
	}
	if shell == nil {
		t.Error("Expected Shell pane, got nil")
	} else if shell.PaneID != "%1235" {
		t.Errorf("Expected Shell pane ID %%1235, got %s", shell.PaneID)
	}

	// Test UpdateTaskPaneTitle
	if err := db.UpdateTaskPaneTitle(extraPane.ID, "New Shell"); err != nil {
		t.Fatalf("UpdateTaskPaneTitle failed: %v", err)
	}
	updatedPane, err := db.GetTaskPaneByID(extraPane.ID)
	if err != nil {
		t.Fatalf("GetTaskPaneByID failed: %v", err)
	}
	if updatedPane.Title != "New Shell" {
		t.Errorf("Expected title 'New Shell', got '%s'", updatedPane.Title)
	}

	// Test DeleteTaskPaneByPaneID
	if err := db.DeleteTaskPaneByPaneID(task.ID, "%1236"); err != nil {
		t.Fatalf("DeleteTaskPaneByPaneID failed: %v", err)
	}
	panes, err = db.GetTaskPanes(task.ID)
	if err != nil {
		t.Fatalf("GetTaskPanes failed: %v", err)
	}
	if len(panes) != 2 {
		t.Errorf("Expected 2 panes after delete, got %d", len(panes))
	}

	// Test ClearTaskPanes
	if err := db.ClearTaskPanes(task.ID); err != nil {
		t.Fatalf("ClearTaskPanes failed: %v", err)
	}
	panes, err = db.GetTaskPanes(task.ID)
	if err != nil {
		t.Fatalf("GetTaskPanes failed: %v", err)
	}
	if len(panes) != 0 {
		t.Errorf("Expected 0 panes after clear, got %d", len(panes))
	}
}

func TestSyncPaneFromTask(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create the test project first
	if err := db.CreateProject(&Project{Name: "test", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create test project: %v", err)
	}

	// Create a test task
	task := &Task{
		Title:   "Test Task",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "test",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Sync panes from task (simulating migration)
	if err := db.SyncPaneFromTask(task.ID, "%1234", "%1235"); err != nil {
		t.Fatalf("SyncPaneFromTask failed: %v", err)
	}

	// Verify panes were created
	panes, err := db.GetTaskPanes(task.ID)
	if err != nil {
		t.Fatalf("GetTaskPanes failed: %v", err)
	}
	if len(panes) != 2 {
		t.Errorf("Expected 2 panes, got %d", len(panes))
	}

	// Verify pane types
	claude, shell, err := db.GetPrimaryPanes(task.ID)
	if err != nil {
		t.Fatalf("GetPrimaryPanes failed: %v", err)
	}
	if claude == nil || claude.PaneID != "%1234" {
		t.Error("Claude pane not synced correctly")
	}
	if shell == nil || shell.PaneID != "%1235" {
		t.Error("Shell pane not synced correctly")
	}

	// Test re-syncing (should replace existing panes)
	if err := db.SyncPaneFromTask(task.ID, "%5678", "%5679"); err != nil {
		t.Fatalf("SyncPaneFromTask (re-sync) failed: %v", err)
	}

	// Verify panes were updated
	claude, shell, err = db.GetPrimaryPanes(task.ID)
	if err != nil {
		t.Fatalf("GetPrimaryPanes failed: %v", err)
	}
	if claude == nil || claude.PaneID != "%5678" {
		t.Error("Claude pane not re-synced correctly")
	}
	if shell == nil || shell.PaneID != "%5679" {
		t.Error("Shell pane not re-synced correctly")
	}
}
