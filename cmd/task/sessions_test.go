package main

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// requireTmux skips the test if tmux is not available.
func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := osexec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available in PATH")
	}
}

// makeDaemonSession creates a tmux daemon session with a window for the given
// task and returns a cleanup func. The session name is intentionally chosen so
// it does NOT match what getDaemonSessionName() would return for the current
// test process — that's the scenario we need to exercise.
func makeDaemonSession(t *testing.T, sessionID string, taskID int) func() {
	t.Helper()
	sessionName := fmt.Sprintf("task-daemon-%s", sessionID)
	windowName := fmt.Sprintf("task-%d", taskID)

	if err := osexec.Command("tmux", "new-session", "-d", "-s", sessionName, "-n", windowName, "sleep", "60").Run(); err != nil {
		t.Fatalf("create daemon session: %v", err)
	}

	cleanup := func() {
		osexec.Command("tmux", "kill-session", "-t", sessionName).Run()
	}

	if err := osexec.Command("tmux", "list-panes", "-t", sessionName+":"+windowName).Run(); err != nil {
		cleanup()
		t.Fatalf("expected window to exist after creation: %v", err)
	}
	return cleanup
}

func windowExists(sessionName, windowName string) bool {
	return osexec.Command("tmux", "list-panes", "-t", sessionName+":"+windowName).Run() == nil
}

// TestKillSession_OnlySearchesCurrentDaemonSession documents the limitation of
// killSession: it only looks in the daemon session computed from the current
// process's session ID. A window living in a daemon session with a different
// ID is invisible to it. This is the bug surface for deleteTask/moveTask.
func TestKillSession_OnlySearchesCurrentDaemonSession(t *testing.T) {
	requireTmux(t)

	const taskID = 991001
	otherSessionID := "test-orphan-991001"
	cleanup := makeDaemonSession(t, otherSessionID, taskID)
	defer cleanup()

	// Make sure the current process's WORKTREE_SESSION_ID won't accidentally
	// point at the test daemon session.
	t.Setenv("WORKTREE_SESSION_ID", "")

	// Sanity: getDaemonSessionName() should NOT equal our test daemon session.
	if got := getDaemonSessionName(); got == "task-daemon-"+otherSessionID {
		t.Fatalf("test setup broken: getDaemonSessionName=%q matches the test session", got)
	}

	// killSession is scoped to the current daemon session only and should fail.
	if err := killSession(taskID); err == nil {
		t.Error("killSession returned nil even though target window lives in a different daemon session")
	}

	// Window should still exist — proving why the bug masquerades as silent
	// success when callers ignore the error (as deleteTask used to do).
	if !windowExists("task-daemon-"+otherSessionID, fmt.Sprintf("task-%d", taskID)) {
		t.Error("window should still exist; killSession is daemon-scoped and shouldn't have reached it")
	}
}

// TestKillSessionAcrossDaemons_FindsWindowInDifferentDaemon proves that the
// across-daemons variant correctly locates and kills a task window regardless
// of which daemon session originally spawned it. This is what deleteTask and
// moveTask should use.
func TestKillSessionAcrossDaemons_FindsWindowInDifferentDaemon(t *testing.T) {
	requireTmux(t)

	const taskID = 991002
	otherSessionID := "test-orphan-991002"
	cleanup := makeDaemonSession(t, otherSessionID, taskID)
	defer cleanup()

	t.Setenv("WORKTREE_SESSION_ID", "")

	if !killSessionAcrossDaemons(taskID) {
		t.Fatal("killSessionAcrossDaemons returned false; expected to find and kill window in foreign daemon session")
	}

	if windowExists("task-daemon-"+otherSessionID, fmt.Sprintf("task-%d", taskID)) {
		t.Error("window should be gone after killSessionAcrossDaemons")
	}
}

// TestDeleteTask_KillsAgentInForeignDaemonSession is the user-facing scenario:
// a task's tmux window lives in a daemon session whose ID does not match the
// current ty CLI invocation's session ID. ty delete should still kill the
// agent. This was broken because deleteTask called killSession (scoped to
// current daemon) instead of killSessionAcrossDaemons.
func TestDeleteTask_KillsAgentInForeignDaemonSession(t *testing.T) {
	requireTmux(t)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	t.Setenv("WORKTREE_DB_PATH", dbPath)

	// Override default DB path so deleteTask uses our temp DB.
	// db.DefaultPath honors WORKTREE_DB_PATH.
	if got := db.DefaultPath(); got != dbPath {
		t.Skipf("db.DefaultPath() does not honor WORKTREE_DB_PATH (got %q); cannot run this test in isolation", got)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	task := &db.Task{
		Title:  "Foreign-daemon orphan reproducer",
		Status: db.StatusBlocked,
		Type:   db.TypeCode,
	}
	if err := database.CreateTask(task); err != nil {
		database.Close()
		t.Fatalf("create task: %v", err)
	}
	database.Close()

	otherSessionID := fmt.Sprintf("test-foreign-%d", task.ID)
	cleanup := makeDaemonSession(t, otherSessionID, int(task.ID))
	defer cleanup()

	t.Setenv("WORKTREE_SESSION_ID", "")

	if err := deleteTask(task.ID); err != nil {
		t.Fatalf("deleteTask returned error: %v", err)
	}

	if windowExists("task-daemon-"+otherSessionID, fmt.Sprintf("task-%d", task.ID)) {
		t.Error("agent window still alive after deleteTask; agent leaked into orphan state")
	}

	// And the DB row should be gone too.
	database, err = db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer database.Close()
	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected task row deleted, got: %+v", got)
	}
	_ = os.Remove(dbPath)
}
