package db

import (
	"path/filepath"
	"testing"
)

func openRoutineTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestRoutineRunLifecycle(t *testing.T) {
	database := openRoutineTestDB(t)

	id, err := database.CreateRoutineRun("scout")
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	run, err := database.GetRoutineRun(id)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != RoutineRunStatusRunning {
		t.Errorf("expected running, got %q", run.Status)
	}
	if run.FinishedAt != nil {
		t.Error("expected nil FinishedAt for running run")
	}

	if err := database.SetRoutineRunLogPath(id, "/tmp/run-1.log"); err != nil {
		t.Fatalf("set log path: %v", err)
	}
	if err := database.FinishRoutineRun(id, RoutineRunStatusOK, 0, "all good"); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	run, err = database.GetRoutineRun(id)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != RoutineRunStatusOK || run.ExitCode != 0 || run.Output != "all good" || run.LogPath != "/tmp/run-1.log" {
		t.Errorf("run not updated: %+v", run)
	}
	if run.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
}

func TestGetRoutineRunMissing(t *testing.T) {
	database := openRoutineTestDB(t)
	run, err := database.GetRoutineRun(999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run != nil {
		t.Errorf("expected nil for missing run, got %+v", run)
	}
}

func TestListRoutineRunsOrderAndLimit(t *testing.T) {
	database := openRoutineTestDB(t)

	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := database.CreateRoutineRun("scout")
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
		ids = append(ids, id)
	}
	// A different routine's runs must not leak in.
	if _, err := database.CreateRoutineRun("other"); err != nil {
		t.Fatalf("create run: %v", err)
	}

	runs, err := database.ListRoutineRuns("scout", 2)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if runs[0].ID != ids[2] || runs[1].ID != ids[1] {
		t.Errorf("expected newest first [%d %d], got [%d %d]", ids[2], ids[1], runs[0].ID, runs[1].ID)
	}
}

func TestLatestRoutineRuns(t *testing.T) {
	database := openRoutineTestDB(t)

	first, _ := database.CreateRoutineRun("scout")
	second, _ := database.CreateRoutineRun("scout")
	otherID, _ := database.CreateRoutineRun("other")
	if err := database.FinishRoutineRun(first, RoutineRunStatusOK, 0, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}

	latest, err := database.LatestRoutineRuns()
	if err != nil {
		t.Fatalf("latest runs: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("expected 2 routines, got %d", len(latest))
	}
	if latest["scout"].ID != second {
		t.Errorf("expected latest scout run %d, got %d", second, latest["scout"].ID)
	}
	if latest["other"].ID != otherID {
		t.Errorf("expected latest other run %d, got %d", otherID, latest["other"].ID)
	}
}

func TestDeleteRoutineRuns(t *testing.T) {
	database := openRoutineTestDB(t)

	if _, err := database.CreateRoutineRun("scout"); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := database.CreateRoutineRun("scout"); err != nil {
		t.Fatalf("create run: %v", err)
	}
	otherID, err := database.CreateRoutineRun("other")
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := database.DeleteRoutineRuns("scout"); err != nil {
		t.Fatalf("delete runs: %v", err)
	}

	runs, err := database.ListRoutineRuns("scout", 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected scout runs deleted, got %d", len(runs))
	}
	// Other routines' history is untouched.
	run, err := database.GetRoutineRun(otherID)
	if err != nil || run == nil {
		t.Errorf("other routine's run should survive: %v %v", run, err)
	}
}

func TestHasOpenTaskWithTitle(t *testing.T) {
	// db.Open seeds a default "personal" project for the task to live in.
	database := openRoutineTestDB(t)

	exists, err := database.HasOpenTaskWithTitle("Routine failed: scout")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if exists {
		t.Error("expected no open task yet")
	}

	task := &Task{Title: "Routine failed: scout", Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	exists, err = database.HasOpenTaskWithTitle("Routine failed: scout")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !exists {
		t.Error("expected open task to be found")
	}

	// Done tasks don't count — the alert can fire again after Bruno closes it.
	if err := database.UpdateTaskStatus(task.ID, StatusDone); err != nil {
		t.Fatalf("update status: %v", err)
	}
	exists, err = database.HasOpenTaskWithTitle("Routine failed: scout")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if exists {
		t.Error("done task should not block a new alert")
	}
}
