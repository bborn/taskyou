package executorlock

import (
	"testing"
	"time"
)

// TestAcquireSpawn_SerializesSameTask verifies that a second spawn-lock
// acquisition for the same task blocks while the first is held (so a TUI and the
// daemon cannot both spawn an executor at once), and succeeds once released.
func TestAcquireSpawn_SerializesSameTask(t *testing.T) {
	dir := t.TempDir()

	rel1, err := AcquireSpawn(dir, 4847, time.Second)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// A second acquirer must NOT get the lock while the first holds it.
	if rel, err := AcquireSpawn(dir, 4847, 50*time.Millisecond); err == nil {
		rel()
		t.Fatal("second acquire succeeded while first still held the lock")
	}

	rel1()

	// Once released, a new acquirer succeeds.
	rel2, err := AcquireSpawn(dir, 4847, time.Second)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	rel2()
}

// TestAcquireSpawn_DifferentTasksDoNotContend verifies the lock is per-task:
// spawning executors for different tasks concurrently is allowed.
func TestAcquireSpawn_DifferentTasksDoNotContend(t *testing.T) {
	dir := t.TempDir()

	r1, err := AcquireSpawn(dir, 1, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire task 1: %v", err)
	}
	defer r1()

	r2, err := AcquireSpawn(dir, 2, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire task 2 should not contend with task 1: %v", err)
	}
	r2()
}

// TestAcquireSpawn_WaitsThenSucceeds verifies a blocked acquirer succeeds once
// the holder releases within the timeout window.
func TestAcquireSpawn_WaitsThenSucceeds(t *testing.T) {
	dir := t.TempDir()

	rel1, err := AcquireSpawn(dir, 7, time.Second)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	released := make(chan struct{})
	go func() {
		time.Sleep(80 * time.Millisecond)
		rel1()
		close(released)
	}()

	// Generous timeout: should block ~80ms then succeed after release.
	rel2, err := AcquireSpawn(dir, 7, time.Second)
	if err != nil {
		t.Fatalf("second acquire should succeed after holder releases: %v", err)
	}
	<-released
	rel2()
}
