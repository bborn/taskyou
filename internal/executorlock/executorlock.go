// Package executorlock provides a per-task, cross-process "spawn lock" that
// serializes the check-window-then-start-executor critical section shared by the
// daemon executor and every ty TUI detail view.
//
// Without it, the daemon and a detail view can each observe "no tmux window yet"
// and both spawn an executor for the same task — two Claude sessions in one
// worktree with clobbered pane ids (the "executors mixed up" bug).
//
// This is deliberately distinct from the TUI's long-lived executor *ownership*
// lock (internal/ui, acquireExecutorLock), which gates borrowing a live pane for
// the whole life of a detail view. The spawn lock is held only around the spawn
// decision and released immediately, so it never blocks a TUI from later joining
// the daemon's live pane.
package executorlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrSpawnLockTimeout is returned by AcquireSpawn when the lock could not be
// taken before the timeout elapsed (another spawner is holding it).
var ErrSpawnLockTimeout = errors.New("executorlock: timed out waiting for spawn lock")

// spawnPollInterval is how often AcquireSpawn retries the non-blocking flock
// while waiting for a concurrent holder to release.
const spawnPollInterval = 25 * time.Millisecond

// SpawnLockPath returns the lock-file path for a task's spawn lock in lockDir.
// Exported so callers can reason about / clean up the file if needed.
func SpawnLockPath(lockDir string, taskID int64) string {
	return filepath.Join(lockDir, fmt.Sprintf("executor-spawn-%d.lock", taskID))
}

// AcquireSpawn takes an exclusive, cross-process lock serializing executor spawns
// for taskID, blocking up to timeout for any concurrent holder to release. It
// returns a release func that must be called once the spawn decision is made.
//
// lockDir is the directory the lock file lives in — co-locate it with the task
// DB so isolated instances (custom DB path, e.g. QA harnesses) get their own lock
// namespace and don't contend with the real daemon.
//
// The lock is tied to the open file description, so it is released automatically
// if the holding process exits — a crashed spawner never leaves it stuck.
func AcquireSpawn(lockDir string, taskID int64, timeout time.Duration) (func(), error) {
	path := SpawnLockPath(lockDir, taskID)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open spawn lock file: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, ErrSpawnLockTimeout
		}
		time.Sleep(spawnPollInterval)
	}
}
