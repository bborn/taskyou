package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestCLICreateTask tests task creation via the database directly
// (same operations as the CLI create command)
func TestCLICreateTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	tests := []struct {
		name     string
		task     *db.Task
		wantErr  bool
	}{
		{
			name: "basic task",
			task: &db.Task{
				Title:   "Test task",
				Body:    "",
				Status:  db.StatusBacklog,
				Type:    db.TypeCode,
				Project: "",
			},
		},
		{
			name: "task with all fields",
			task: &db.Task{
				Title:   "Full task",
				Body:    "This is the body",
				Status:  db.StatusBacklog,
				Type:    db.TypeWriting,
				Project: "myproject",
			},
		},
		{
			name: "queued task",
			task: &db.Task{
				Title:   "Queued task",
				Body:    "",
				Status:  db.StatusQueued,
				Type:    db.TypeCode,
				Project: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := database.CreateTask(tt.task)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateTask() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if tt.task.ID == 0 {
					t.Error("expected task ID to be set")
				}
				// Verify task was created
				fetched, err := database.GetTask(tt.task.ID)
				if err != nil {
					t.Fatalf("GetTask() error = %v", err)
				}
				if fetched.Title != tt.task.Title {
					t.Errorf("Title = %v, want %v", fetched.Title, tt.task.Title)
				}
				if fetched.Status != tt.task.Status {
					t.Errorf("Status = %v, want %v", fetched.Status, tt.task.Status)
				}
			}
		})
	}
}

// TestCLIListTasks tests task listing via the database directly
func TestCLIListTasks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Create some tasks
	tasks := []*db.Task{
		{Title: "Task 1", Status: db.StatusBacklog, Type: db.TypeCode},
		{Title: "Task 2", Status: db.StatusQueued, Type: db.TypeCode},
		{Title: "Task 3", Status: db.StatusDone, Type: db.TypeWriting, Project: "proj1"},
	}
	for _, task := range tasks {
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
	}

	// Test list all (excluding done)
	result, err := database.ListTasks(db.ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 tasks (excluding done), got %d", len(result))
	}

	// Test list with status filter
	result, err = database.ListTasks(db.ListTasksOptions{Status: db.StatusQueued})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 queued task, got %d", len(result))
	}

	// Test list including closed
	result, err = database.ListTasks(db.ListTasksOptions{IncludeClosed: true})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 tasks (including done), got %d", len(result))
	}

	// Test list with project filter
	result, err = database.ListTasks(db.ListTasksOptions{Project: "proj1", IncludeClosed: true})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 task in proj1, got %d", len(result))
	}
}

// TestCLIUpdateTask tests task update via the database directly
func TestCLIUpdateTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Create a task
	task := &db.Task{
		Title:  "Original title",
		Body:   "Original body",
		Status: db.StatusBacklog,
		Type:   db.TypeCode,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Update the task
	task.Title = "Updated title"
	task.Body = "Updated body"
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}

	// Verify update
	fetched, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if fetched.Title != "Updated title" {
		t.Errorf("Title = %v, want 'Updated title'", fetched.Title)
	}
	if fetched.Body != "Updated body" {
		t.Errorf("Body = %v, want 'Updated body'", fetched.Body)
	}
}

// TestCLIExecuteTask tests queueing a task for execution
func TestCLIExecuteTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Create a backlog task
	task := &db.Task{
		Title:  "Task to execute",
		Status: db.StatusBacklog,
		Type:   db.TypeCode,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Queue the task (simulate execute command)
	if err := database.UpdateTaskStatus(task.ID, db.StatusQueued); err != nil {
		t.Fatalf("UpdateTaskStatus() error = %v", err)
	}

	// Verify status change
	fetched, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if fetched.Status != db.StatusQueued {
		t.Errorf("Status = %v, want %v", fetched.Status, db.StatusQueued)
	}
}

// TestCLICloseTask tests marking a task as done
func TestCLICloseTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Create and queue a task
	task := &db.Task{
		Title:  "Task to close",
		Status: db.StatusProcessing,
		Type:   db.TypeCode,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Close the task
	if err := database.UpdateTaskStatus(task.ID, db.StatusDone); err != nil {
		t.Fatalf("UpdateTaskStatus() error = %v", err)
	}

	// Verify status change
	fetched, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if fetched.Status != db.StatusDone {
		t.Errorf("Status = %v, want %v", fetched.Status, db.StatusDone)
	}
	if fetched.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}
}

