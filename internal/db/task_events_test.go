package db

import (
	"path/filepath"
	"testing"
)

// setupTestDB creates a temporary test database.
func setupTestDB(t *testing.T) *DB {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	// Personal project is created by migrations, so just verify it exists
	project, err := database.GetProjectByName("personal")
	if err != nil {
		t.Fatalf("Failed to get personal project: %v", err)
	}
	if project == nil {
		t.Fatal("Personal project not created by migrations")
	}

	return database
}

// MockEventEmitter implements EventEmitter for testing.
type MockEventEmitter struct {
	CreatedTasks  []*Task
	UpdatedTasks  []*Task
	DeletedTasks  []int64
	PinnedTasks   []*Task
	UnpinnedTasks []*Task
	Changes       []map[string]interface{}
}

func (m *MockEventEmitter) EmitTaskCreated(task *Task) {
	m.CreatedTasks = append(m.CreatedTasks, task)
}

func (m *MockEventEmitter) EmitTaskUpdated(task *Task, changes map[string]interface{}) {
	m.UpdatedTasks = append(m.UpdatedTasks, task)
	m.Changes = append(m.Changes, changes)
}

func (m *MockEventEmitter) EmitTaskDeleted(taskID int64, title string) {
	m.DeletedTasks = append(m.DeletedTasks, taskID)
}

func (m *MockEventEmitter) EmitTaskPinned(task *Task) {
	m.PinnedTasks = append(m.PinnedTasks, task)
}

func (m *MockEventEmitter) EmitTaskUnpinned(task *Task) {
	m.UnpinnedTasks = append(m.UnpinnedTasks, task)
}

func TestUpdateTaskStatusEmitsEvents(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	// Create mock event emitter
	mockEmitter := &MockEventEmitter{}
	database.SetEventEmitter(mockEmitter)

	// Create a task
	task := &Task{
		Title:   "Test Task",
		Body:    "Test body",
		Status:  StatusBacklog,
		Project: "personal",
	}

	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Clear created events from task creation
	mockEmitter.UpdatedTasks = nil
	mockEmitter.Changes = nil

	// Update status
	if err := database.UpdateTaskStatus(task.ID, StatusQueued); err != nil {
		t.Fatalf("Failed to update task status: %v", err)
	}

	// Verify event was emitted
	if len(mockEmitter.UpdatedTasks) != 1 {
		t.Fatalf("Expected 1 updated task event, got %d", len(mockEmitter.UpdatedTasks))
	}

	// Verify changes contain status change
	if len(mockEmitter.Changes) != 1 {
		t.Fatalf("Expected 1 change record, got %d", len(mockEmitter.Changes))
	}

	changes := mockEmitter.Changes[0]
	statusChange, ok := changes["status"].(map[string]string)
	if !ok {
		t.Fatal("Expected status change in metadata")
	}

	if statusChange["old"] != StatusBacklog {
		t.Errorf("Expected old status %s, got %s", StatusBacklog, statusChange["old"])
	}

	if statusChange["new"] != StatusQueued {
		t.Errorf("Expected new status %s, got %s", StatusQueued, statusChange["new"])
	}
}

func TestUpdateTaskStatusNoEventOnSameStatus(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	mockEmitter := &MockEventEmitter{}
	database.SetEventEmitter(mockEmitter)

	// Create a task with queued status
	task := &Task{
		Title:   "Test Task",
		Body:    "Test body",
		Status:  StatusQueued,
		Project: "personal",
	}

	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Clear created events
	mockEmitter.UpdatedTasks = nil
	mockEmitter.Changes = nil

	// Update to same status
	if err := database.UpdateTaskStatus(task.ID, StatusQueued); err != nil {
		t.Fatalf("Failed to update task status: %v", err)
	}

	// Verify no event was emitted (status didn't actually change)
	if len(mockEmitter.UpdatedTasks) != 0 {
		t.Errorf("Expected 0 updated task events for unchanged status, got %d", len(mockEmitter.UpdatedTasks))
	}
}

