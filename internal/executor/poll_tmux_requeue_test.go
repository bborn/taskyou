package executor

import (
	"context"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// TestPollTmuxSessionRequeueVsBacklog covers the fix for issue #674: a parked
// pollTmuxSession must distinguish a deliberate re-queue (a human moving a
// blocked task back to "In Progress", which sets the task to queued) from a
// genuine interrupt/cancel (task set to backlog).
//
//   - queued  -> {Interrupted: true, Requeued: true}  so executeTask's
//     finalization PRESERVES the queued status instead of clobbering it with
//     backlog, and lets the worker spawn a fresh run.
//   - backlog -> {Interrupted: true}                  a genuine interrupt; the
//     finalization writes backlog as before.
//
// Both statuses are checked at the very top of pollTmuxSession's tick, before
// any tmux window probe, so a non-existent session name is fine here — the
// function returns from the DB-status branch on the first tick (~1s).
//
// Note: the executeTask finalization branch itself (which turns a Requeued
// result into "preserve queued + kill session + TriggerProcessing") is not
// unit-tested here because executeTask spawns a real executor backend, sets up
// git worktrees, and runs a live tmux session — none of which can be stubbed
// with the current test helpers. The poll-level contract asserted below is the
// signal that branch keys off of, so verifying it deterministically guards the
// behavior that was broken.
func TestPollTmuxSessionRequeueVsBacklog(t *testing.T) {
	tests := []struct {
		name            string
		status          string
		wantRequeued    bool
		wantInterrupted bool
	}{
		{
			// A requeue is NOT a cancellation: Requeued only, never Interrupted, so
			// the finalizer preserves 'queued' instead of writing backlog.
			name:            "re-queued task signals Requeued (not Interrupted)",
			status:          db.StatusQueued,
			wantRequeued:    true,
			wantInterrupted: false,
		},
		{
			name:            "backlog task is a genuine interrupt without Requeued",
			status:          db.StatusBacklog,
			wantRequeued:    false,
			wantInterrupted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec, database := newTestExecutor(t)

			task := &db.Task{Title: "poll requeue test", Type: "task", Project: "test"}
			if err := database.CreateTask(task); err != nil {
				t.Fatal(err)
			}
			if err := database.UpdateTaskStatus(task.ID, tt.status); err != nil {
				t.Fatal(err)
			}

			// Bound the test so a logic regression that fails to return can't hang
			// the suite. pollTmuxSession returns from the DB-status branch on the
			// first 1s tick; give it generous headroom.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			done := make(chan execResult, 1)
			go func() {
				done <- exec.pollTmuxSession(ctx, task.ID, "nonexistent-session-674")
			}()

			var result execResult
			select {
			case result = <-done:
			case <-ctx.Done():
				t.Fatalf("pollTmuxSession did not return for status %q within timeout", tt.status)
			}

			if result.Requeued != tt.wantRequeued {
				t.Errorf("status %q: Requeued = %v, want %v", tt.status, result.Requeued, tt.wantRequeued)
			}
			if result.Interrupted != tt.wantInterrupted {
				t.Errorf("status %q: Interrupted = %v, want %v", tt.status, result.Interrupted, tt.wantInterrupted)
			}
			// The two signals are mutually exclusive: a requeue must never also be
			// flagged as an interrupt, or the finalizer's else-if could finalize it
			// to backlog and undo the requeue.
			if result.Requeued && result.Interrupted {
				t.Errorf("status %q: both Requeued and Interrupted set", tt.status)
			}
		})
	}
}
