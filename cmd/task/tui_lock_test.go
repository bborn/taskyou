package main

import (
	"path/filepath"
	"testing"
)

// TestAcquireTUILock verifies the singleton guard: a second acquisition is
// refused while the first is held, and succeeds again once it's released. This
// is what prevents two concurrent TUIs from fighting over the executor panes.
func TestAcquireTUILock(t *testing.T) {
	// Point the lock at an isolated temp dir via the DB path it derives from.
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(t.TempDir(), "tasks.db"))

	release1, err := acquireTUILock()
	if err != nil {
		t.Fatalf("first acquire should succeed, got: %v", err)
	}

	if _, err := acquireTUILock(); err == nil {
		t.Fatal("second acquire should fail while the first lock is held")
	}

	release1()

	release2, err := acquireTUILock()
	if err != nil {
		t.Fatalf("acquire after release should succeed, got: %v", err)
	}
	release2()
}
