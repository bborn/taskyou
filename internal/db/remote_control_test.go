package db

import (
	"os"
	"path/filepath"
	"testing"
)

func newRemoteControlTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
		os.Remove(dbPath)
	})
	return database
}

// TestCreateTaskRemoteControlRoundTrip verifies the RemoteControl flag persists
// through CreateTask and is read back by GetTask.
func TestCreateTaskRemoteControlRoundTrip(t *testing.T) {
	database := newRemoteControlTestDB(t)
	if err := database.CreateProject(&Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	task := &Task{Title: "T", Status: StatusQueued, Type: TypeCode, Project: "p", RemoteControl: true}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !got.RemoteControl {
		t.Errorf("RemoteControl should be true after round-trip, got %v", got.RemoteControl)
	}
}

// TestCreateTaskRemoteControlDefaultsFalse verifies RemoteControl defaults to
// false when not set on the task.
func TestCreateTaskRemoteControlDefaultsFalse(t *testing.T) {
	database := newRemoteControlTestDB(t)
	if err := database.CreateProject(&Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	task := &Task{Title: "T", Status: StatusQueued, Type: TypeCode, Project: "p"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.RemoteControl {
		t.Errorf("RemoteControl should default to false, got %v", got.RemoteControl)
	}
}
