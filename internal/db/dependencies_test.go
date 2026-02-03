package db

import (
	"os"
	"testing"
)

func setupDepsTestDB(t *testing.T) (*DB, func()) {
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := Open(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
	}

	return db, cleanup
}

func TestAddDependency(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create two tasks
	task1 := &Task{Title: "Task 1", Status: StatusBacklog}
	task2 := &Task{Title: "Task 2", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}

	// Add dependency: task2 is blocked by task1
	err := db.AddDependency(task1.ID, task2.ID, false)
	if err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Verify dependency exists
	dep, err := db.GetDependency(task1.ID, task2.ID)
	if err != nil {
		t.Fatalf("Failed to get dependency: %v", err)
	}
	if dep == nil {
		t.Fatal("Expected dependency to exist")
	}
	if dep.BlockerID != task1.ID || dep.BlockedID != task2.ID {
		t.Errorf("Dependency has wrong IDs: blocker=%d, blocked=%d", dep.BlockerID, dep.BlockedID)
	}
}

func TestAddDependencySelfBlocking(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	task := &Task{Title: "Task 1", Status: StatusBacklog}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Try to make a task block itself
	err := db.AddDependency(task.ID, task.ID, false)
	if err == nil {
		t.Error("Expected error when adding self-blocking dependency")
	}
}

