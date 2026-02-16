package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTimestampLocalization(t *testing.T) {
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

	// Create a task
	task := &Task{
		Title:   "Test Task",
		Body:    "Test body",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "test",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Retrieve the task
	retrieved, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	// Check that CreatedAt is in local timezone
	localZone := time.Now().Location()
	if retrieved.CreatedAt.Location() != localZone {
		t.Errorf("expected CreatedAt timezone %v, got %v", localZone, retrieved.CreatedAt.Location())
	}

	// Verify the time is reasonable (within last minute)
	now := time.Now()
	diff := now.Sub(retrieved.CreatedAt.Time)
	if diff < 0 || diff > time.Minute {
		t.Errorf("CreatedAt %v is not within expected range of now %v", retrieved.CreatedAt.Time, now)
	}
}

func TestBusyTimeoutIsSet(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	var timeout int
	if err := database.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("failed to query busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("expected busy_timeout=5000, got %d", timeout)
	}
}

func TestPersonalProjectCreation(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Verify 'personal' project was created
	personal, err := db.GetProjectByName("personal")
	if err != nil {
		t.Fatalf("failed to get personal project: %v", err)
	}
	if personal == nil {
		t.Fatal("personal project was not created")
	}
	if personal.Name != "personal" {
		t.Errorf("expected project name 'personal', got '%s'", personal.Name)
	}

	// Verify that creating a task with empty project defaults to 'personal'
	task := &Task{
		Title:   "Test Task",
		Body:    "Test body",
		Status:  StatusBacklog,
		Type:    TypeCode,
		Project: "", // Empty project
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Retrieve the task and verify it has 'personal' project
	retrieved, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.Project != "personal" {
		t.Errorf("expected task project 'personal', got '%s'", retrieved.Project)
	}
}

func TestProjectContext(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	defer os.Remove(dbPath)

	// Create a test project
	projectName := "test-context-project"
	if err := db.CreateProject(&Project{Name: projectName, Path: tmpDir}); err != nil {
		t.Fatalf("failed to create test project: %v", err)
	}

	// Initially context should be empty
	context, err := db.GetProjectContext(projectName)
	if err != nil {
		t.Fatalf("failed to get project context: %v", err)
	}
	if context != "" {
		t.Errorf("expected empty context initially, got '%s'", context)
	}

	// Set project context
	testContext := "This is a Go project with a cmd/ and internal/ structure. Key packages: db, executor, mcp."
	if err := db.SetProjectContext(projectName, testContext); err != nil {
		t.Fatalf("failed to set project context: %v", err)
	}

	// Verify context was saved
	context, err = db.GetProjectContext(projectName)
	if err != nil {
		t.Fatalf("failed to get project context: %v", err)
	}
	if context != testContext {
		t.Errorf("expected context '%s', got '%s'", testContext, context)
	}

	// Update context
	newContext := "Updated context with more details."
	if err := db.SetProjectContext(projectName, newContext); err != nil {
		t.Fatalf("failed to update project context: %v", err)
	}

	// Verify updated context
	context, err = db.GetProjectContext(projectName)
	if err != nil {
		t.Fatalf("failed to get updated project context: %v", err)
	}
	if context != newContext {
		t.Errorf("expected updated context '%s', got '%s'", newContext, context)
	}

	// Test getting context for non-existent project
	context, err = db.GetProjectContext("nonexistent-project")
	if err != nil {
		t.Fatalf("unexpected error for non-existent project: %v", err)
	}
	if context != "" {
		t.Errorf("expected empty context for non-existent project, got '%s'", context)
	}

	// Test setting context for non-existent project (should fail)
	err = db.SetProjectContext("nonexistent-project", "some context")
	if err == nil {
		t.Error("expected error when setting context for non-existent project")
	}
}
