package db

import (
	"path/filepath"
	"testing"
)

func placementTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	database, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&Project{Name: "taskyou", Path: dir}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return database
}

// placementTask creates a task in the test project and returns it.
func placementTask(t *testing.T, database *DB, title string) *Task {
	t.Helper()
	task := &Task{Title: title, Status: StatusBacklog, Type: TypeCode, Project: "taskyou"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// A task nobody asked a placement question about looks exactly as it always
// did: no host, no reason.
func TestTaskPlacementDefaultsToEmpty(t *testing.T) {
	database := placementTestDB(t)
	task := placementTask(t, database, "local task")

	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.PlacementTarget != "" || got.PlacementReason != "" {
		t.Errorf("placement = (%q, %q), want empty for an unplaced task", got.PlacementTarget, got.PlacementReason)
	}
}

func TestSetAndGetTaskPlacement(t *testing.T) {
	database := placementTestDB(t)
	task := placementTask(t, database, "remote task")

	const (
		host   = "ol-agents"
		reason = "most free memory of 2 hosts serving offerlab"
	)
	if err := database.SetTaskPlacement(task.ID, host, reason); err != nil {
		t.Fatalf("set placement: %v", err)
	}

	gotTarget, gotReason, err := database.GetTaskPlacement(task.ID)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	if gotTarget != host || gotReason != reason {
		t.Errorf("GetTaskPlacement = (%q, %q), want (%q, %q)", gotTarget, gotReason, host, reason)
	}

	// It must also come back on the task itself, which is what `ty show` and the
	// board read.
	loaded, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if loaded.PlacementTarget != host || loaded.PlacementReason != reason {
		t.Errorf("task placement = (%q, %q), want (%q, %q)",
			loaded.PlacementTarget, loaded.PlacementReason, host, reason)
	}
}

// A resolver that deliberately chose "run here" gives a reason worth keeping —
// it is how a user finds out why their fleet was not used.
func TestSetTaskPlacementRecordsALocalDecision(t *testing.T) {
	database := placementTestDB(t)
	task := placementTask(t, database, "local by choice")
	if err := database.SetTaskPlacement(task.ID, "", "no host serves taskyou"); err != nil {
		t.Fatalf("set placement: %v", err)
	}

	loaded, _ := database.GetTask(task.ID)
	if loaded.PlacementTarget != "" {
		t.Errorf("PlacementTarget = %q, want empty", loaded.PlacementTarget)
	}
	if loaded.PlacementReason != "no host serves taskyou" {
		t.Errorf("PlacementReason = %q", loaded.PlacementReason)
	}
}