func TestAddDependencyCycleDetection(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks
	task1 := &Task{Title: "Task 1", Status: StatusBacklog}
	task2 := &Task{Title: "Task 2", Status: StatusBacklog}
	task3 := &Task{Title: "Task 3", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// Create chain: task1 -> task2 -> task3
	if err := db.AddDependency(task1.ID, task2.ID, false); err != nil {
		t.Fatalf("Failed to add dependency 1->2: %v", err)
	}
	if err := db.AddDependency(task2.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency 2->3: %v", err)
	}

	// Try to create cycle: task3 -> task1 should fail
	err := db.AddDependency(task3.ID, task1.ID, false)
	if err == nil {
		t.Error("Expected error when creating a cycle")
	}
}

func TestRemoveDependency(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create two tasks
	task1 := &Task{Title: "Task 1", Status: StatusBacklog}
	task2 := &Task{Title: "Task 2", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}

	// Add and then remove dependency
	if err := db.AddDependency(task1.ID, task2.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}
	if err := db.RemoveDependency(task1.ID, task2.ID); err != nil {
		t.Fatalf("Failed to remove dependency: %v", err)
	}

	// Verify dependency is gone
	dep, err := db.GetDependency(task1.ID, task2.ID)
	if err != nil {
		t.Fatalf("Failed to get dependency: %v", err)
	}
	if dep != nil {
		t.Error("Expected dependency to be removed")
	}
}

func TestGetBlockers(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks
	task1 := &Task{Title: "Blocker 1", Status: StatusBacklog}
	task2 := &Task{Title: "Blocker 2", Status: StatusBacklog}
	task3 := &Task{Title: "Blocked", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// task3 is blocked by task1 and task2
	if err := db.AddDependency(task1.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}
	if err := db.AddDependency(task2.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Get blockers for task3
	blockers, err := db.GetBlockers(task3.ID)
	if err != nil {
		t.Fatalf("Failed to get blockers: %v", err)
	}
	if len(blockers) != 2 {
		t.Errorf("Expected 2 blockers, got %d", len(blockers))
	}
}

func TestGetBlockedBy(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks
	task1 := &Task{Title: "Blocker", Status: StatusBacklog}
	task2 := &Task{Title: "Blocked 1", Status: StatusBacklog}
	task3 := &Task{Title: "Blocked 2", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// task1 blocks task2 and task3
	if err := db.AddDependency(task1.ID, task2.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}
	if err := db.AddDependency(task1.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Get tasks blocked by task1
	blocked, err := db.GetBlockedBy(task1.ID)
	if err != nil {
		t.Fatalf("Failed to get blocked: %v", err)
	}
	if len(blocked) != 2 {
		t.Errorf("Expected 2 blocked tasks, got %d", len(blocked))
	}
}

func TestGetOpenBlockerCount(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks
	task1 := &Task{Title: "Blocker 1", Status: StatusBacklog}
	task2 := &Task{Title: "Blocker 2", Status: StatusDone} // Already done
	task3 := &Task{Title: "Blocked", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// task3 is blocked by task1 (open) and task2 (done)
	if err := db.AddDependency(task1.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}
	if err := db.AddDependency(task2.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Should only count open blockers
	count, err := db.GetOpenBlockerCount(task3.ID)
	if err != nil {
		t.Fatalf("Failed to get open blocker count: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 open blocker, got %d", count)
	}
}

func TestProcessCompletedBlocker(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create two tasks
	task1 := &Task{Title: "Blocker", Status: StatusProcessing}
	task2 := &Task{Title: "Blocked", Status: StatusBlocked}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}

	// task2 is blocked by task1 with auto-queue enabled
	if err := db.AddDependency(task1.ID, task2.ID, true); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Complete task1
	if err := db.UpdateTaskStatus(task1.ID, StatusDone); err != nil {
		t.Fatalf("Failed to update task1 status: %v", err)
	}

	// Process completion (this is normally called by UpdateTaskStatus)
	unblocked, err := db.ProcessCompletedBlocker(task1.ID)
	if err != nil {
		t.Fatalf("Failed to process completed blocker: %v", err)
	}

	if len(unblocked) != 1 {
		t.Errorf("Expected 1 unblocked task, got %d", len(unblocked))
	}

	// Check that task2 was queued (auto-queue was enabled)
	task2Updated, err := db.GetTask(task2.ID)
	if err != nil {
		t.Fatalf("Failed to get task2: %v", err)
	}
	if task2Updated.Status != StatusQueued {
		t.Errorf("Expected task2 status to be queued, got %s", task2Updated.Status)
	}
}

func TestProcessCompletedBlockerWithMultipleBlockers(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks: two blockers and one blocked
	task1 := &Task{Title: "Blocker 1", Status: StatusProcessing}
	task2 := &Task{Title: "Blocker 2", Status: StatusProcessing}
	task3 := &Task{Title: "Blocked", Status: StatusBlocked}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// task3 is blocked by both task1 and task2
	if err := db.AddDependency(task1.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency 1->3: %v", err)
	}
	if err := db.AddDependency(task2.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency 2->3: %v", err)
	}

	// Complete only task1
	if err := db.UpdateTaskStatus(task1.ID, StatusDone); err != nil {
		t.Fatalf("Failed to update task1 status: %v", err)
	}

	// task3 should still be blocked (task2 is still open)
	task3Updated, err := db.GetTask(task3.ID)
	if err != nil {
		t.Fatalf("Failed to get task3: %v", err)
	}
	if task3Updated.Status != StatusBlocked {
		t.Errorf("Expected task3 to still be blocked, got %s", task3Updated.Status)
	}

	// Now complete task2
	if err := db.UpdateTaskStatus(task2.ID, StatusDone); err != nil {
		t.Fatalf("Failed to update task2 status: %v", err)
	}

	// task3 should now be unblocked (moved to backlog since auto-queue was false)
	task3Updated, err = db.GetTask(task3.ID)
	if err != nil {
		t.Fatalf("Failed to get task3: %v", err)
	}
	if task3Updated.Status != StatusBacklog {
		t.Errorf("Expected task3 status to be backlog, got %s", task3Updated.Status)
	}
}

func TestIsBlocked(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create two tasks
	task1 := &Task{Title: "Blocker", Status: StatusBacklog}
	task2 := &Task{Title: "Blocked", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}

	// Initially task2 is not blocked
	blocked, err := db.IsBlocked(task2.ID)
	if err != nil {
		t.Fatalf("Failed to check if blocked: %v", err)
	}
	if blocked {
		t.Error("Task should not be blocked initially")
	}

	// Add dependency
	if err := db.AddDependency(task1.ID, task2.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Now task2 should be blocked
	blocked, err = db.IsBlocked(task2.ID)
	if err != nil {
		t.Fatalf("Failed to check if blocked: %v", err)
	}
	if !blocked {
		t.Error("Task should be blocked after adding dependency")
	}
}