func TestUpdateTaskStatusMultipleTransitions(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	mockEmitter := &MockEventEmitter{}
	database.SetEventEmitter(mockEmitter)

	// Create a task
	task := &Task{
		Title:   "Test Task",
		Body:    "Test body",
		Status:  StatusBacklog,
		Project: "personal",
	}

	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Clear created events
	mockEmitter.UpdatedTasks = nil
	mockEmitter.Changes = nil

	// Simulate task lifecycle
	transitions := []struct {
		from string
		to   string
	}{
		{StatusBacklog, StatusQueued},
		{StatusQueued, StatusProcessing},
		{StatusProcessing, StatusBlocked},
		{StatusBlocked, StatusProcessing},
		{StatusProcessing, StatusDone},
	}

	for i, transition := range transitions {
		if err := database.UpdateTaskStatus(task.ID, transition.to); err != nil {
			t.Fatalf("Failed to update task status on transition %d: %v", i, err)
		}

		// Verify event was emitted for each transition
		expectedEvents := i + 1
		if len(mockEmitter.UpdatedTasks) != expectedEvents {
			t.Errorf("After transition %d: expected %d events, got %d",
				i, expectedEvents, len(mockEmitter.UpdatedTasks))
		}

		// Verify the change record
		if len(mockEmitter.Changes) != expectedEvents {
			t.Fatalf("After transition %d: expected %d change records, got %d",
				i, expectedEvents, len(mockEmitter.Changes))
		}

		changes := mockEmitter.Changes[i]
		statusChange, ok := changes["status"].(map[string]string)
		if !ok {
			t.Fatalf("Expected status change in metadata for transition %d", i)
		}

		if statusChange["old"] != transition.from {
			t.Errorf("Transition %d: expected old status %s, got %s",
				i, transition.from, statusChange["old"])
		}

		if statusChange["new"] != transition.to {
			t.Errorf("Transition %d: expected new status %s, got %s",
				i, transition.to, statusChange["new"])
		}
	}
}

func TestUpdateTaskStatusTimestamps(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	// Create a task
	task := &Task{
		Title:   "Test Task",
		Body:    "Test body",
		Status:  StatusBacklog,
		Project: "personal",
	}

	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Update to processing (should set started_at)
	if err := database.UpdateTaskStatus(task.ID, StatusProcessing); err != nil {
		t.Fatalf("Failed to update to processing: %v", err)
	}

	updated, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}

	if updated.StartedAt == nil {
		t.Error("Expected started_at to be set when status changed to processing")
	}

	// Update to done (should set completed_at)
	if err := database.UpdateTaskStatus(task.ID, StatusDone); err != nil {
		t.Fatalf("Failed to update to done: %v", err)
	}

	updated, err = database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}

	if updated.CompletedAt == nil {
		t.Error("Expected completed_at to be set when status changed to done")
	}
}

func TestCreateTaskEmitsEvent(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	mockEmitter := &MockEventEmitter{}
	database.SetEventEmitter(mockEmitter)

	task := &Task{
		Title:   "New Task",
		Body:    "Task body",
		Status:  StatusBacklog,
		Project: "personal",
	}

	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Verify created event was emitted
	if len(mockEmitter.CreatedTasks) != 1 {
		t.Fatalf("Expected 1 created task event, got %d", len(mockEmitter.CreatedTasks))
	}

	if mockEmitter.CreatedTasks[0].Title != "New Task" {
		t.Errorf("Expected task title 'New Task', got '%s'", mockEmitter.CreatedTasks[0].Title)
	}
}