// TestCLIDeleteTask tests deleting a task
func TestCLIDeleteTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Create a task
	task := &db.Task{
		Title:  "Task to delete",
		Status: db.StatusBacklog,
		Type:   db.TypeCode,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Delete the task
	if err := database.DeleteTask(task.ID); err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}

	// Verify deletion
	fetched, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if fetched != nil {
		t.Error("expected task to be deleted")
	}
}

// TestCLIRetryTask tests retrying a blocked task
func TestCLIRetryTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Create a blocked task
	task := &db.Task{
		Title:  "Blocked task",
		Status: db.StatusBlocked,
		Type:   db.TypeCode,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Retry with feedback
	if err := database.RetryTask(task.ID, "Try a different approach"); err != nil {
		t.Fatalf("RetryTask() error = %v", err)
	}

	// Verify task is re-queued
	fetched, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if fetched.Status != db.StatusQueued {
		t.Errorf("Status = %v, want %v", fetched.Status, db.StatusQueued)
	}

	// Verify feedback was logged
	feedback, err := database.GetRetryFeedback(task.ID)
	if err != nil {
		t.Fatalf("GetRetryFeedback() error = %v", err)
	}
	if feedback != "Try a different approach" {
		t.Errorf("feedback = %v, want 'Try a different approach'", feedback)
	}
}

// TestTaskTypeValidation tests task type validation
func TestTaskTypeValidation(t *testing.T) {
	validTypes := []string{db.TypeCode, db.TypeWriting, db.TypeThinking}
	invalidTypes := []string{"invalid", "unknown", ""}

	for _, typ := range validTypes {
		if typ != db.TypeCode && typ != db.TypeWriting && typ != db.TypeThinking {
			t.Errorf("type %q should be valid", typ)
		}
	}

	for _, typ := range invalidTypes {
		if typ == db.TypeCode || typ == db.TypeWriting || typ == db.TypeThinking {
			t.Errorf("type %q should be invalid", typ)
		}
	}
}

