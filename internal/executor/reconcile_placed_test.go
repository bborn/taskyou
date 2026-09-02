package executor

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func reconcileTestExecutor(t *testing.T) (*Executor, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "test", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return New(database, config.New(database)), database
}

func reconcileTestTask(t *testing.T, database *db.DB, host string) *db.Task {
	t.Helper()
	task := &db.Task{Title: "a task", Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE tasks SET daemon_session = 'task-daemon-23335' WHERE id = ?`, task.ID); err != nil {
		t.Fatal(err)
	}
	task.DaemonSession = "task-daemon-23335"
	if host != "" {
		if err := database.SetTaskPlacementDecision(task.ID, host, "only host", "/srv/repo"); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

// The regression: a placed task's window is on another machine, so the local
// tmux server always says "not here" and the reconciler parked a task whose
// agent was working. tmux answering live on the far side must keep it running.
func TestExecutorWindowLivesAsksThePlacedHost(t *testing.T) {
	e, database := reconcileTestExecutor(t)
	task := reconcileTestTask(t, database, "ik-agents")
	// ssh succeeds: tmux on the far side found the window.
	stubSSH(t, "#!/bin/sh\nexit 0\n")

	// The local tmux server knows nothing about it, which is the whole point.
	e.windowExistsFn = func(int64) bool { return false }

	if !e.executorWindowLives(task) {
		t.Error("a placed task with a live remote window was read as dead")
	}
}

// tmux on the far side exiting non-zero (and not ssh's own 255) means the window
// really is gone, so the task is genuinely orphaned.
func TestExecutorWindowLivesSeesAFinishedRemoteAgent(t *testing.T) {
	e, database := reconcileTestExecutor(t)
	task := reconcileTestTask(t, database, "ik-agents")
	stubSSH(t, "#!/bin/sh\nexit 1\n")
	e.windowExistsFn = func(int64) bool { return false }

	if e.executorWindowLives(task) {
		t.Error("a finished remote agent was read as still running")
	}
}

// A host we cannot reach is a failure to LOOK, not a finished task. Blocking on
// it would park a working agent every time the network blipped.
func TestExecutorWindowLivesKeepsATaskWhoseHostIsUnreachable(t *testing.T) {
	e, database := reconcileTestExecutor(t)
	task := reconcileTestTask(t, database, "ik-agents")
	stubSSH(t, "#!/bin/sh\nexit 255\n") // ssh's own failure code
	e.windowExistsFn = func(int64) bool { return false }

	if !e.executorWindowLives(task) {
		t.Error("an unreachable host was treated as a finished task")
	}
}

// An unplaced task keeps exactly the local check it has always had.
func TestExecutorWindowLivesUsesTheLocalCheckForAnUnplacedTask(t *testing.T) {
	e, database := reconcileTestExecutor(t)
	task := reconcileTestTask(t, database, "")
	// Any ssh at all would be wrong here.
	stubSSH(t, "#!/bin/sh\necho 'ssh was used for a local task' >&2\nexit 1\n")

	e.windowExistsFn = func(int64) bool { return true }
	if !e.executorWindowLives(task) {
		t.Error("a local task with a live window was read as dead")
	}
	e.windowExistsFn = func(int64) bool { return false }
	if e.executorWindowLives(task) {
		t.Error("a local task with no window was read as alive")
	}
}
