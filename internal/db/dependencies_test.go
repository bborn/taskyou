package db

import (
	"os"
	"testing"
)

func setupDepsTestDB(t *testing.T) (*DB, func()) {
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := Open(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
	}

	return db, cleanup
}

func TestAddDependency(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create two tasks
	task1 := &Task{Title: "Task 1", Status: StatusBacklog}
	task2 := &Task{Title: "Task 2", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}

	// Add dependency: task2 is blocked by task1
	err := db.AddDependency(task1.ID, task2.ID, false)
	if err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Verify dependency exists
	dep, err := db.GetDependency(task1.ID, task2.ID)
	if err != nil {
		t.Fatalf("Failed to get dependency: %v", err)
	}
	if dep == nil {
		t.Fatal("Expected dependency to exist")
	}
	if dep.BlockerID != task1.ID || dep.BlockedID != task2.ID {
		t.Errorf("Dependency has wrong IDs: blocker=%d, blocked=%d", dep.BlockerID, dep.BlockedID)
	}
}

func TestAddDependencySelfBlocking(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	task := &Task{Title: "Task 1", Status: StatusBacklog}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Try to make a task block itself
	err := db.AddDependency(task.ID, task.ID, false)
	if err == nil {
		t.Error("Expected error when adding self-blocking dependency")
	}
}

