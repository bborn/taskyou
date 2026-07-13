package db

import (
	"testing"
	"time"
)

// mkTask creates a minimal task and returns its id.
func mkTask(t *testing.T, database *DB, title string) int64 {
	t.Helper()
	task := &Task{Title: title, Project: "personal", Type: TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return task.ID
}

func listActiveIDs(t *testing.T, database *DB) map[int64]bool {
	t.Helper()
	tasks, err := database.ListTasks(ListTasksOptions{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	ids := map[int64]bool{}
	for _, tk := range tasks {
		ids[tk.ID] = true
	}
	return ids
}

func TestSoftDeleteHidesButKeepsTask(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	id := mkTask(t, database, "trash me")

	if !listActiveIDs(t, database)[id] {
		t.Fatal("task should be listed before soft-delete")
	}

	if err := database.SoftDeleteTask(id); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	// Hidden from the default board/list...
	if listActiveIDs(t, database)[id] {
		t.Error("soft-deleted task should not appear in default ListTasks")
	}
	// ...but the row still exists and is fetchable.
	if got, err := database.GetTask(id); err != nil || got == nil {
		t.Fatalf("GetTask after soft-delete: got=%v err=%v (row must survive)", got, err)
	}
	// ...and shows up in the trash listing.
	trashed, err := database.ListTrashedTasks()
	if err != nil {
		t.Fatalf("list trashed: %v", err)
	}
	if len(trashed) != 1 || trashed[0].ID != id {
		t.Fatalf("expected task %d in trash, got %+v", id, trashed)
	}
	// ...and IncludeTrashed surfaces it in the normal query.
	all, err := database.ListTasks(ListTasksOptions{IncludeTrashed: true})
	if err != nil {
		t.Fatalf("list incl trashed: %v", err)
	}
	found := false
	for _, tk := range all {
		if tk.ID == id {
			found = true
		}
	}
	if !found {
		t.Error("IncludeTrashed should surface the trashed task")
	}
}

func TestRestoreBringsTaskBack(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	id := mkTask(t, database, "restore me")
	if err := database.SoftDeleteTask(id); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	if err := database.RestoreTask(id); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if !listActiveIDs(t, database)[id] {
		t.Error("restored task should reappear in default ListTasks")
	}
	trashed, _ := database.ListTrashedTasks()
	if len(trashed) != 0 {
		t.Errorf("trash should be empty after restore, got %+v", trashed)
	}
}

func TestSweepableRespectsRetention(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	fresh := mkTask(t, database, "just trashed")
	old := mkTask(t, database, "trashed long ago")
	for _, id := range []int64{fresh, old} {
		if err := database.SoftDeleteTask(id); err != nil {
			t.Fatalf("soft-delete %d: %v", id, err)
		}
	}
	// Backdate the "old" one well beyond any retention.
	if _, err := database.Exec(
		"UPDATE tasks SET deleted_at = ? WHERE id = ?",
		time.Now().Add(-30*24*time.Hour).UTC(), old,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	sweepable, err := database.GetSweepableTrashedTasks(14 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("sweepable: %v", err)
	}
	if len(sweepable) != 1 || sweepable[0].ID != old {
		t.Fatalf("only the long-trashed task should be sweepable at 14d, got %+v", sweepable)
	}
}

func TestSoftDeletedTaskFreesPort(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	id := mkTask(t, database, "holds a port")
	if _, err := database.Exec("UPDATE tasks SET port = 4242 WHERE id = ?", id); err != nil {
		t.Fatalf("set port: %v", err)
	}

	ports, _ := database.GetActiveTaskPorts()
	if !ports[4242] {
		t.Fatal("port should be held while task is live")
	}

	if err := database.SoftDeleteTask(id); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	ports, _ = database.GetActiveTaskPorts()
	if ports[4242] {
		t.Error("soft-deleted task must not keep its port reserved")
	}
}