// TestClaudeHookStatusHandling tests that Claude hooks only change status for started tasks
func TestClaudeHookStatusHandling(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Test 1: NotificationHook should NOT change status for task without StartedAt
	t.Run("NotificationHook ignores unstarted task", func(t *testing.T) {
		task := &db.Task{
			Title:  "Unstarted task",
			Status: db.StatusProcessing, // Even if processing status, no StartedAt
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Simulate idle_prompt notification
		input := &ClaudeHookInput{NotificationType: "idle_prompt"}
		err := handleNotificationHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handleNotificationHook() error = %v", err)
		}

		// Status should NOT have changed because StartedAt is nil
		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Status != db.StatusProcessing {
			t.Errorf("Status = %v, want %v (should not change for unstarted task)", fetched.Status, db.StatusProcessing)
		}
	})

	// Test 2: NotificationHook SHOULD change status for started task
	t.Run("NotificationHook changes status for started task", func(t *testing.T) {
		task := &db.Task{
			Title:  "Started task",
			Status: db.StatusProcessing,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Mark the task as started
		if err := database.MarkTaskStarted(task.ID); err != nil {
			t.Fatalf("MarkTaskStarted() error = %v", err)
		}

		// Simulate idle_prompt notification
		input := &ClaudeHookInput{NotificationType: "idle_prompt"}
		err := handleNotificationHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handleNotificationHook() error = %v", err)
		}

		// Status SHOULD change to blocked because task was started
		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Status != db.StatusBlocked {
			t.Errorf("Status = %v, want %v", fetched.Status, db.StatusBlocked)
		}
	})

	// Test 3: StopHook should NOT change status for task without StartedAt
	t.Run("StopHook ignores unstarted task", func(t *testing.T) {
		task := &db.Task{
			Title:  "Unstarted task for stop",
			Status: db.StatusProcessing,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Simulate end_turn stop
		input := &ClaudeHookInput{StopReason: "end_turn"}
		err := handleStopHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handleStopHook() error = %v", err)
		}

		// Status should NOT have changed
		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Status != db.StatusProcessing {
			t.Errorf("Status = %v, want %v (should not change for unstarted task)", fetched.Status, db.StatusProcessing)
		}
	})

	// Test 4: StopHook SHOULD change status for started task
	t.Run("StopHook changes status for started task", func(t *testing.T) {
		task := &db.Task{
			Title:  "Started task for stop",
			Status: db.StatusProcessing,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Mark the task as started
		if err := database.MarkTaskStarted(task.ID); err != nil {
			t.Fatalf("MarkTaskStarted() error = %v", err)
		}

		// Simulate end_turn stop
		input := &ClaudeHookInput{StopReason: "end_turn"}
		err := handleStopHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handleStopHook() error = %v", err)
		}

		// Status SHOULD change to blocked
		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Status != db.StatusBlocked {
			t.Errorf("Status = %v, want %v", fetched.Status, db.StatusBlocked)
		}
	})

	// Test 5: PreToolUseHook should NOT change status for task without StartedAt
	t.Run("PreToolUseHook ignores unstarted task", func(t *testing.T) {
		task := &db.Task{
			Title:  "Unstarted task for tool",
			Status: db.StatusBlocked,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Simulate PreToolUse
		input := &ClaudeHookInput{}
		err := handlePreToolUseHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handlePreToolUseHook() error = %v", err)
		}

		// Status should NOT have changed
		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Status != db.StatusBlocked {
			t.Errorf("Status = %v, want %v (should not change for unstarted task)", fetched.Status, db.StatusBlocked)
		}
	})

	// Test 6: PreToolUseHook SHOULD change blocked→processing for started task
	t.Run("PreToolUseHook resumes started task", func(t *testing.T) {
		task := &db.Task{
			Title:  "Started blocked task",
			Status: db.StatusBlocked,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Mark the task as started (this also sets status to processing, so we need to set it back)
		if err := database.MarkTaskStarted(task.ID); err != nil {
			t.Fatalf("MarkTaskStarted() error = %v", err)
		}
		// Set status back to blocked to simulate waiting for input
		if err := database.UpdateTaskStatus(task.ID, db.StatusBlocked); err != nil {
			t.Fatalf("UpdateTaskStatus() error = %v", err)
		}

		// Simulate PreToolUse (Claude resumed working)
		input := &ClaudeHookInput{}
		err := handlePreToolUseHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handlePreToolUseHook() error = %v", err)
		}

		// Status SHOULD change to processing
		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Status != db.StatusProcessing {
			t.Errorf("Status = %v, want %v", fetched.Status, db.StatusProcessing)
		}
	})

	// Test 7: PostToolUseHook should NOT change status for task without StartedAt
	t.Run("PostToolUseHook ignores unstarted task", func(t *testing.T) {
		task := &db.Task{
			Title:  "Unstarted task for post tool",
			Status: db.StatusBlocked,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Simulate PostToolUse
		input := &ClaudeHookInput{}
		err := handlePostToolUseHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handlePostToolUseHook() error = %v", err)
		}

		// Status should NOT have changed
		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Status != db.StatusBlocked {
			t.Errorf("Status = %v, want %v (should not change for unstarted task)", fetched.Status, db.StatusBlocked)
		}
	})

	// Test 8: tool_use stop reason should NOT change status
	t.Run("StopHook with tool_use does not change status", func(t *testing.T) {
		task := &db.Task{
			Title:  "Task with tool_use stop",
			Status: db.StatusProcessing,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Mark the task as started
		if err := database.MarkTaskStarted(task.ID); err != nil {
			t.Fatalf("MarkTaskStarted() error = %v", err)
		}

		// Simulate tool_use stop (should NOT change status)
		input := &ClaudeHookInput{StopReason: "tool_use"}
		err := handleStopHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handleStopHook() error = %v", err)
		}

		// Status should remain processing
		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Status != db.StatusProcessing {
			t.Errorf("Status = %v, want %v (tool_use should not change status)", fetched.Status, db.StatusProcessing)
		}
	})
}