func TestAddDependencyCycleDetection(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks
	task1 := &Task{Title: "Task 1", Status: StatusBacklog}
	task2 := &Task{Title: "Task 2", Status: StatusBacklog}
	task3 := &Task{Title: "Task 3", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// Create chain: task1 -> task2 -> task3
	if err := db.AddDependency(task1.ID, task2.ID, false); err != nil {
		t.Fatalf("Failed to add dependency 1->2: %v", err)
	}
	if err := db.AddDependency(task2.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency 2->3: %v", err)
	}

	// Try to create cycle: task3 -> task1 should fail
	err := db.AddDependency(task3.ID, task1.ID, false)
	if err == nil {
		t.Error("Expected error when creating a cycle")
	}
}

func TestRemoveDependency(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create two tasks
	task1 := &Task{Title: "Task 1", Status: StatusBacklog}
	task2 := &Task{Title: "Task 2", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}

	// Add and then remove dependency
	if err := db.AddDependency(task1.ID, task2.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}
	if err := db.RemoveDependency(task1.ID, task2.ID); err != nil {
		t.Fatalf("Failed to remove dependency: %v", err)
	}

	// Verify dependency is gone
	dep, err := db.GetDependency(task1.ID, task2.ID)
	if err != nil {
		t.Fatalf("Failed to get dependency: %v", err)
	}
	if dep != nil {
		t.Error("Expected dependency to be removed")
	}
}

func TestGetBlockers(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks
	task1 := &Task{Title: "Blocker 1", Status: StatusBacklog}
	task2 := &Task{Title: "Blocker 2", Status: StatusBacklog}
	task3 := &Task{Title: "Blocked", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// task3 is blocked by task1 and task2
	if err := db.AddDependency(task1.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}
	if err := db.AddDependency(task2.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Get blockers for task3
	blockers, err := db.GetBlockers(task3.ID)
	if err != nil {
		t.Fatalf("Failed to get blockers: %v", err)
	}
	if len(blockers) != 2 {
		t.Errorf("Expected 2 blockers, got %d", len(blockers))
	}
}

func TestGetBlockedBy(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks
	task1 := &Task{Title: "Blocker", Status: StatusBacklog}
	task2 := &Task{Title: "Blocked 1", Status: StatusBacklog}
	task3 := &Task{Title: "Blocked 2", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// task1 blocks task2 and task3
	if err := db.AddDependency(task1.ID, task2.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}
	if err := db.AddDependency(task1.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Get tasks blocked by task1
	blocked, err := db.GetBlockedBy(task1.ID)
	if err != nil {
		t.Fatalf("Failed to get blocked: %v", err)
	}
	if len(blocked) != 2 {
		t.Errorf("Expected 2 blocked tasks, got %d", len(blocked))
	}
}

func TestGetOpenBlockerCount(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks
	task1 := &Task{Title: "Blocker 1", Status: StatusBacklog}
	task2 := &Task{Title: "Blocker 2", Status: StatusDone} // Already done
	task3 := &Task{Title: "Blocked", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// task3 is blocked by task1 (open) and task2 (done)
	if err := db.AddDependency(task1.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}
	if err := db.AddDependency(task2.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Should only count open blockers
	count, err := db.GetOpenBlockerCount(task3.ID)
	if err != nil {
		t.Fatalf("Failed to get open blocker count: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 open blocker, got %d", count)
	}
}

func TestProcessCompletedBlocker(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create two tasks
	task1 := &Task{Title: "Blocker", Status: StatusProcessing}
	task2 := &Task{Title: "Blocked", Status: StatusBlocked}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}

	// task2 is blocked by task1 with auto-queue enabled
	if err := db.AddDependency(task1.ID, task2.ID, true); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Complete task1 — UpdateTaskStatus runs ProcessCompletedBlocker, which queues task2.
	if err := db.UpdateTaskStatus(task1.ID, StatusDone); err != nil {
		t.Fatalf("Failed to update task1 status: %v", err)
	}

	// task2 was queued (auto-queue was enabled).
	task2Updated, err := db.GetTask(task2.ID)
	if err != nil {
		t.Fatalf("Failed to get task2: %v", err)
	}
	if task2Updated.Status != StatusQueued {
		t.Errorf("Expected task2 status to be queued, got %s", task2Updated.Status)
	}

	// ProcessCompletedBlocker is idempotent: a second call reports no flips because
	// task2 is already queued (only genuine blocked→queued transitions are reported).
	unblocked, err := db.ProcessCompletedBlocker(task1.ID)
	if err != nil {
		t.Fatalf("Failed to process completed blocker: %v", err)
	}
	if len(unblocked) != 0 {
		t.Errorf("second ProcessCompletedBlocker should be a no-op, got %d unblocked", len(unblocked))
	}
}

// TestGateStepCloseReleasesDependents models the human approving a workflow gate.
// A gate step parks 'blocked' after producing its output (a started task in the
// blocked lane, tagged "pipeline,gate"), holding its dependents. When a human
// approves it — `ty close` → UpdateTaskStatus(done) — the standard
// ProcessCompletedBlocker cascade fires and queues the next phase.
func TestGateStepCloseReleasesDependents(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// The gate step already ran and is parked 'blocked' for review.
	gate := &Task{Title: "[design] goal", Status: StatusBlocked, Tags: "pipeline,gate"}
	if err := db.CreateTask(gate); err != nil {
		t.Fatalf("Failed to create gate step: %v", err)
	}
	if err := db.MarkTaskStarted(gate.ID); err != nil {
		t.Fatalf("Failed to mark gate started: %v", err)
	}

	// The next phase waits, blocked on the gate with auto-queue enabled.
	next := &Task{Title: "[build] goal", Status: StatusBlocked, Tags: "pipeline"}
	if err := db.CreateTask(next); err != nil {
		t.Fatalf("Failed to create next step: %v", err)
	}
	if err := db.AddDependency(gate.ID, next.ID, true); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// The human approves the gate.
	if err := db.UpdateTaskStatus(gate.ID, StatusDone); err != nil {
		t.Fatalf("Failed to close gate step: %v", err)
	}

	// The next phase is released (queued) by the cascade.
	nextUpdated, err := db.GetTask(next.ID)
	if err != nil {
		t.Fatalf("Failed to get next step: %v", err)
	}
	if nextUpdated.Status != StatusQueued {
		t.Errorf("Expected next phase to be queued after closing the gate, got %s", nextUpdated.Status)
	}
}

func TestProcessCompletedBlockerWithMultipleBlockers(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create three tasks: two blockers and one blocked
	task1 := &Task{Title: "Blocker 1", Status: StatusProcessing}
	task2 := &Task{Title: "Blocker 2", Status: StatusProcessing}
	task3 := &Task{Title: "Blocked", Status: StatusBlocked}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}
	if err := db.CreateTask(task3); err != nil {
		t.Fatalf("Failed to create task3: %v", err)
	}

	// task3 is blocked by both task1 and task2
	if err := db.AddDependency(task1.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency 1->3: %v", err)
	}
	if err := db.AddDependency(task2.ID, task3.ID, false); err != nil {
		t.Fatalf("Failed to add dependency 2->3: %v", err)
	}

	// Complete only task1
	if err := db.UpdateTaskStatus(task1.ID, StatusDone); err != nil {
		t.Fatalf("Failed to update task1 status: %v", err)
	}

	// task3 should still be blocked (task2 is still open)
	task3Updated, err := db.GetTask(task3.ID)
	if err != nil {
		t.Fatalf("Failed to get task3: %v", err)
	}
	if task3Updated.Status != StatusBlocked {
		t.Errorf("Expected task3 to still be blocked, got %s", task3Updated.Status)
	}

	// Now complete task2
	if err := db.UpdateTaskStatus(task2.ID, StatusDone); err != nil {
		t.Fatalf("Failed to update task2 status: %v", err)
	}

	// task3 should now be unblocked (moved to backlog since auto-queue was false)
	task3Updated, err = db.GetTask(task3.ID)
	if err != nil {
		t.Fatalf("Failed to get task3: %v", err)
	}
	if task3Updated.Status != StatusBacklog {
		t.Errorf("Expected task3 status to be backlog, got %s", task3Updated.Status)
	}
}

func TestIsBlocked(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// Create two tasks
	task1 := &Task{Title: "Blocker", Status: StatusBacklog}
	task2 := &Task{Title: "Blocked", Status: StatusBacklog}
	if err := db.CreateTask(task1); err != nil {
		t.Fatalf("Failed to create task1: %v", err)
	}
	if err := db.CreateTask(task2); err != nil {
		t.Fatalf("Failed to create task2: %v", err)
	}

	// Initially task2 is not blocked
	blocked, err := db.IsBlocked(task2.ID)
	if err != nil {
		t.Fatalf("Failed to check if blocked: %v", err)
	}
	if blocked {
		t.Error("Task should not be blocked initially")
	}

	// Add dependency
	if err := db.AddDependency(task1.ID, task2.ID, false); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Now task2 should be blocked
	blocked, err = db.IsBlocked(task2.ID)
	if err != nil {
		t.Fatalf("Failed to check if blocked: %v", err)
	}
	if !blocked {
		t.Error("Task should be blocked after adding dependency")
	}
}

// TestRequeueReadyTasks covers the safety-net sweep that recovers workflow DAG
// advances when the one-shot ProcessCompletedBlocker flip is dropped.
func TestRequeueReadyTasks(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	// A: ready — blocker already done, dependent blocked+never-started, auto_queue.
	// Created with the blocker pre-done and the dep added after, so nothing flipped
	// the dependent — exactly the "dropped flip" the sweep must recover.
	aBlk := &Task{Title: "A blocker", Status: StatusDone}
	aDep := &Task{Title: "A dep", Status: StatusBlocked}
	mustCreate(t, db, aBlk, aDep)
	if err := db.AddDependency(aBlk.ID, aDep.ID, true); err != nil {
		t.Fatal(err)
	}

	// B: not ready — blocker still open.
	bBlk := &Task{Title: "B blocker", Status: StatusProcessing}
	bDep := &Task{Title: "B dep", Status: StatusBlocked}
	mustCreate(t, db, bBlk, bDep)
	if err := db.AddDependency(bBlk.ID, bDep.ID, true); err != nil {
		t.Fatal(err)
	}

	// C: blocker done but dependent already STARTED (ran, now needs input) — must
	// NOT be re-queued; it's waiting on a human, not on the DAG.
	cBlk := &Task{Title: "C blocker", Status: StatusDone}
	cDep := &Task{Title: "C dep", Status: StatusBlocked}
	mustCreate(t, db, cBlk, cDep)
	if err := db.AddDependency(cBlk.ID, cDep.ID, true); err != nil {
		t.Fatal(err)
	}
	// Mark cDep started (processing sets started_at), then blocked (needs-input).
	if err := db.UpdateTaskStatus(cDep.ID, StatusProcessing); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(cDep.ID, StatusBlocked); err != nil {
		t.Fatal(err)
	}

	moved, err := db.RequeueReadyTasks()
	if err != nil {
		t.Fatalf("RequeueReadyTasks: %v", err)
	}

	if got := statusOfTask(t, db, aDep.ID); got != StatusQueued {
		t.Errorf("A dep = %s, want queued (ready → swept)", got)
	}
	if got := statusOfTask(t, db, bDep.ID); got != StatusBlocked {
		t.Errorf("B dep = %s, want blocked (open blocker)", got)
	}
	if got := statusOfTask(t, db, cDep.ID); got != StatusBlocked {
		t.Errorf("C dep = %s, want blocked (already started → needs input, not swept)", got)
	}
	if len(moved) != 1 || moved[0].ID != aDep.ID {
		t.Errorf("moved = %d tasks, want just the A dep", len(moved))
	}
}

func mustCreate(t *testing.T, db *DB, tasks ...*Task) {
	t.Helper()
	for _, tk := range tasks {
		if err := db.CreateTask(tk); err != nil {
			t.Fatalf("create %q: %v", tk.Title, err)
		}
	}
}

func statusOfTask(t *testing.T, db *DB, id int64) string {
	t.Helper()
	tk, err := db.GetTask(id)
	if err != nil || tk == nil {
		t.Fatalf("get task %d: %v", id, err)
	}
	return tk.Status
}

// TestHasQuestionLog: the needs-input guard for the finished-step sweep.
func TestHasQuestionLog(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()
	tk := &Task{Title: "step", Status: StatusBlocked}
	if err := db.CreateTask(tk); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HasQuestionLog(tk.ID); got {
		t.Error("no question logged yet, want false")
	}
	if err := db.AppendTaskLog(tk.ID, "system", "did some work"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HasQuestionLog(tk.ID); got {
		t.Error("only a system log, want false")
	}
	if err := db.AppendTaskLog(tk.ID, "question", "which approach?"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HasQuestionLog(tk.ID); !got {
		t.Error("a question was logged, want true")
	}
}

// TestBaseCommitAndSessionStartedAt covers the two signals that keep the sweep from
// completing a step that never ran: the worktree's base commit, and whether an executor
// session actually started (worktree setup alone must NOT count as a session).
func TestBaseCommitAndSessionStartedAt(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()
	tk := &Task{Title: "step", Status: StatusProcessing}
	if err := db.CreateTask(tk); err != nil {
		t.Fatal(err)
	}

	if sha, _ := db.GetTaskBaseCommit(tk.ID); sha != "" {
		t.Errorf("base commit before recording = %q, want empty", sha)
	}
	if err := db.SetTaskBaseCommit(tk.ID, "abc123"); err != nil {
		t.Fatal(err)
	}
	if sha, _ := db.GetTaskBaseCommit(tk.ID); sha != "abc123" {
		t.Errorf("base commit = %q, want abc123", sha)
	}

	// A task flips to 'processing' and sets up its worktree long before any session.
	if started, _ := db.HasSessionStarted(tk.ID); started {
		t.Error("no session has started yet")
	}
	if err := db.AppendTaskLog(tk.ID, "system", "Created worktree at /x (branch: pipeline/1-y)"); err != nil {
		t.Fatal(err)
	}
	if started, _ := db.HasSessionStarted(tk.ID); started {
		t.Error("worktree setup is NOT a session start — conflating them let the sweep complete unstarted steps")
	}

	if err := db.AppendTaskLog(tk.ID, "system", "Starting new session (executor: claude)"); err != nil {
		t.Fatal(err)
	}
	if started, _ := db.HasSessionStarted(tk.ID); !started {
		t.Error("session start should be detected once the session begins")
	}
}

func TestHasLogLineContaining(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()
	tk := &Task{Title: "step", Status: StatusBlocked}
	if err := db.CreateTask(tk); err != nil {
		t.Fatal(err)
	}
	marker := "parked for merge review"
	if got, _ := db.HasLogLineContaining(tk.ID, marker); got {
		t.Error("marker not logged yet, want false")
	}
	if err := db.AppendTaskLog(tk.ID, "system", "some unrelated work happened"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HasLogLineContaining(tk.ID, marker); got {
		t.Error("unrelated log only, want false")
	}
	if err := db.AppendTaskLog(tk.ID, "system", "step "+marker+" awaiting human"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HasLogLineContaining(tk.ID, marker); !got {
		t.Error("marker substring is present, want true")
	}
}
