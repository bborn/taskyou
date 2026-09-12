package executor

import (
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func localTime(t time.Time) *db.LocalTime {
	lt := db.LocalTime{Time: t}
	return &lt
}

// TestBlockedIdleDurationIgnoresUnrelatedRowWrites is the regression test for the
// bug that made idle-suspend never fire: the sweep measured idleness from
// updated_at, but updated_at is bumped by any write to the row — PR info
// refreshes, log appends, pane-ID updates — all of which the daemon performs on
// its own schedule. A task parked for days therefore looked freshly active.
//
// completed_at is stamped only when a started task transitions to blocked, so it
// is the real "parked at" clock.
func TestBlockedIdleDurationIgnoresUnrelatedRowWrites(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	parkedAt := now.Add(-30 * time.Hour)

	task := &db.Task{
		ID:          1,
		Status:      db.StatusBlocked,
		CompletedAt: localTime(parkedAt),
		// The daemon refreshed PR info a second ago, bumping updated_at.
		UpdatedAt: db.LocalTime{Time: now.Add(-time.Second)},
	}

	idle, ok := blockedIdleDuration(task, now)
	if !ok {
		t.Fatal("blockedIdleDuration: ok = false, want true for a parked task")
	}
	if idle != 30*time.Hour {
		t.Errorf("idle = %v, want 30h measured from completed_at, not updated_at", idle)
	}
}

// TestBlockedIdleDurationSkipsNeverStartedSteps guards the other half of the
// status: a pipeline step staged behind its dependencies is also "blocked" but
// never ran, so it has no completed_at and no agent process to reclaim.
func TestBlockedIdleDurationSkipsNeverStartedSteps(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	task := &db.Task{
		ID:        2,
		Status:    db.StatusBlocked,
		UpdatedAt: db.LocalTime{Time: now.Add(-90 * time.Hour)},
	}

	if _, ok := blockedIdleDuration(task, now); ok {
		t.Error("ok = true for a never-started step, want false")
	}
}

// TestEligibleForIdleSuspendUsesParkedClock is the end-to-end selection test for
// the sweep: of a batch of blocked tasks, only those genuinely parked past the
// threshold are returned. The task that a daemon write touched a second ago must
// still be selected, because that write says nothing about agent activity.
func TestEligibleForIdleSuspendUsesParkedClock(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	timeout := 6 * time.Hour

	parkedLongAgo := &db.Task{
		ID:          1,
		Status:      db.StatusBlocked,
		CompletedAt: localTime(now.Add(-30 * time.Hour)),
		UpdatedAt:   db.LocalTime{Time: now.Add(-time.Second)}, // daemon just refreshed PR info
	}
	parkedRecently := &db.Task{
		ID:          2,
		Status:      db.StatusBlocked,
		CompletedAt: localTime(now.Add(-10 * time.Minute)),
		UpdatedAt:   db.LocalTime{Time: now.Add(-10 * time.Minute)},
	}
	neverStartedStep := &db.Task{
		ID:        3,
		Status:    db.StatusBlocked,
		UpdatedAt: db.LocalTime{Time: now.Add(-90 * time.Hour)},
	}

	got := eligibleForIdleSuspend(
		[]*db.Task{parkedLongAgo, parkedRecently, neverStartedStep},
		now, timeout,
	)

	if len(got) != 1 {
		t.Fatalf("selected %d tasks, want 1 (got %v)", len(got), taskIDs(got))
	}
	if got[0].ID != 1 {
		t.Errorf("selected task %d, want task 1", got[0].ID)
	}
}

// TestEligibleForIdleSuspendRespectsThreshold pins the boundary so a longer
// configured timeout actually defers suspension.
func TestEligibleForIdleSuspendRespectsThreshold(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	task := &db.Task{
		ID:          1,
		Status:      db.StatusBlocked,
		CompletedAt: localTime(now.Add(-8 * time.Hour)),
	}

	if got := eligibleForIdleSuspend([]*db.Task{task}, now, 6*time.Hour); len(got) != 1 {
		t.Errorf("6h timeout: selected %v, want task 1", taskIDs(got))
	}
	if got := eligibleForIdleSuspend([]*db.Task{task}, now, 24*time.Hour); len(got) != 0 {
		t.Errorf("24h timeout: selected %v, want none", taskIDs(got))
	}
}

// TestIdleSuspendListOptionsAreUnlimited guards a sharp edge in ListTasks: the
// default limit is 100, and blocked tasks come back most-recently-parked first.
// A user with more than 100 blocked tasks would therefore have exactly the
// longest-parked ones — the whole target of the sweep — fall off the end of the
// page and never be examined.
func TestIdleSuspendListOptionsAreUnlimited(t *testing.T) {
	opts := idleSuspendListOptions()

	if opts.Status != db.StatusBlocked {
		t.Errorf("Status = %q, want %q", opts.Status, db.StatusBlocked)
	}
	if opts.Limit != -1 {
		t.Errorf("Limit = %d, want -1 (unlimited); 0 and 100 both cap the page at 100", opts.Limit)
	}
}

func taskIDs(tasks []*db.Task) []int64 {
	ids := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

// TestTaskWindowTargetsFindsWindowAcrossDaemonGenerations covers the reason the
// CLI needed killSessionAcrossDaemons: a restarted daemon leaves its old
// task-daemon-<pid> session holding the live task windows, so the current
// daemon's session name cannot be assumed.
func TestTaskWindowTargetsFindsWindowAcrossDaemonGenerations(t *testing.T) {
	sessions := "ikgm\ntask-daemon-45180\ntask-daemon-82899\ntask-ui-90417\nwhiny\n"

	got := taskWindowTargets(sessions, 5141)

	want := []string{"task-daemon-45180:task-5141", "task-daemon-82899:task-5141"}
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("targets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestTaskWindowTargetsIgnoresNonDaemonSessions keeps the sweep from reaching
// into the user's own tmux sessions.
func TestTaskWindowTargetsIgnoresNonDaemonSessions(t *testing.T) {
	sessions := "main\nolgm\ntask-ui-90417\n"

	if got := taskWindowTargets(sessions, 5141); len(got) != 0 {
		t.Errorf("targets = %v, want none", got)
	}
}
