package ui

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestAcquireExecutorLock verifies the granular guard that replaced the old
// process-wide TUI lock: a second acquisition of the SAME executor is refused
// while the first is held (so a second ty won't steal a live pane), succeeds
// again once released, and never contends across DIFFERENT executors.
func TestAcquireExecutorLock(t *testing.T) {
	// Point the lock dir at an isolated temp dir via the DB path it derives from.
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(t.TempDir(), "tasks.db"))

	release1, err := acquireExecutorLock(7)
	if err != nil {
		t.Fatalf("first acquire should succeed, got: %v", err)
	}

	if _, err := acquireExecutorLock(7); !errors.Is(err, errExecutorBusy) {
		t.Fatalf("second acquire of the same executor should return errExecutorBusy, got: %v", err)
	}

	// A different executor must not contend.
	releaseOther, err := acquireExecutorLock(8)
	if err != nil {
		t.Fatalf("acquiring a different executor should succeed, got: %v", err)
	}
	releaseOther()

	release1()

	release2, err := acquireExecutorLock(7)
	if err != nil {
		t.Fatalf("acquire after release should succeed, got: %v", err)
	}
	release2()
}
