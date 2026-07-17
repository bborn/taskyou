package executor

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func newVerifySweepExecutor(t *testing.T) (*Executor, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return New(database, &config.Config{}), database
}

func gatedStepTask(t *testing.T, database *db.DB, verifyCmd string) *db.Task {
	t.Helper()
	task := &db.Task{Title: "[model] x", Status: db.StatusProcessing, Project: "p", Tags: "pipeline"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	task.WorktreePath = t.TempDir()
	if err := database.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := database.SetStepVerify(task.ID, verifyCmd); err != nil {
		t.Fatal(err)
	}
	return task
}

// A git-finished step whose verify command FAILS must NOT be auto-completed by the
// sweep — otherwise a step that committed broken work but never called
// taskyou_complete would advance the DAG unchecked.
func TestVerifyGateBlocksSweepAutoComplete(t *testing.T) {
	ex, database := newVerifySweepExecutor(t)
	task := gatedStepTask(t, database, "exit 1")

	ex.verifyThenAutoComplete(task, "exit 1")

	got, _ := database.GetTask(task.ID)
	if got.Status == db.StatusDone {
		t.Error("sweep auto-completed a step whose verify command failed")
	}
}

// A git-finished step whose verify command PASSES is auto-completed as before.
func TestVerifyGatePassesSweepAutoComplete(t *testing.T) {
	ex, database := newVerifySweepExecutor(t)
	task := gatedStepTask(t, database, "exit 0")

	ex.verifyThenAutoComplete(task, "exit 0")

	got, _ := database.GetTask(task.ID)
	if got.Status != db.StatusDone {
		t.Errorf("status = %q, want done after verify passed", got.Status)
	}
}
