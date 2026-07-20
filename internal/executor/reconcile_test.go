package executor

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func newTestExecutor(t *testing.T) (*Executor, *db.DB) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "test-reconcile-*.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	if err := database.CreateProject(&db.Project{Name: "test", Path: "/tmp/test"}); err != nil {
		t.Fatal(err)
	}

	exec := New(database, &config.Config{})
	return exec, database
}

func createProcessingTask(t *testing.T, database *db.DB, title string) *db.Task {
	t.Helper()
	task := &db.Task{Title: title, Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskStatus(task.ID, db.StatusProcessing); err != nil {
		t.Fatal(err)
	}
	return task
}

// TestReconcileOrphanedTasksMovesDeadTasksToBlocked verifies that a task stuck in
// 'processing' with no live executor window is moved to 'blocked' on startup,
// with a log entry recording why.
func TestReconcileOrphanedTasksMovesDeadTasksToBlocked(t *testing.T) {
	exec, database := newTestExecutor(t)

	orphan := createProcessingTask(t, database, "orphaned task")

	// No live executor window for any task.
	exec.windowExistsFn = func(taskID int64) bool { return false }

	exec.reconcileOrphanedTasks(true)

	got, err := database.GetTask(orphan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusBlocked {
		t.Fatalf("expected orphaned task to be blocked, got %q", got.Status)
	}

	// A log entry should record the reconciliation.
	logs, err := database.GetTaskLogs(orphan.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range logs {
		if l.LineType == "error" && strings.Contains(l.Content, "daemon restart") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a log entry recording the orphan reconciliation, got %+v", logs)
	}
}

// TestReconcileOrphanedTasksLeavesLiveTasksAlone verifies that a processing task
// whose executor window is still alive is not touched.
func TestReconcileOrphanedTasksLeavesLiveTasksAlone(t *testing.T) {
	exec, database := newTestExecutor(t)

	live := createProcessingTask(t, database, "live task")

	// The executor window exists for this task - it's genuinely running.
	exec.windowExistsFn = func(taskID int64) bool { return true }

	exec.reconcileOrphanedTasks(true)

	got, err := database.GetTask(live.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusProcessing {
		t.Fatalf("expected live task to stay processing, got %q", got.Status)
	}
}

// TestReconcileOrphanedTasksSkipsRunningTasks verifies that a task this executor
// is actively running is never reconciled, even if no window is detected.
func TestReconcileOrphanedTasksSkipsRunningTasks(t *testing.T) {
	exec, database := newTestExecutor(t)

	running := createProcessingTask(t, database, "running task")

	exec.mu.Lock()
	exec.runningTasks[running.ID] = true
	exec.mu.Unlock()

	exec.windowExistsFn = func(taskID int64) bool { return false }

	exec.reconcileOrphanedTasks(true)

	got, err := database.GetTask(running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusProcessing {
		t.Fatalf("expected actively-running task to stay processing, got %q", got.Status)
	}
}

// TestReconcileOrphanedTasksOnlyTouchesProcessing verifies non-processing tasks
// are ignored.
func TestReconcileOrphanedTasksOnlyTouchesProcessing(t *testing.T) {
	exec, database := newTestExecutor(t)

	queued := &db.Task{Title: "queued task", Type: "task", Project: "test"}
	if err := database.CreateTask(queued); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskStatus(queued.ID, db.StatusQueued); err != nil {
		t.Fatal(err)
	}

	exec.windowExistsFn = func(taskID int64) bool { return false }

	exec.reconcileOrphanedTasks(true)

	got, err := database.GetTask(queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusQueued {
		t.Fatalf("expected queued task to stay queued, got %q", got.Status)
	}
}

// backdateStartedAt pushes a task's started_at into the past so the periodic
// sweep's spawn grace period no longer covers it.
func backdateStartedAt(t *testing.T, database *db.DB, taskID int64, d time.Duration) {
	t.Helper()
	_, err := database.Exec(
		"UPDATE tasks SET started_at = ? WHERE id = ?",
		time.Now().Add(-d).UTC(),
		taskID,
	)
	if err != nil {
		t.Fatal(err)
	}
}

// TestReconcileOrphanedTasksPeriodicSweepsDeadExecutor is the regression test for
// an executor that dies mid-run while the daemon stays up. Before the periodic
// sweep existed this task sat in 'processing' with no pane until the next daemon
// restart, and the board reported it as still running.
func TestReconcileOrphanedTasksPeriodicSweepsDeadExecutor(t *testing.T) {
	exec, database := newTestExecutor(t)

	orphan := createProcessingTask(t, database, "executor died mid-run")
	backdateStartedAt(t, database, orphan.ID, 10*time.Minute)

	// The executor's window is gone, and the daemon never had it in runningTasks
	// (e.g. the TUI started it).
	exec.windowExistsFn = func(taskID int64) bool { return false }

	exec.reconcileOrphanedTasks(false)

	got, err := database.GetTask(orphan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusBlocked {
		t.Fatalf("expected dead-executor task to be blocked, got %q", got.Status)
	}

	logs, err := database.GetTaskLogs(orphan.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range logs {
		if l.LineType == "error" && strings.Contains(l.Content, "Executor died") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a log entry recording the dead executor, got %+v", logs)
	}
}

// TestReconcileOrphanedTasksPeriodicRespectsSpawnGrace verifies the periodic
// sweep leaves a just-started task alone. A freshly spawned executor needs a
// moment before its tmux window is visible, and a TUI-started task is never in
// this executor's runningTasks - without the grace period the sweep would block
// a task that is still coming up.
func TestReconcileOrphanedTasksPeriodicRespectsSpawnGrace(t *testing.T) {
	exec, database := newTestExecutor(t)

	// createProcessingTask stamps started_at as it flips to processing, so this
	// task is inside the grace window.
	starting := createProcessingTask(t, database, "just spawned")

	exec.windowExistsFn = func(taskID int64) bool { return false }

	exec.reconcileOrphanedTasks(false)

	got, err := database.GetTask(starting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusProcessing {
		t.Fatalf("expected just-started task to stay processing, got %q", got.Status)
	}
}

// TestReconcileOrphanedTasksStartupIgnoresSpawnGrace verifies the startup pass
// still sweeps a recently-started task: after a daemon restart every executor
// pane is dead no matter how recently the task began.
func TestReconcileOrphanedTasksStartupIgnoresSpawnGrace(t *testing.T) {
	exec, database := newTestExecutor(t)

	recent := createProcessingTask(t, database, "started just before restart")

	exec.windowExistsFn = func(taskID int64) bool { return false }

	exec.reconcileOrphanedTasks(true)

	got, err := database.GetTask(recent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.StatusBlocked {
		t.Fatalf("expected startup sweep to block recent task, got %q", got.Status)
	}
}
