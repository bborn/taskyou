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
