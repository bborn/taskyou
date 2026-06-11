package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

	// Create the myproject project for testing
	if err := database.CreateProject(&db.Project{Name: "myproject", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create myproject: %v", err)
	}

	tests := []struct {
		name    string
		task    *db.Task
		wantErr bool
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
		{
			name: "task with executor",
			task: &db.Task{
				Title:    "Task with codex",
				Body:     "Use codex executor",
				Status:   db.StatusBacklog,
				Type:     db.TypeCode,
				Executor: db.ExecutorCodex,
				Project:  "",
			},
		},
		{
			name: "task with tags",
			task: &db.Task{
				Title:   "Task with tags",
				Body:    "Has some tags",
				Status:  db.StatusBacklog,
				Type:    db.TypeCode,
				Tags:    "bug,urgent",
				Project: "",
			},
		},
		{
			name: "task pinned",
			task: &db.Task{
				Title:   "Pinned task",
				Body:    "This is pinned",
				Status:  db.StatusBacklog,
				Type:    db.TypeCode,
				Pinned:  true,
				Project: "",
			},
		},
		{
			name: "task with all CLI fields",
			task: &db.Task{
				Title:    "Full CLI task",
				Body:     "Has all fields",
				Status:   db.StatusBacklog,
				Type:     db.TypeWriting,
				Executor: db.ExecutorGemini,
				Tags:     "feature,high-priority",
				Pinned:   true,
				Project:  "myproject",
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
				// Check executor (defaults to claude if empty)
				expectedExecutor := tt.task.Executor
				if expectedExecutor == "" {
					expectedExecutor = db.ExecutorClaude
				}
				if fetched.Executor != expectedExecutor {
					t.Errorf("Executor = %v, want %v", fetched.Executor, expectedExecutor)
				}
				// Check tags
				if fetched.Tags != tt.task.Tags {
					t.Errorf("Tags = %v, want %v", fetched.Tags, tt.task.Tags)
				}
				// Check pinned
				if fetched.Pinned != tt.task.Pinned {
					t.Errorf("Pinned = %v, want %v", fetched.Pinned, tt.task.Pinned)
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

	// Create the proj1 project for testing
	if err := database.CreateProject(&db.Project{Name: "proj1", Path: tmpDir}); err != nil {
		t.Fatalf("failed to create proj1: %v", err)
	}

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

// TestCLIUpdateTaskNewFields tests updating executor, tags, and pinned fields
func TestCLIUpdateTaskNewFields(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	t.Run("update executor", func(t *testing.T) {
		task := &db.Task{
			Title:    "Task to update executor",
			Status:   db.StatusBacklog,
			Type:     db.TypeCode,
			Executor: db.ExecutorClaude,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Update executor
		task.Executor = db.ExecutorCodex
		if err := database.UpdateTask(task); err != nil {
			t.Fatalf("UpdateTask() error = %v", err)
		}

		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Executor != db.ExecutorCodex {
			t.Errorf("Executor = %v, want %v", fetched.Executor, db.ExecutorCodex)
		}
	})

	t.Run("update tags", func(t *testing.T) {
		task := &db.Task{
			Title:  "Task to update tags",
			Status: db.StatusBacklog,
			Type:   db.TypeCode,
			Tags:   "original-tag",
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Update tags
		task.Tags = "new-tag,updated"
		if err := database.UpdateTask(task); err != nil {
			t.Fatalf("UpdateTask() error = %v", err)
		}

		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Tags != "new-tag,updated" {
			t.Errorf("Tags = %v, want 'new-tag,updated'", fetched.Tags)
		}
	})

	t.Run("update pinned", func(t *testing.T) {
		task := &db.Task{
			Title:  "Task to pin",
			Status: db.StatusBacklog,
			Type:   db.TypeCode,
			Pinned: false,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Pin the task
		task.Pinned = true
		if err := database.UpdateTask(task); err != nil {
			t.Fatalf("UpdateTask() error = %v", err)
		}

		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if !fetched.Pinned {
			t.Errorf("Pinned = %v, want true", fetched.Pinned)
		}
	})

	t.Run("clear tags", func(t *testing.T) {
		task := &db.Task{
			Title:  "Task to clear tags",
			Status: db.StatusBacklog,
			Type:   db.TypeCode,
			Tags:   "some,tags",
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Clear tags
		task.Tags = ""
		if err := database.UpdateTask(task); err != nil {
			t.Fatalf("UpdateTask() error = %v", err)
		}

		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Tags != "" {
			t.Errorf("Tags = %v, want empty string", fetched.Tags)
		}
	})

	t.Run("unpin task", func(t *testing.T) {
		task := &db.Task{
			Title:  "Task to unpin",
			Status: db.StatusBacklog,
			Type:   db.TypeCode,
			Pinned: true,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		// Unpin the task
		task.Pinned = false
		if err := database.UpdateTask(task); err != nil {
			t.Fatalf("UpdateTask() error = %v", err)
		}

		fetched, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if fetched.Pinned {
			t.Errorf("Pinned = %v, want false", fetched.Pinned)
		}
	})
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

// TestCLIExecuteTaskDangerous tests queueing a task for execution in dangerous mode
func TestCLIExecuteTaskDangerous(t *testing.T) {
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
		Title:  "Task to execute dangerously",
		Status: db.StatusBacklog,
		Type:   db.TypeCode,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Set dangerous mode and queue (simulate execute --dangerous)
	if err := database.UpdateTaskDangerousMode(task.ID, true); err != nil {
		t.Fatalf("UpdateTaskDangerousMode() error = %v", err)
	}
	if err := database.UpdateTaskStatus(task.ID, db.StatusQueued); err != nil {
		t.Fatalf("UpdateTaskStatus() error = %v", err)
	}

	// Verify status and dangerous mode
	fetched, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if fetched.Status != db.StatusQueued {
		t.Errorf("Status = %v, want %v", fetched.Status, db.StatusQueued)
	}
	if !fetched.DangerousMode {
		t.Error("DangerousMode = false, want true")
	}
}

// TestCLICreateTaskDangerous tests creating a task with --execute --dangerous
func TestCLICreateTaskDangerous(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Create a task with dangerous mode (simulate create --execute --dangerous)
	task := &db.Task{
		Title:         "Dangerous task",
		Status:        db.StatusQueued,
		Type:          db.TypeCode,
		DangerousMode: true,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Verify dangerous mode persists through CreateTask
	fetched, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if fetched.Status != db.StatusQueued {
		t.Errorf("Status = %v, want %v", fetched.Status, db.StatusQueued)
	}
	if !fetched.DangerousMode {
		t.Error("DangerousMode = false, want true")
	}
}

// TestCLICreateTaskDangerousWithoutExecute tests that --dangerous without --execute doesn't set dangerous mode
func TestCLICreateTaskDangerousWithoutExecute(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()
	defer os.Remove(dbPath)

	// Simulate create --dangerous (without --execute): DangerousMode should be false
	task := &db.Task{
		Title:         "Not really dangerous",
		Status:        db.StatusBacklog,
		Type:          db.TypeCode,
		DangerousMode: false, // --dangerous && --execute is false
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	fetched, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if fetched.DangerousMode {
		t.Error("DangerousMode = true, want false (--dangerous without --execute should not set dangerous mode)")
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

// TestCLIMoveTask tests moving a task to a different project
func TestCLIMoveTask(t *testing.T) {
	// Test 1: Basic move preserves task content
	t.Run("move preserves task content", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("failed to open database: %v", err)
		}
		defer database.Close()

		// Create source and target projects
		srcProjectDir := filepath.Join(tmpDir, "src-project")
		tgtProjectDir := filepath.Join(tmpDir, "tgt-project")
		os.MkdirAll(srcProjectDir, 0755)
		os.MkdirAll(tgtProjectDir, 0755)

		if err := database.CreateProject(&db.Project{Name: "src-project", Path: srcProjectDir}); err != nil {
			t.Fatalf("failed to create src-project: %v", err)
		}
		if err := database.CreateProject(&db.Project{Name: "tgt-project", Path: tgtProjectDir}); err != nil {
			t.Fatalf("failed to create tgt-project: %v", err)
		}
		task := &db.Task{
			Title:   "Task to move",
			Body:    "Task description",
			Status:  db.StatusBacklog,
			Type:    db.TypeWriting,
			Tags:    "tag1,tag2",
			Project: "src-project",
			Pinned:  true,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
		oldID := task.ID

		// Move the task
		newID, err := moveTask(database, task, "tgt-project")
		if err != nil {
			t.Fatalf("moveTask() error = %v", err)
		}

		// Verify old task is deleted
		oldTask, err := database.GetTask(oldID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if oldTask != nil {
			t.Error("expected old task to be deleted")
		}

		// Verify new task exists with correct content
		newTask, err := database.GetTask(newID)
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if newTask == nil {
			t.Fatal("expected new task to exist")
		}
		if newTask.Title != "Task to move" {
			t.Errorf("Title = %v, want 'Task to move'", newTask.Title)
		}
		if newTask.Body != "Task description" {
			t.Errorf("Body = %v, want 'Task description'", newTask.Body)
		}
		if newTask.Type != db.TypeWriting {
			t.Errorf("Type = %v, want 'writing'", newTask.Type)
		}
		if newTask.Tags != "tag1,tag2" {
			t.Errorf("Tags = %v, want 'tag1,tag2'", newTask.Tags)
		}
		if newTask.Project != "tgt-project" {
			t.Errorf("Project = %v, want 'tgt-project'", newTask.Project)
		}
		if !newTask.Pinned {
			t.Error("expected Pinned to be true")
		}
	})

	// Test 2: Move resets execution-related fields
	t.Run("move resets execution fields", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("failed to open database: %v", err)
		}
		defer database.Close()

		// Create source and target projects
		srcProjectDir := filepath.Join(tmpDir, "src-project")
		tgtProjectDir := filepath.Join(tmpDir, "tgt-project")
		os.MkdirAll(srcProjectDir, 0755)
		os.MkdirAll(tgtProjectDir, 0755)

		if err := database.CreateProject(&db.Project{Name: "src-project", Path: srcProjectDir}); err != nil {
			t.Fatalf("failed to create src-project: %v", err)
		}
		if err := database.CreateProject(&db.Project{Name: "tgt-project", Path: tgtProjectDir}); err != nil {
			t.Fatalf("failed to create tgt-project: %v", err)
		}
		task := &db.Task{
			Title:           "Task with execution state",
			Status:          db.StatusBacklog,
			Type:            db.TypeCode,
			Project:         "src-project",
			WorktreePath:    "/some/path",
			BranchName:      "task/123-branch",
			Port:            3100,
			ClaudeSessionID: "session-123",
			DaemonSession:   "daemon-123",
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		newID, err := moveTask(database, task, "tgt-project")
		if err != nil {
			t.Fatalf("moveTask() error = %v", err)
		}

		newTask, err := database.GetTask(newID)
		if err != nil || newTask == nil {
			t.Fatalf("failed to get new task: %v", err)
		}

		// Verify execution fields are reset
		if newTask.WorktreePath != "" {
			t.Errorf("WorktreePath = %v, want ''", newTask.WorktreePath)
		}
		if newTask.BranchName != "" {
			t.Errorf("BranchName = %v, want ''", newTask.BranchName)
		}
		if newTask.Port != 0 {
			t.Errorf("Port = %v, want 0", newTask.Port)
		}
		if newTask.ClaudeSessionID != "" {
			t.Errorf("ClaudeSessionID = %v, want ''", newTask.ClaudeSessionID)
		}
		if newTask.DaemonSession != "" {
			t.Errorf("DaemonSession = %v, want ''", newTask.DaemonSession)
		}
	})

	// Test 3: Move resets status for processing/blocked tasks
	t.Run("move resets processing status to backlog", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("failed to open database: %v", err)
		}
		defer database.Close()

		// Create source and target projects
		srcProjectDir := filepath.Join(tmpDir, "src-project")
		tgtProjectDir := filepath.Join(tmpDir, "tgt-project")
		os.MkdirAll(srcProjectDir, 0755)
		os.MkdirAll(tgtProjectDir, 0755)

		if err := database.CreateProject(&db.Project{Name: "src-project", Path: srcProjectDir}); err != nil {
			t.Fatalf("failed to create src-project: %v", err)
		}
		if err := database.CreateProject(&db.Project{Name: "tgt-project", Path: tgtProjectDir}); err != nil {
			t.Fatalf("failed to create tgt-project: %v", err)
		}
		task := &db.Task{
			Title:   "Processing task",
			Status:  db.StatusProcessing,
			Type:    db.TypeCode,
			Project: "src-project",
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		newID, err := moveTask(database, task, "tgt-project")
		if err != nil {
			t.Fatalf("moveTask() error = %v", err)
		}

		newTask, err := database.GetTask(newID)
		if err != nil || newTask == nil {
			t.Fatalf("failed to get new task: %v", err)
		}
		if newTask.Status != db.StatusBacklog {
			t.Errorf("Status = %v, want 'backlog' (should reset from processing)", newTask.Status)
		}
	})

	// Test 4: Move preserves queued status
	t.Run("move preserves queued status", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("failed to open database: %v", err)
		}
		defer database.Close()

		// Create source and target projects
		srcProjectDir := filepath.Join(tmpDir, "src-project")
		tgtProjectDir := filepath.Join(tmpDir, "tgt-project")
		os.MkdirAll(srcProjectDir, 0755)
		os.MkdirAll(tgtProjectDir, 0755)

		if err := database.CreateProject(&db.Project{Name: "src-project", Path: srcProjectDir}); err != nil {
			t.Fatalf("failed to create src-project: %v", err)
		}
		if err := database.CreateProject(&db.Project{Name: "tgt-project", Path: tgtProjectDir}); err != nil {
			t.Fatalf("failed to create tgt-project: %v", err)
		}
		task := &db.Task{
			Title:   "Queued task",
			Status:  db.StatusQueued,
			Type:    db.TypeCode,
			Project: "src-project",
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		newID, err := moveTask(database, task, "tgt-project")
		if err != nil {
			t.Fatalf("moveTask() error = %v", err)
		}

		newTask, err := database.GetTask(newID)
		if err != nil || newTask == nil {
			t.Fatalf("failed to get new task: %v", err)
		}
		if newTask.Status != db.StatusQueued {
			t.Errorf("Status = %v, want 'queued' (should preserve)", newTask.Status)
		}
	})
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

		// Simulate PreToolUse (agent resumed working)
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

// TestPostToolUseHookLogging tests that PostToolUse hooks log tool usage to the task log.
func TestPostToolUseHookLogging(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Test 1: PostToolUseHook logs tool name
	t.Run("logs tool name to task log", func(t *testing.T) {
		task := &db.Task{
			Title:  "Task for tool logging",
			Status: db.StatusProcessing,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
		if err := database.MarkTaskStarted(task.ID); err != nil {
			t.Fatalf("MarkTaskStarted() error = %v", err)
		}

		// Simulate PostToolUse with tool name
		input := &ClaudeHookInput{
			ToolName:  "Read",
			ToolInput: []byte(`{"file_path": "/path/to/file.go"}`),
		}
		err := handlePostToolUseHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handlePostToolUseHook() error = %v", err)
		}

		// Check that tool use was logged
		logs, err := database.GetTaskLogs(task.ID, 10)
		if err != nil {
			t.Fatalf("GetTaskLogs() error = %v", err)
		}

		found := false
		for _, log := range logs {
			if log.LineType == "tool" && log.Content == "Read: /path/to/file.go" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected tool log entry not found. Logs: %+v", logs)
		}
	})

	// Test 2: PostToolUseHook does not log when tool name is empty
	t.Run("does not log when tool name is empty", func(t *testing.T) {
		task := &db.Task{
			Title:  "Task for empty tool name",
			Status: db.StatusProcessing,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
		if err := database.MarkTaskStarted(task.ID); err != nil {
			t.Fatalf("MarkTaskStarted() error = %v", err)
		}

		// Clear any previous logs by getting initial count
		initialLogs, _ := database.GetTaskLogs(task.ID, 100)
		initialCount := len(initialLogs)

		// Simulate PostToolUse without tool name
		input := &ClaudeHookInput{}
		err := handlePostToolUseHook(database, task.ID, input)
		if err != nil {
			t.Fatalf("handlePostToolUseHook() error = %v", err)
		}

		// Check that no new tool log was added
		logs, err := database.GetTaskLogs(task.ID, 100)
		if err != nil {
			t.Fatalf("GetTaskLogs() error = %v", err)
		}

		toolLogCount := 0
		for _, log := range logs {
			if log.LineType == "tool" {
				toolLogCount++
			}
		}
		if toolLogCount != 0 || len(logs) != initialCount {
			t.Errorf("Expected no new tool logs, got %d tool logs (total logs: %d vs initial: %d)", toolLogCount, len(logs), initialCount)
		}
	})
}

// TestFormatToolLogMessage tests the formatToolLogMessage function.
func TestFormatToolLogMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    *ClaudeHookInput
		expected string
	}{
		{
			name: "Bash with command",
			input: &ClaudeHookInput{
				ToolName:  "Bash",
				ToolInput: []byte(`{"command": "go build ./..."}`),
			},
			expected: "Bash: go build ./...",
		},
		{
			name: "Bash with long command gets truncated",
			input: &ClaudeHookInput{
				ToolName:  "Bash",
				ToolInput: []byte(`{"command": "` + strings.Repeat("a", 150) + `"}`),
			},
			expected: "Bash: " + strings.Repeat("a", 100) + "...",
		},
		{
			name: "Read with file_path",
			input: &ClaudeHookInput{
				ToolName:  "Read",
				ToolInput: []byte(`{"file_path": "/path/to/file.go"}`),
			},
			expected: "Read: /path/to/file.go",
		},
		{
			name: "Write with file_path",
			input: &ClaudeHookInput{
				ToolName:  "Write",
				ToolInput: []byte(`{"file_path": "/path/to/new.go"}`),
			},
			expected: "Write: /path/to/new.go",
		},
		{
			name: "Edit with file_path",
			input: &ClaudeHookInput{
				ToolName:  "Edit",
				ToolInput: []byte(`{"file_path": "/path/to/edit.go"}`),
			},
			expected: "Edit: /path/to/edit.go",
		},
		{
			name: "Glob with pattern",
			input: &ClaudeHookInput{
				ToolName:  "Glob",
				ToolInput: []byte(`{"pattern": "**/*.go"}`),
			},
			expected: "Glob: **/*.go",
		},
		{
			name: "Grep with pattern",
			input: &ClaudeHookInput{
				ToolName:  "Grep",
				ToolInput: []byte(`{"pattern": "func Test"}`),
			},
			expected: "Grep: func Test",
		},
		{
			name: "Task with description",
			input: &ClaudeHookInput{
				ToolName:  "Task",
				ToolInput: []byte(`{"description": "Explore codebase"}`),
			},
			expected: "Task: Explore codebase",
		},
		{
			name: "WebFetch with url",
			input: &ClaudeHookInput{
				ToolName:  "WebFetch",
				ToolInput: []byte(`{"url": "https://example.com"}`),
			},
			expected: "WebFetch: https://example.com",
		},
		{
			name: "WebSearch with query",
			input: &ClaudeHookInput{
				ToolName:  "WebSearch",
				ToolInput: []byte(`{"query": "golang testing"}`),
			},
			expected: "WebSearch: golang testing",
		},
		{
			name: "Unknown tool just shows tool name",
			input: &ClaudeHookInput{
				ToolName:  "CustomTool",
				ToolInput: []byte(`{"custom_field": "value"}`),
			},
			expected: "CustomTool",
		},
		{
			name: "Tool with no input just shows tool name",
			input: &ClaudeHookInput{
				ToolName: "Read",
			},
			expected: "Read",
		},
		{
			name: "Tool with invalid JSON just shows tool name",
			input: &ClaudeHookInput{
				ToolName:  "Read",
				ToolInput: []byte(`invalid json`),
			},
			expected: "Read",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatToolLogMessage(tt.input)
			if result != tt.expected {
				t.Errorf("formatToolLogMessage() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestUnescapeNewlines tests the unescapeNewlines function for CLI input handling.
func TestUnescapeNewlines(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no newlines",
			input:    "simple text",
			expected: "simple text",
		},
		{
			name:     "single literal newline",
			input:    "line1\\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "multiple literal newlines",
			input:    "line1\\nline2\\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "newline at start",
			input:    "\\nline2",
			expected: "\nline2",
		},
		{
			name:     "newline at end",
			input:    "line1\\n",
			expected: "line1\n",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only newlines",
			input:    "\\n\\n\\n",
			expected: "\n\n\n",
		},
		{
			name:     "actual newlines preserved",
			input:    "line1\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "mixed literal and actual newlines",
			input:    "line1\\nline2\nline3",
			expected: "line1\nline2\nline3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := unescapeNewlines(tt.input)
			if result != tt.expected {
				t.Errorf("unescapeNewlines(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestTailModelUpdate tests the tailModel Update method
func TestTailModelUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	m := tailModel{
		db:       database,
		interval: 2 * time.Second,
		showDone: false,
		width:    80,
		height:   24,
	}

	t.Run("q quits", func(t *testing.T) {
		msgs := parseKeyEvents("q")
		_, cmd := m.Update(msgs[0])
		if cmd == nil {
			t.Error("expected quit command, got nil")
		}
	})

	t.Run("esc quits", func(t *testing.T) {
		msgs := parseKeyEvents("esc")
		_, cmd := m.Update(msgs[0])
		if cmd == nil {
			t.Error("expected quit command, got nil")
		}
	})

	t.Run("window resize updates dimensions", func(t *testing.T) {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		um := updated.(tailModel)
		if um.width != 120 || um.height != 40 {
			t.Errorf("expected 120x40, got %dx%d", um.width, um.height)
		}
	})

	t.Run("tick returns new tick command", func(t *testing.T) {
		_, cmd := m.Update(tailTickMsg(time.Now()))
		if cmd == nil {
			t.Error("expected tick command, got nil")
		}
	})
}

// TestTailModelView tests the tailModel View method
func TestTailModelView(t *testing.T) {
	t.Run("empty database shows no tasks", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("failed to open database: %v", err)
		}
		defer database.Close()

		m := tailModel{
			db:       database,
			interval: 2 * time.Second,
			width:    80,
			height:   24,
		}

		view := m.View()
		if !strings.Contains(view, "Live Tail") {
			t.Error("expected header with 'Live Tail'")
		}
		if !strings.Contains(view, "No tasks found") {
			t.Error("expected 'No tasks found' message")
		}
		if !strings.Contains(view, "q/esc to quit") {
			t.Error("expected quit hint in footer")
		}
	})

	t.Run("renders tasks grouped by project", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("failed to open database: %v", err)
		}
		defer database.Close()

		if err := database.CreateProject(&db.Project{Name: "myproject", Path: tmpDir}); err != nil {
			t.Fatalf("failed to create project: %v", err)
		}

		tasks := []*db.Task{
			{Title: "Processing task", Status: db.StatusProcessing, Type: db.TypeCode, Project: "myproject"},
			{Title: "Queued task", Status: db.StatusQueued, Type: db.TypeCode, Project: "myproject"},
			{Title: "Backlog task", Status: db.StatusBacklog, Type: db.TypeCode},
		}
		for _, task := range tasks {
			if err := database.CreateTask(task); err != nil {
				t.Fatalf("failed to create task: %v", err)
			}
		}

		m := tailModel{
			db:       database,
			interval: 2 * time.Second,
			width:    80,
			height:   50,
		}

		view := m.View()
		if !strings.Contains(view, "myproject") {
			t.Error("expected 'myproject' in view")
		}
		if !strings.Contains(view, "Processing task") {
			t.Error("expected 'Processing task' in view")
		}
		if !strings.Contains(view, "Queued task") {
			t.Error("expected 'Queued task' in view")
		}
		if !strings.Contains(view, "Backlog task") {
			t.Error("expected 'Backlog task' in view")
		}
		if !strings.Contains(view, "In Progress") {
			t.Error("expected 'In Progress' status label")
		}
	})

	t.Run("truncates to terminal height", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("failed to open database: %v", err)
		}
		defer database.Close()

		for i := 0; i < 30; i++ {
			task := &db.Task{
				Title:  fmt.Sprintf("Task %d", i),
				Status: db.StatusBacklog,
				Type:   db.TypeCode,
			}
			if err := database.CreateTask(task); err != nil {
				t.Fatalf("failed to create task: %v", err)
			}
		}

		m := tailModel{
			db:       database,
			interval: 2 * time.Second,
			width:    80,
			height:   10,
		}

		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) > 10 {
			t.Errorf("expected at most 10 lines, got %d", len(lines))
		}
		if !strings.Contains(view, "resize terminal") {
			t.Error("expected truncation message")
		}
	})

	t.Run("done tasks hidden by default", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("failed to open database: %v", err)
		}
		defer database.Close()

		tasks := []*db.Task{
			{Title: "Active task", Status: db.StatusQueued, Type: db.TypeCode},
			{Title: "Done task", Status: db.StatusDone, Type: db.TypeCode},
		}
		for _, task := range tasks {
			if err := database.CreateTask(task); err != nil {
				t.Fatalf("failed to create task: %v", err)
			}
		}

		m := tailModel{
			db:       database,
			interval: 2 * time.Second,
			showDone: false,
			width:    80,
			height:   50,
		}

		view := m.View()
		if !strings.Contains(view, "Active task") {
			t.Error("expected active task in view")
		}
		if strings.Contains(view, "Done task") {
			t.Error("done task should be hidden when showDone is false")
		}
	})

	t.Run("done tasks shown when enabled", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("failed to open database: %v", err)
		}
		defer database.Close()

		tasks := []*db.Task{
			{Title: "Active task", Status: db.StatusQueued, Type: db.TypeCode},
			{Title: "Done task", Status: db.StatusDone, Type: db.TypeCode},
		}
		for _, task := range tasks {
			if err := database.CreateTask(task); err != nil {
				t.Fatalf("failed to create task: %v", err)
			}
		}

		m := tailModel{
			db:       database,
			interval: 2 * time.Second,
			showDone: true,
			width:    80,
			height:   50,
		}

		view := m.View()
		if !strings.Contains(view, "Done task") {
			t.Error("done task should be visible when showDone is true")
		}
	})
}

func TestFormatPermissionDetail(t *testing.T) {
	tests := []struct {
		name     string
		input    *ClaudeHookInput
		expected string
	}{
		{
			name:     "empty tool name",
			input:    &ClaudeHookInput{},
			expected: "",
		},
		{
			name: "bash command",
			input: &ClaudeHookInput{
				ToolName:  "Bash",
				ToolInput: []byte(`{"command": "git status"}`),
			},
			expected: "git status",
		},
		{
			name: "read file",
			input: &ClaudeHookInput{
				ToolName:  "Read",
				ToolInput: []byte(`{"file_path": "/src/main.go"}`),
			},
			expected: "/src/main.go",
		},
		{
			name: "write file",
			input: &ClaudeHookInput{
				ToolName:  "Write",
				ToolInput: []byte(`{"file_path": "/src/new.go"}`),
			},
			expected: "/src/new.go",
		},
		{
			name: "edit file",
			input: &ClaudeHookInput{
				ToolName:  "Edit",
				ToolInput: []byte(`{"file_path": "/src/app.go"}`),
			},
			expected: "/src/app.go",
		},
		{
			name: "task tool",
			input: &ClaudeHookInput{
				ToolName:  "Task",
				ToolInput: []byte(`{"description": "Run tests"}`),
			},
			expected: "Run tests",
		},
		{
			name: "long command truncated",
			input: &ClaudeHookInput{
				ToolName:  "Bash",
				ToolInput: []byte(`{"command": "` + strings.Repeat("a", 250) + `"}`),
			},
			expected: strings.Repeat("a", 200) + "...",
		},
		{
			name: "no tool input",
			input: &ClaudeHookInput{
				ToolName: "Bash",
			},
			expected: "",
		},
		{
			name: "unknown tool",
			input: &ClaudeHookInput{
				ToolName:  "CustomTool",
				ToolInput: []byte(`{"foo": "bar"}`),
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPermissionDetail(tt.input)
			if result != tt.expected {
				t.Errorf("formatPermissionDetail() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestShouldSubmitInput verifies the submit decision used by `task input`:
// by default a non-empty message is submitted (Enter pressed), --no-submit
// leaves it in the field, and --enter always submits.
func TestShouldSubmitInput(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		justEnter bool
		noSubmit  bool
		want      bool
	}{
		{name: "message submits by default", message: "yes", want: true},
		{name: "message with --no-submit stays in field", message: "yes", noSubmit: true, want: false},
		{name: "--enter alone submits", justEnter: true, want: true},
		{name: "--enter wins over --no-submit", justEnter: true, noSubmit: true, want: true},
		{name: "empty message does not submit", message: "", want: false},
		{name: "empty message with --no-submit does not submit", message: "", noSubmit: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSubmitInput(tt.message, tt.justEnter, tt.noSubmit); got != tt.want {
				t.Errorf("shouldSubmitInput(%q, %v, %v) = %v, want %v",
					tt.message, tt.justEnter, tt.noSubmit, got, tt.want)
			}
		})
	}
}
