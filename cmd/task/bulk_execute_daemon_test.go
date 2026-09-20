package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tmuxtest"
)

// This file regresses a bug introduced by c818219: `ty bulk execute <valid>
// <missing>` called printBulkSummary — which os.Exit(1)s whenever any item
// failed — before ensureDaemonForQueuedWork, so a mixed succeed/fail batch
// exited non-zero without starting the daemon. The successfully-queued tasks
// were then left in `queued` with nothing to pick them up, because the daemon
// is the only thing that polls for queued work and no other path auto-starts
// it. The fix swaps the order so the daemon is ensured before the
// (possibly-exiting) summary runs.
//
// Each test builds the real `ty` binary and drives it against an isolated
// WORKTREE_DB_PATH, then asserts a live daemon was left behind. The daemon is
// torn down in t.Cleanup so no real daemon ever outlives the suite.

// setupBulkDaemonDB opens an isolated DB at a temp path, disables the
// daemon's HTTP API so a daemon started during the test cannot contend for a
// port with any live daemon, and returns the temp dir (where the daemon pid
// file lands) and the open handle. WORKTREE_DB_PATH is set for the test
// process, so the built binary — which inherits the env — writes its pid file
// under dir as well (getPidFilePath keys on filepath.Dir(db.DefaultPath())).
func setupBulkDaemonDB(t *testing.T) (dir string, database *db.DB) {
	t.Helper()
	dir = t.TempDir()
	dbPath := filepath.Join(dir, "tasks.db")
	t.Setenv("WORKTREE_DB_PATH", dbPath)
	t.Setenv("TY_SKIP_VERSION_CHECK", "1")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.SetSetting(config.SettingHTTPAPIDisabled, "true"); err != nil {
		t.Fatalf("disable daemon http api: %v", err)
	}
	return dir, database
}

// createBacklogTask adds one backlog task to database and returns its id.
func createBacklogTask(t *testing.T, database *db.DB) int64 {
	t.Helper()
	task := &db.Task{
		Title:   "bulk execute daemon regression",
		Status:  db.StatusBacklog,
		Type:    db.TypeCode,
		Project: "personal",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task.ID
}

// runBulkExecute runs `ty bulk execute <args...>` against the test's isolated
// DB and returns the combined output and exit error.
func runBulkExecute(t *testing.T, bin string, args ...string) ([]byte, error) {
	t.Helper()
	full := append([]string{bin, "bulk", "execute"}, args...)
	cmd := exec.Command(full[0], full[1:]...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return out, err
}

// daemonAliveInDir polls up to timeout for a live daemon pid file in dir and
// returns the live pid, or 0 if none appeared. The pid file is written
// synchronously by ensureDaemonRunning before the `ty bulk execute` process
// exits, so it is normally present on the first poll.
func daemonAliveInDir(dir string, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid, err := readPidFile(filepath.Join(dir, "daemon.pid")); err == nil && pid > 0 && processExists(pid) {
			return pid
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0
}

// taskWasQueued reports whether the task moved out of backlog — i.e. the bulk
// execute actually queued it. It tolerates the daemon picking the task up in
// the meantime (status may have advanced to processing or blocked), so the
// assertion is race-free.
func taskWasQueued(t *testing.T, database *db.DB, taskID int64) bool {
	t.Helper()
	task, err := database.GetTask(taskID)
	if err != nil || task == nil {
		return false
	}
	return task.Status != db.StatusBacklog
}

// stopDaemonForTest tears down any daemon this test started. stopDaemon sends
// SIGTERM and removes the pid/mode/record files; if the daemon has not exited
// within a short grace window it is force-killed so the test never leaves a
// daemon running. The daemon is detached (Setsid), so it survives the
// `ty bulk execute` process and must be killed explicitly.
func stopDaemonForTest() {
	pid, _ := readPidFile(getPidFilePath())
	_ = stopDaemon()
	if pid <= 0 {
		return
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && processExists(pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if processExists(pid) {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGKILL)
		}
	}
}

// TestBulkExecuteEnsuresDaemon_MixedBatch is the core regression: with no
// daemon running and no pid file, a mixed succeed/fail batch must still start
// the daemon for the successfully-queued task, while preserving the non-zero
// exit on the partial failure.
func TestBulkExecuteEnsuresDaemon_MixedBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the ty binary")
	}
	dir, database := setupBulkDaemonDB(t)
	tmuxtest.Isolate(t)
	t.Cleanup(stopDaemonForTest)
	validID := createBacklogTask(t, database)
	bin := buildTyBinary(t)

	out, err := runBulkExecute(t, bin, fmt.Sprint(validID), "99")

	if err == nil {
		t.Fatalf("ty bulk execute <valid> <missing> exited 0; non-zero exit on partial failure must be preserved.\noutput:\n%s", out)
	}
	if !taskWasQueued(t, database, validID) {
		t.Fatalf("ty bulk execute did not queue the valid task #%d.\noutput:\n%s", validID, out)
	}
	if pid := daemonAliveInDir(dir, 5*time.Second); pid == 0 {
		t.Fatalf("ty bulk execute did not start a daemon for the successfully-queued task (no live pid in %s).\noutput:\n%s", dir, out)
	}
}

// TestBulkExecuteEnsuresDaemon_StalePidFile covers the stale-pidfile branch
// of ensureDaemonRunning: a dead pid in the pid file must be detected, the
// stale file cleaned up, and a fresh daemon started — all before the
// os.Exit(1) in printBulkSummary.
func TestBulkExecuteEnsuresDaemon_StalePidFile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the ty binary")
	}
	dir, database := setupBulkDaemonDB(t)
	tmuxtest.Isolate(t)
	t.Cleanup(stopDaemonForTest)
	validID := createBacklogTask(t, database)

	// Seed a stale pid file: start a process, capture its pid, kill it, then
	// write the dead pid into the daemon pid file.
	sleep := exec.Command("sleep", "100000")
	if err := sleep.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	stalePID := sleep.Process.Pid
	_ = sleep.Process.Kill()
	_ = sleep.Wait()
	if err := writePidFile(getPidFilePath(), stalePID); err != nil {
		t.Fatalf("write stale pid file: %v", err)
	}

	bin := buildTyBinary(t)
	out, err := runBulkExecute(t, bin, fmt.Sprint(validID), "99")

	if err == nil {
		t.Fatalf("ty bulk execute <valid> <missing> exited 0; non-zero exit on partial failure must be preserved.\noutput:\n%s", out)
	}
	if !taskWasQueued(t, database, validID) {
		t.Fatalf("ty bulk execute did not queue the valid task #%d.\noutput:\n%s", validID, out)
	}
	pid := daemonAliveInDir(dir, 5*time.Second)
	if pid == 0 {
		t.Fatalf("ty bulk execute did not start a daemon despite a successfully-queued task (stale pid file not replaced).\noutput:\n%s", out)
	}
	if pid == stalePID {
		t.Fatalf("pid file still points at the stale pid %d; ensureDaemonRunning did not replace it with a live daemon.\noutput:\n%s", stalePID, out)
	}
}