func TestDeleteTaskEmitsEvent(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	mockEmitter := &MockEventEmitter{}
	database.SetEventEmitter(mockEmitter)

	task := &Task{
		Title:   "Task to Delete",
		Body:    "Will be deleted",
		Status:  StatusBacklog,
		Project: "personal",
	}

	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	taskID := task.ID

	// Clear created events
	mockEmitter.DeletedTasks = nil

	// Delete task
	if err := database.DeleteTask(taskID); err != nil {
		t.Fatalf("Failed to delete task: %v", err)
	}

	// Verify deleted event was emitted
	if len(mockEmitter.DeletedTasks) != 1 {
		t.Fatalf("Expected 1 deleted task event, got %d", len(mockEmitter.DeletedTasks))
	}

	if mockEmitter.DeletedTasks[0] != taskID {
		t.Errorf("Expected deleted task ID %d, got %d", taskID, mockEmitter.DeletedTasks[0])
	}
}

func TestPinTaskEmitsEvent(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	mockEmitter := &MockEventEmitter{}
	database.SetEventEmitter(mockEmitter)

	task := &Task{
		Title:   "Task to Pin",
		Body:    "Will be pinned",
		Status:  StatusBacklog,
		Project: "personal",
		Pinned:  false,
	}

	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Pin task
	if err := database.UpdateTaskPinned(task.ID, true); err != nil {
		t.Fatalf("Failed to pin task: %v", err)
	}

	// Verify pinned event was emitted
	if len(mockEmitter.PinnedTasks) != 1 {
		t.Fatalf("Expected 1 pinned task event, got %d", len(mockEmitter.PinnedTasks))
	}

	// Unpin task
	mockEmitter.UnpinnedTasks = nil
	if err := database.UpdateTaskPinned(task.ID, false); err != nil {
		t.Fatalf("Failed to unpin task: %v", err)
	}

	// Verify unpinned event was emitted
	if len(mockEmitter.UnpinnedTasks) != 1 {
		t.Fatalf("Expected 1 unpinned task event, got %d", len(mockEmitter.UnpinnedTasks))
	}
}

func TestUpdateTaskEmitsEventWithChanges(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	mockEmitter := &MockEventEmitter{}
	database.SetEventEmitter(mockEmitter)

	task := &Task{
		Title:   "Original Title",
		Body:    "Original body",
		Status:  StatusBacklog,
		Type:    "code",
		Project: "personal",
	}

	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Clear created events
	mockEmitter.UpdatedTasks = nil
	mockEmitter.Changes = nil

	// Update multiple fields
	task.Title = "Updated Title"
	task.Body = "Updated body"
	task.Type = "writing"

	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("Failed to update task: %v", err)
	}

	// Verify event was emitted
	if len(mockEmitter.UpdatedTasks) != 1 {
		t.Fatalf("Expected 1 updated task event, got %d", len(mockEmitter.UpdatedTasks))
	}

	// Verify changes were tracked
	if len(mockEmitter.Changes) != 1 {
		t.Fatalf("Expected 1 change record, got %d", len(mockEmitter.Changes))
	}

	changes := mockEmitter.Changes[0]

	// Check title change
	if titleChange, ok := changes["title"].(map[string]string); !ok {
		t.Error("Expected title change in metadata")
	} else {
		if titleChange["old"] != "Original Title" {
			t.Errorf("Expected old title 'Original Title', got '%s'", titleChange["old"])
		}
		if titleChange["new"] != "Updated Title" {
			t.Errorf("Expected new title 'Updated Title', got '%s'", titleChange["new"])
		}
	}

	// Check body change
	if bodyChange, ok := changes["body"].(map[string]string); !ok {
		t.Error("Expected body change in metadata")
	} else {
		if bodyChange["old"] != "Original body" {
			t.Errorf("Expected old body 'Original body', got '%s'", bodyChange["old"])
		}
	}

	// Check type change
	if typeChange, ok := changes["type"].(map[string]string); !ok {
		t.Error("Expected type change in metadata")
	} else {
		if typeChange["old"] != "code" {
			t.Errorf("Expected old type 'code', got '%s'", typeChange["old"])
		}
		if typeChange["new"] != "writing" {
			t.Errorf("Expected new type 'writing', got '%s'", typeChange["new"])
		}
	}
}
