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
	"crypto/sha256"
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

// ErrRepoLockTimeout is returned by AcquireRepo when the repository lock could
// not be taken before the timeout elapsed.
var ErrRepoLockTimeout = errors.New("executorlock: timed out waiting for repo worktree lock")

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

// RepoLockPath returns the lock-file path serializing worktree creation in one
// repository. The repo path is hashed so the file name is filesystem-safe and
// bounded regardless of how deep the repo lives.
func RepoLockPath(lockDir, repoPath string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(repoPath)))
	return filepath.Join(lockDir, fmt.Sprintf("git-worktree-%x.lock", sum[:8]))
}

// AcquireRepo takes an exclusive, cross-process lock for a repository, blocking
// up to timeout, and returns a release func.
//
// `git worktree add` is not safe to run concurrently in the same repository: it
// writes .git/config (to record the new worktree's upstream) under a lock file
// of git's own, and the loser does not retry — it fails outright with
//
//	error: could not lock config file .git/config: File exists
//
// Nothing serialized this before because nothing could reach it concurrently:
// steps sharing one branch were forced to run one at a time by git itself. Once
// a workflow's parallel steps each got their own branch they spawn together, and
// two `worktree add` calls land in the same repo in the same instant.
func AcquireRepo(lockDir, repoPath string, timeout time.Duration) (func(), error) {
	path := RepoLockPath(lockDir, repoPath)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open repo lock file: %w", err)
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
			return nil, fmt.Errorf("%w: %s", ErrRepoLockTimeout, repoPath)
		}
		time.Sleep(spawnPollInterval)
	}
}
