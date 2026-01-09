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

	// Create a task
	task := &Task{
		Title:    "Test Task",
		Body:     "Test body",
		Status:   StatusBacklog,
		Type:     TypeCode,
		Project:  "test",
		Priority: "normal",
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
		Title:    "Test Task",
		Body:     "Test body",
		Status:   StatusBacklog,
		Type:     TypeCode,
		Project:  "", // Empty project
		Priority: "normal",
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
