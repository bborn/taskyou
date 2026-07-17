package executor

import (
	"os"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// newGuardTestDB opens a throwaway DB with a project for the guard tests.
func newGuardTestDB(t *testing.T) *db.DB {
	t.Helper()
	f, err := os.CreateTemp("", "guard-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	database, err := db.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	if err := database.CreateProject(&db.Project{Name: "test", Path: "/tmp/test"}); err != nil {
		t.Fatal(err)
	}
	return database
}

// The DAG invariant, enforced at the daemon's spawn gate: a queued step that
// still has an incomplete blocker must never start. If it was mis-queued (a
// race, a stray flip), the daemon refuses it and reverts it to 'blocked' rather
// than running work out of order.
func TestAdmitQueuedTaskRefusesOpenBlocker(t *testing.T) {
	database := newGuardTestDB(t)
	exec := New(database, &config.Config{})

	blocker := &db.Task{Title: "blocker", Status: db.StatusBlocked, Project: "test"}
	if err := database.CreateTask(blocker); err != nil {
		t.Fatal(err)
	}
	step := &db.Task{Title: "mis-queued step", Status: db.StatusQueued, Project: "test"}
	if err := database.CreateTask(step); err != nil {
		t.Fatal(err)
	}
	if err := database.AddDependency(blocker.ID, step.ID, true); err != nil {
		t.Fatal(err)
	}

	if exec.admitQueuedTask(step) {
		t.Error("daemon must refuse a queued step whose blocker is not done")
	}

	got, err := database.GetTask(step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusBlocked {
		t.Errorf("mis-queued step should be reverted to blocked, got %q", got.Status)
	}
}

// A genuinely-ready queued task (no blockers, or all blockers done) is admitted
// and left queued for the executor to run.
func TestAdmitQueuedTaskAllowsReadyTask(t *testing.T) {
	database := newGuardTestDB(t)
	exec := New(database, &config.Config{})

	blocker := &db.Task{Title: "done blocker", Status: db.StatusDone, Project: "test"}
	if err := database.CreateTask(blocker); err != nil {
		t.Fatal(err)
	}
	step := &db.Task{Title: "ready step", Status: db.StatusQueued, Project: "test"}
	if err := database.CreateTask(step); err != nil {
		t.Fatal(err)
	}
	if err := database.AddDependency(blocker.ID, step.ID, true); err != nil {
		t.Fatal(err)
	}

	if !exec.admitQueuedTask(step) {
		t.Error("daemon must admit a queued step whose blockers are all done")
	}

	got, err := database.GetTask(step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusQueued {
		t.Errorf("ready step should stay queued, got %q", got.Status)
	}
}
