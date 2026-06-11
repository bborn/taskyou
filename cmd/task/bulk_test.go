package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func setupBulkTestDB(t *testing.T) (*db.DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	cleanup := func() {
		database.Close()
		os.Remove(dbPath)
	}

	return database, cleanup
}

func createTestTasks(t *testing.T, database *db.DB, count int) []int64 {
	t.Helper()
	var ids []int64
	for i := 0; i < count; i++ {
		task := &db.Task{
			Title:  "Test task " + string(rune('A'+i)),
			Status: db.StatusBacklog,
			Type:   db.TypeCode,
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("failed to create task %d: %v", i, err)
		}
		ids = append(ids, task.ID)
	}
	return ids
}

func TestParseTaskIDs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []int64
		wantErr bool
	}{
		{
			name: "single ID",
			args: []string{"42"},
			want: []int64{42},
		},
		{
			name: "multiple IDs",
			args: []string{"1", "2", "3"},
			want: []int64{1, 2, 3},
		},
		{
			name:    "invalid ID",
			args:    []string{"1", "abc", "3"},
			wantErr: true,
		},
		{
			name:    "empty args",
			args:    []string{},
			want:    nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTaskIDs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTaskIDs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("parseTaskIDs() = %v, want %v", got, tt.want)
					return
				}
				for i := range got {
					if got[i] != tt.want[i] {
						t.Errorf("parseTaskIDs()[%d] = %d, want %d", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestBulkStatusChange(t *testing.T) {
	database, cleanup := setupBulkTestDB(t)
	defer cleanup()

	ids := createTestTasks(t, database, 3)

	// Change all to done
	for _, id := range ids {
		if err := database.UpdateTaskStatus(id, db.StatusDone); err != nil {
			t.Fatalf("failed to update task %d: %v", id, err)
		}
	}

	// Verify all are done
	for _, id := range ids {
		task, err := database.GetTask(id)
		if err != nil {
			t.Fatalf("failed to get task %d: %v", id, err)
		}
		if task.Status != db.StatusDone {
			t.Errorf("task #%d status = %s, want %s", id, task.Status, db.StatusDone)
		}
	}
}

func TestBulkDeleteTasks(t *testing.T) {
	database, cleanup := setupBulkTestDB(t)
	defer cleanup()

	ids := createTestTasks(t, database, 3)

	// Delete all
	for _, id := range ids {
		if err := database.DeleteTask(id); err != nil {
			t.Fatalf("failed to delete task %d: %v", id, err)
		}
	}

	// Verify all deleted
	for _, id := range ids {
		task, err := database.GetTask(id)
		if err != nil {
			t.Fatalf("unexpected error getting deleted task %d: %v", id, err)
		}
		if task != nil {
			t.Errorf("task #%d should be deleted but still exists", id)
		}
	}
}

func TestBulkArchiveTasks(t *testing.T) {
	database, cleanup := setupBulkTestDB(t)
	defer cleanup()

	ids := createTestTasks(t, database, 3)

	// Archive all
	for _, id := range ids {
		if err := database.UpdateTaskStatus(id, db.StatusArchived); err != nil {
			t.Fatalf("failed to archive task %d: %v", id, err)
		}
	}

	// Verify all archived
	for _, id := range ids {
		task, err := database.GetTask(id)
		if err != nil {
			t.Fatalf("failed to get task %d: %v", id, err)
		}
		if task.Status != db.StatusArchived {
			t.Errorf("task #%d status = %s, want %s", id, task.Status, db.StatusArchived)
		}
	}
}

func TestBulkCloseTasks(t *testing.T) {
	database, cleanup := setupBulkTestDB(t)
	defer cleanup()

	ids := createTestTasks(t, database, 3)

	// Close all
	for _, id := range ids {
		if err := database.UpdateTaskStatus(id, db.StatusDone); err != nil {
			t.Fatalf("failed to close task %d: %v", id, err)
		}
	}

	// Verify all done
	for _, id := range ids {
		task, err := database.GetTask(id)
		if err != nil {
			t.Fatalf("failed to get task %d: %v", id, err)
		}
		if task.Status != db.StatusDone {
			t.Errorf("task #%d status = %s, want %s", id, task.Status, db.StatusDone)
		}
	}
}

func TestBulkQueueTasks(t *testing.T) {
	database, cleanup := setupBulkTestDB(t)
	defer cleanup()

	ids := createTestTasks(t, database, 3)

	// Queue all
	for _, id := range ids {
		if err := database.UpdateTaskStatus(id, db.StatusQueued); err != nil {
			t.Fatalf("failed to queue task %d: %v", id, err)
		}
	}

	// Verify all queued
	for _, id := range ids {
		task, err := database.GetTask(id)
		if err != nil {
			t.Fatalf("failed to get task %d: %v", id, err)
		}
		if task.Status != db.StatusQueued {
			t.Errorf("task #%d status = %s, want %s", id, task.Status, db.StatusQueued)
		}
	}
}

func TestBulkSkipsAlreadyDone(t *testing.T) {
	database, cleanup := setupBulkTestDB(t)
	defer cleanup()

	ids := createTestTasks(t, database, 2)

	// Set first task to done already
	if err := database.UpdateTaskStatus(ids[0], db.StatusDone); err != nil {
		t.Fatalf("failed to set task done: %v", err)
	}

	// Verify first is already done
	task, _ := database.GetTask(ids[0])
	if task.Status != db.StatusDone {
		t.Fatalf("expected task to be done")
	}

	// Second task should still be backlog
	task, _ = database.GetTask(ids[1])
	if task.Status != db.StatusBacklog {
		t.Fatalf("expected task to be backlog")
	}
}

func TestBulkHandlesMissingTasks(t *testing.T) {
	database, cleanup := setupBulkTestDB(t)
	defer cleanup()

	// Try to get a non-existent task
	task, err := database.GetTask(9999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task != nil {
		t.Error("expected nil for non-existent task")
	}
}
