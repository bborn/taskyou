package db

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newStatusTask creates a task for the status tests. Titles read like real
// board items on purpose — a failure message naming "Rotate the Stripe webhook
// secret" tells you more than one naming "test 1".
func newStatusTask(t *testing.T, database *DB, title, status string) *Task {
	t.Helper()
	task := &Task{Title: title, Body: "seeded by status_test", Status: status,
		Type: TypeCode, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return task
}

// markStarted stamps started_at the way the executor does, without going
// through a status transition, so a test can set up "this task genuinely ran"
// independently of the transition it is about to assert on.
func markStarted(t *testing.T, database *DB, id int64) {
	t.Helper()
	if err := database.MarkTaskStarted(id); err != nil {
		t.Fatalf("mark task %d started: %v", id, err)
	}
}

// giveOpenPR attaches an open pull request to a task, as the daemon's PR
// refresh does.
func giveOpenPR(t *testing.T, database *DB, id int64, number int) {
	t.Helper()
	if _, err := database.Exec(
		`UPDATE tasks SET pr_number = ?, pr_url = ?, pr_info_json = ? WHERE id = ?`,
		number, fmt.Sprintf("https://github.com/acme/checkout/pull/%d", number),
		`{"state":"OPEN"}`, id); err != nil {
		t.Fatalf("attach PR to task %d: %v", id, err)
	}
}

// TestSetTaskStatusAppendsTheTransition is the base claim: an accepted
// transition lands in the log with everything needed to explain it later.
func TestSetTaskStatusAppendsTheTransition(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Rotate the Stripe webhook secret", StatusBacklog)
	if err := database.SetTaskStatus(task.ID, StatusQueued, ActorTUI,
		"queued for execution from the board",
		ByHuman("pressed execute on task #%d", task.ID)); err != nil {
		t.Fatalf("SetTaskStatus: %v", err)
	}

	events, err := database.GetStatusEvents(task.ID)
	if err != nil {
		t.Fatalf("GetStatusEvents: %v", err)
	}
	// genesis + the transition
	if len(events) != 2 {
		t.Fatalf("want 2 events (genesis + transition), got %d: %+v", len(events), events)
	}
	last := events[1]
	if last.From != StatusBacklog || last.To != StatusQueued {
		t.Errorf("want backlog→queued, got %s→%s", last.From, last.To)
	}
	if last.Actor != ActorTUI {
		t.Errorf("want actor tui, got %q", last.Actor)
	}
	if last.Reason == "" {
		t.Error("transition recorded no reason")
	}
	if last.Outcome != OutcomeApplied {
		t.Errorf("want outcome applied, got %q", last.Outcome)
	}
	if !strings.Contains(last.Evidence.Human, "pressed execute") {
		t.Errorf("evidence lost the human's action: %+v", last.Evidence)
	}
}

// TestSetTaskStatusRejectsUnnamedCallers proves the signature is not the only
// enforcement: an actor outside the known set, or an empty reason, is refused
// rather than logged as an anonymous change.
func TestSetTaskStatusRejectsUnnamedCallers(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Add idempotency keys to the payouts API", StatusBacklog)

	err := database.SetTaskStatus(task.ID, StatusQueued, Actor("somebody"), "because", NoEvidence)
	if !IsRefused(err) || RefusalGate(err) != GateMissingActor {
		t.Fatalf("want missing-actor refusal, got %v", err)
	}

	err = database.SetTaskStatus(task.ID, StatusQueued, ActorCLI, "   ", NoEvidence)
	if !IsRefused(err) || RefusalGate(err) != GateMissingReason {
		t.Fatalf("want missing-reason refusal, got %v", err)
	}

	err = database.SetTaskStatus(task.ID, "shipped", ActorCLI, "typo", NoEvidence)
	if !IsRefused(err) || RefusalGate(err) != GateUnknownStatus {
		t.Fatalf("want unknown-status refusal, got %v", err)
	}

	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status != StatusBacklog {
		t.Errorf("a refused write moved the task anyway: %q", reloaded.Status)
	}
}

// TestZombieStepCannotBecomeDone is the historic bug, stated as a test: a step
// with no tmux window and no commit — that is, one that never started and
// produced nothing — must not end up done, no matter which automation asks.
func TestZombieStepCannotBecomeDone(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	// A workflow step staged behind its dependencies: created, never run. No
	// window, no worktree, no commit — exactly what the sweep used to read as
	// "finished".
	step := newStatusTask(t, database, "Migrate the sessions table to UUID keys", StatusBlocked)
	if step.StartedAt != nil {
		t.Fatal("precondition: a freshly created step must not look started")
	}

	for _, actor := range []Actor{ActorSweep, ActorDaemon, ActorHook, ActorMCP} {
		err := database.SetTaskStatus(step.ID, StatusDone, actor,
			"no tmux window found for this step",
			Observedf("no tmux window named ty-task-%d", step.ID))
		if !IsRefused(err) {
			t.Fatalf("actor %s completed a step that never started: %v", actor, err)
		}
		if gate := RefusalGate(err); gate != GateNeverStarted {
			t.Errorf("actor %s: want %s gate, got %s", actor, GateNeverStarted, gate)
		}
	}

	// And with no evidence at all it does not even reach that gate.
	err := database.SetTaskStatus(step.ID, StatusDone, ActorSweep, "window is gone", NoEvidence)
	if RefusalGate(err) != GateEvidenceRequired {
		t.Fatalf("want %s, got %v", GateEvidenceRequired, err)
	}

	reloaded, _ := database.GetTask(step.ID)
	if reloaded.Status != StatusBlocked {
		t.Fatalf("the zombie step moved to %q", reloaded.Status)
	}
	if reloaded.CompletedAt != nil {
		t.Fatal("a step that never started was stamped completed_at")
	}
}

// TestHumanMayCloseUnstartedWorkAutomationMayNot draws the line the
// never-started gate exists to draw. A person abandoning a backlog item is a
// decision; a sweep deciding it for them is the bug.
func TestHumanMayCloseUnstartedWorkAutomationMayNot(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Evaluate Redis Cluster for the rate limiter", StatusBacklog)

	if err := database.SetTaskStatus(task.ID, StatusDone, ActorCLI,
		"closed by `ty close`", ByHuman("ran `ty close %d`", task.ID)); err != nil {
		t.Fatalf("a human could not close an unstarted backlog item: %v", err)
	}
	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status != StatusDone {
		t.Fatalf("status is %q, want done", reloaded.Status)
	}
	// The started_at guard: closing work that never ran must not claim it ran.
	if reloaded.CompletedAt != nil {
		t.Error("completed_at was stamped on a task that never started")
	}
}

// TestCompletedAtOnlyOnTasksThatStarted covers the second half of the same
// invariant: a task that really ran does get the stamp.
func TestCompletedAtOnlyOnTasksThatStarted(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	ran := newStatusTask(t, database, "Backfill missing invoice PDFs", StatusBacklog)
	if err := database.SetTaskStatus(ran.ID, StatusProcessing, ActorDaemon,
		"picked up for execution", NoEvidence); err != nil {
		t.Fatalf("start task: %v", err)
	}
	started, _ := database.GetTask(ran.ID)
	if started.StartedAt == nil {
		t.Fatal("moving to processing did not stamp started_at")
	}
	if err := database.SetTaskStatus(ran.ID, StatusDone, ActorMCP,
		"the agent signalled completion",
		Observedf("agent called taskyou_complete with a summary")); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	done, _ := database.GetTask(ran.ID)
	if done.CompletedAt == nil {
		t.Fatal("a task that ran and finished has no completed_at")
	}

	// The blocked case that used to make staged steps look finished.
	staged := newStatusTask(t, database, "Wire the new invoice PDF renderer", StatusBacklog)
	if err := database.SetTaskStatus(staged.ID, StatusBlocked, ActorPipeline,
		"non-root workflow step: staged behind its dependencies", NoEvidence); err != nil {
		t.Fatalf("block staged step: %v", err)
	}
	blocked, _ := database.GetTask(staged.ID)
	if blocked.CompletedAt != nil {
		t.Error("a never-started step parked as blocked was stamped completed_at")
	}
}

// TestOpenPRGateRefusesDoneWrite is the `ty close` incident: a task whose PR is
// still open must not be buried, and the refusal must leave a trace.
func TestOpenPRGateRefusesDoneWrite(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Harden the OAuth callback against replay", StatusBlocked)
	markStarted(t, database, task.ID)
	giveOpenPR(t, database, task.ID, 412)

	err := database.SetTaskStatus(task.ID, StatusDone, ActorCLI,
		"closed by `ty close`", ByHuman("ran `ty close %d`", task.ID))
	if !IsRefused(err) || RefusalGate(err) != GateOpenPR {
		t.Fatalf("want open-pr refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "412") {
		t.Errorf("the refusal does not name the PR that blocked it: %v", err)
	}

	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status != StatusBlocked {
		t.Fatalf("the task was buried anyway: %q", reloaded.Status)
	}

	// The refusal is itself a fact in the log.
	events, err := database.GetStatusEvents(task.ID)
	if err != nil {
		t.Fatalf("GetStatusEvents: %v", err)
	}
	last := events[len(events)-1]
	if last.Outcome != OutcomeRefused || last.Gate != GateOpenPR {
		t.Fatalf("the refusal was not logged: %+v", last)
	}
	if last.Actor != ActorCLI {
		t.Errorf("the log does not say who tried: %q", last.Actor)
	}
	// A refusal changed nothing, so it must not fold.
	if got := FoldStatus(events); got != StatusBlocked {
		t.Errorf("a refused transition folded into the status: %q", got)
	}
}

// TestObservedTerminalPRStateGetsThrough: the only way past the open-PR gate is
// to have looked at the PR and seen it finish — which is what the daemon's
// review reconciler does.
func TestObservedTerminalPRStateGetsThrough(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Ship the checkout retry banner", StatusBlocked)
	markStarted(t, database, task.ID)
	giveOpenPR(t, database, task.ID, 88)

	if err := database.SetTaskStatus(task.ID, StatusDone, ActorDaemon,
		"PR #88 was merged by a human, which finishes this task",
		Evidence{Observed: "gh reports PR #88 in state MERGED", PRNumber: 88, PRState: "MERGED"}); err != nil {
		t.Fatalf("the reconciler could not complete a merged PR's task: %v", err)
	}
	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status != StatusDone {
		t.Fatalf("status is %q, want done", reloaded.Status)
	}
}

// TestDisownedSharedBranchPRGetsThrough: a non-terminal workflow step carries
// the terminal step's PR number because they share a branch. It may complete —
// but only by saying out loud that the PR is not its work, which the log keeps.
func TestDisownedSharedBranchPRGetsThrough(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	step := newStatusTask(t, database, "Extract the pricing rules into a service", StatusProcessing)
	markStarted(t, database, step.ID)
	giveOpenPR(t, database, step.ID, 901)

	ev := Evidence{Observed: "HEAD moved past the recorded base commit and is pushed"}.
		DisownSharedBranchPR(901, "workflow/pricing-extract")
	if err := database.SetTaskStatus(step.ID, StatusDone, ActorSweep,
		"workflow step committed and pushed its work but never signalled done", ev); err != nil {
		t.Fatalf("a disowned shared-branch PR still blocked the step: %v", err)
	}

	events, _ := database.GetStatusEvents(step.ID)
	last := events[len(events)-1]
	if !strings.Contains(last.Evidence.PRDisowned, "shared branch") {
		t.Errorf("the disown claim is not in the log: %+v", last.Evidence)
	}
}

// TestUnknownPRStateCountsAsOpen: the failure mode is burying live work, so an
// unknown PR state must not be a way through the gate.
func TestUnknownPRStateCountsAsOpen(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Cache the pricing table in Redis", StatusBlocked)
	markStarted(t, database, task.ID)
	if _, err := database.Exec(
		`UPDATE tasks SET pr_number = 77, pr_info_json = '' WHERE id = ?`, task.ID); err != nil {
		t.Fatalf("attach PR: %v", err)
	}

	err := database.SetTaskStatus(task.ID, StatusDone, ActorWeb,
		"closed from the web API", ByHuman("POST /api/tasks/%d/close", task.ID))
	if RefusalGate(err) != GateOpenPR {
		t.Fatalf("an unknown PR state was treated as closed: %v", err)
	}
}

// TestFoldRoundTrip: apply a sequence of transitions, assert the fold equals
// the cached row, and that rewinding to event N reproduces the status at N.
func TestFoldRoundTrip(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Replace the nightly export cron with a queue", StatusBacklog)

	type step struct {
		to     string
		actor  Actor
		reason string
		ev     Evidence
	}
	steps := []step{
		{StatusQueued, ActorTUI, "queued from the board", ByHuman("pressed execute")},
		{StatusProcessing, ActorDaemon, "picked up for execution", NoEvidence},
		{StatusBlocked, ActorHook, "the agent asked a question", Observedf("stop reason %q", "needs_input")},
		{StatusProcessing, ActorHook, "the agent is running a tool again", NoEvidence},
		{StatusDone, ActorMCP, "the agent signalled completion", Observedf("agent called taskyou_complete")},
	}

	// The status the task should hold after each step, indexed alongside.
	want := []string{StatusBacklog}
	for _, s := range steps {
		if err := database.SetTaskStatus(task.ID, s.to, s.actor, s.reason, s.ev); err != nil {
			t.Fatalf("transition to %s: %v", s.to, err)
		}
		want = append(want, s.to)
	}

	events, err := database.GetAppliedStatusEvents(task.ID)
	if err != nil {
		t.Fatalf("GetAppliedStatusEvents: %v", err)
	}
	if len(events) != len(want) {
		t.Fatalf("want %d applied events (genesis + %d transitions), got %d",
			len(want), len(steps), len(events))
	}

	reloaded, _ := database.GetTask(task.ID)
	if got := FoldStatus(events); got != reloaded.Status {
		t.Fatalf("fold(events) = %q but the cached row says %q", got, reloaded.Status)
	}

	// Rewind: the fold through event N must reproduce the status as of N.
	for i, e := range events {
		if got := FoldStatusAt(events, e.ID); got != want[i] {
			t.Errorf("rewound to event %d (%s→%s): fold says %q, want %q",
				e.ID, e.From, e.To, got, want[i])
		}
	}

	// And the whole-DB invariant holds.
	mismatches, err := database.CheckStatusConsistency()
	if err != nil {
		t.Fatalf("CheckStatusConsistency: %v", err)
	}
	if len(mismatches) != 0 {
		t.Fatalf("consistency check found mismatches: %v", mismatches)
	}
}

// TestCheckStatusConsistencyCatchesABypass proves the check can actually fail.
// A consistency check that passes no matter what is not a check — so write the
// status column behind SetTaskStatus's back and assert it is caught.
func TestCheckStatusConsistencyCatchesABypass(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Split the billing worker off the web dyno", StatusBacklog)
	if mismatches, _ := database.CheckStatusConsistency(); len(mismatches) != 0 {
		t.Fatalf("a fresh task is already inconsistent: %v", mismatches)
	}

	// Exactly the write this design outlaws.
	if _, err := database.Exec(`UPDATE tasks SET status = 'done' WHERE id = ?`, task.ID); err != nil {
		t.Fatalf("raw update: %v", err)
	}

	mismatches, err := database.CheckStatusConsistency()
	if err != nil {
		t.Fatalf("CheckStatusConsistency: %v", err)
	}
	if len(mismatches) != 1 {
		t.Fatalf("a raw status write went undetected (%d mismatches)", len(mismatches))
	}
	m := mismatches[0]
	if m.TaskID != task.ID || m.Cached != StatusDone || m.Folded != StatusBacklog {
		t.Errorf("mismatch does not describe the bypass: %+v", m)
	}
	if !strings.Contains(m.String(), "row says") {
		t.Errorf("mismatch renders unhelpfully: %s", m.String())
	}
}

// TestUpdateTaskRefusesToChangeStatus closes the second back door: loading a
// task, assigning Status, and saving used to be a status change with no
// transition behind it.
func TestUpdateTaskRefusesToChangeStatus(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Trim the audit log retention to 90 days", StatusBacklog)
	task.Status = StatusDone
	task.Title = "Trim the audit log retention to 90 days"
	err := database.UpdateTask(task)
	if err == nil {
		t.Fatal("UpdateTask silently changed the status")
	}
	if !strings.Contains(err.Error(), "SetTaskStatus") {
		t.Errorf("the error does not point at the one legal path: %v", err)
	}
	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status != StatusBacklog {
		t.Fatalf("status changed anyway: %q", reloaded.Status)
	}
}

// TestNoOpWriteIsNotLogged: hooks re-assert 'processing' on every tool call. An
// audit trail full of processing→processing is an audit trail nobody reads.
func TestNoOpWriteIsNotLogged(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Instrument the checkout funnel", StatusBacklog)
	before, _ := database.GetStatusEvents(task.ID)
	if err := database.SetTaskStatus(task.ID, StatusBacklog, ActorHook,
		"the agent is running a tool", NoEvidence); err != nil {
		t.Fatalf("no-op write: %v", err)
	}
	after, _ := database.GetStatusEvents(task.ID)
	if len(after) != len(before) {
		t.Fatalf("a no-op write appended %d event(s)", len(after)-len(before))
	}
}

// TestTransitionAndLogShareOneTransaction: the row and the log are written
// together, so a failure to append cannot leave a status change unexplained.
// Dropping the log table makes the append fail; the row must not move.
func TestTransitionAndLogShareOneTransaction(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Retire the legacy webhook dispatcher", StatusBacklog)
	if _, err := database.Exec(`DROP TABLE task_status_events`); err != nil {
		t.Fatalf("drop log table: %v", err)
	}
	err := database.SetTaskStatus(task.ID, StatusQueued, ActorCLI,
		"queued by `ty execute`", ByHuman("ran `ty execute`"))
	if err == nil {
		t.Fatal("the transition reported success with no log to write to")
	}
	reloaded, _ := database.GetTask(task.ID)
	if reloaded.Status != StatusBacklog {
		t.Fatalf("the row moved to %q even though the append failed — they are not in one transaction",
			reloaded.Status)
	}
}

// TestBackfillGivesPreLogTasksAGenesisEvent: opening a database that predates
// the status log must leave the consistency check passing, without inventing
// or reordering history.
func TestBackfillGivesPreLogTasksAGenesisEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Build a "legacy" database: tasks whose rows exist with no status log at
	// all, the shape every real installation is in before this migration.
	legacy := []struct {
		title  string
		status string
	}{
		{"Move image thumbnails to the CDN", StatusDone},
		{"Add SAML login for enterprise accounts", StatusBlocked},
		{"Deduplicate the nightly usage rollup", StatusBacklog},
	}
	var ids []int64
	for _, l := range legacy {
		task := newStatusTask(t, database, l.title, StatusBacklog)
		ids = append(ids, task.ID)
		if _, err := database.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, l.status, task.ID); err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
	}
	// Erase the log entirely and the backfill marker, so reopening looks exactly
	// like the first upgrade of an old database.
	if _, err := database.Exec(`DELETE FROM task_status_events`); err != nil {
		t.Fatalf("clear log: %v", err)
	}
	if err := database.SetSetting(statusEventBackfillKey, ""); err != nil {
		t.Fatalf("clear backfill marker: %v", err)
	}
	database.Close()

	// Reopen: migrate() runs the backfill.
	database, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer database.Close()

	mismatches, err := database.CheckStatusConsistency()
	if err != nil {
		t.Fatalf("CheckStatusConsistency: %v", err)
	}
	if len(mismatches) != 0 {
		t.Fatalf("a backfilled database is inconsistent: %v", mismatches)
	}

	for i, id := range ids {
		events, err := database.GetStatusEvents(id)
		if err != nil {
			t.Fatalf("events for %d: %v", id, err)
		}
		if len(events) != 1 {
			t.Fatalf("task %d got %d genesis events, want 1", id, len(events))
		}
		g := events[0]
		if g.Actor != ActorMigration {
			t.Errorf("task %d: genesis actor is %q, want migration", id, g.Actor)
		}
		if g.To != legacy[i].status {
			t.Errorf("task %d: genesis records %q, row says %q", id, g.To, legacy[i].status)
		}
		if g.From != "" {
			t.Errorf("task %d: genesis invented a from-status %q", id, g.From)
		}
		if !strings.Contains(g.Evidence.Observed, "no transition history") {
			t.Errorf("task %d: genesis evidence claims more than it knows: %q", id, g.Evidence.Observed)
		}
	}

	// Idempotent: reopening again must not append a second genesis event.
	database.Close()
	database, err = Open(path)
	if err != nil {
		t.Fatalf("third open: %v", err)
	}
	events, _ := database.GetStatusEvents(ids[0])
	if len(events) != 1 {
		t.Fatalf("re-running the backfill duplicated history: %d events", len(events))
	}
}

// TestBackfillPreservesOrderAndExistingHistory: the migration only ever
// inserts, and only for tasks with no events, so history it finds is untouched
// and the events it adds sort by when the work actually happened.
func TestBackfillPreservesOrderAndExistingHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.db")

	database, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// One task with real history, two without — and the two without finished at
	// known, different times.
	withHistory := newStatusTask(t, database, "Roll the API gateway to Envoy", StatusBacklog)
	if err := database.SetTaskStatus(withHistory.ID, StatusQueued, ActorCLI,
		"queued by `ty execute`", ByHuman("ran `ty execute`")); err != nil {
		t.Fatalf("transition: %v", err)
	}
	existing, _ := database.GetStatusEvents(withHistory.ID)

	old := newStatusTask(t, database, "Decommission the staging Postgres replica", StatusBacklog)
	recent := newStatusTask(t, database, "Alert on p99 checkout latency", StatusBacklog)
	if _, err := database.Exec(
		`UPDATE tasks SET status='done', started_at=?, completed_at=? WHERE id=?`,
		"2024-03-01 09:00:00", "2024-03-01 10:00:00", old.ID); err != nil {
		t.Fatalf("age the old task: %v", err)
	}
	if _, err := database.Exec(
		`UPDATE tasks SET status='done', started_at=?, completed_at=? WHERE id=?`,
		"2025-11-02 09:00:00", "2025-11-02 10:00:00", recent.ID); err != nil {
		t.Fatalf("age the recent task: %v", err)
	}
	// Strip only the pre-log tasks' events, leaving withHistory's alone.
	if _, err := database.Exec(`DELETE FROM task_status_events WHERE task_id IN (?, ?)`,
		old.ID, recent.ID); err != nil {
		t.Fatalf("clear log for pre-log tasks: %v", err)
	}
	if err := database.SetSetting(statusEventBackfillKey, ""); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	database.Close()

	database, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer database.Close()

	// Existing history is byte-for-byte what it was: not reordered, not rewritten.
	after, _ := database.GetStatusEvents(withHistory.ID)
	if len(after) != len(existing) {
		t.Fatalf("the backfill touched a task that already had history: %d → %d events",
			len(existing), len(after))
	}
	for i := range after {
		if after[i].ID != existing[i].ID || after[i].To != existing[i].To || after[i].Actor != existing[i].Actor {
			t.Fatalf("event %d changed: %+v → %+v", i, existing[i], after[i])
		}
	}

	// The backfilled events carry the task's own clock, so the log sorts in the
	// order the work happened rather than all at once at migration time.
	oldEvents, _ := database.GetStatusEvents(old.ID)
	recentEvents, _ := database.GetStatusEvents(recent.ID)
	if len(oldEvents) != 1 || len(recentEvents) != 1 {
		t.Fatalf("want one genesis event each, got %d and %d", len(oldEvents), len(recentEvents))
	}
	if !oldEvents[0].CreatedAt.Before(recentEvents[0].CreatedAt.Time) {
		t.Errorf("backfilled history is out of order: %v is not before %v",
			oldEvents[0].CreatedAt, recentEvents[0].CreatedAt)
	}
	if got := oldEvents[0].CreatedAt.Year(); got != 2024 {
		t.Errorf("backfill stamped the migration's clock (%d) rather than the task's", got)
	}
}

// TestSetTaskStatusIsSerializedUnderConcurrency: the gates read the task and
// then write it. If two callers interleave between the read and the write, a
// task can move twice from one starting point and the log stops explaining the
// row. Every transition must be observed by the one that follows it.
//
// The delay is the point: without a real gap between read and write, an
// instantaneous fake would pass this even with the serialization removed.
func TestSetTaskStatusIsSerializedUnderConcurrency(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Shard the events table by month", StatusBacklog)

	const rounds = 12
	errs := make(chan error, rounds)
	start := make(chan struct{})
	for i := 0; i < rounds; i++ {
		go func(i int) {
			<-start
			// A real gap, so an unserialized implementation genuinely interleaves.
			time.Sleep(time.Duration(i%4) * 3 * time.Millisecond)
			to := StatusQueued
			if i%2 == 1 {
				to = StatusBacklog
			}
			errs <- database.SetTaskStatus(task.ID, to, ActorDaemon,
				fmt.Sprintf("concurrent writer %d", i), NoEvidence)
		}(i)
	}
	close(start)
	for i := 0; i < rounds; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent transition failed: %v", err)
		}
	}

	events, err := database.GetAppliedStatusEvents(task.ID)
	if err != nil {
		t.Fatalf("GetAppliedStatusEvents: %v", err)
	}
	// Every applied event's from must equal the previous event's to. A torn
	// read/write shows up here as a gap in the chain.
	for i := 1; i < len(events); i++ {
		if events[i].From != events[i-1].To {
			t.Fatalf("event %d claims to start at %q but the previous event ended at %q — the log has a gap",
				events[i].ID, events[i].From, events[i-1].To)
		}
	}
	reloaded, _ := database.GetTask(task.ID)
	if got := FoldStatus(events); got != reloaded.Status {
		t.Fatalf("after concurrent writes the fold says %q and the row says %q", got, reloaded.Status)
	}
}

// TestEvidenceSurvivesTheRoundTrip: evidence is only useful if it comes back
// out intact months later.
func TestEvidenceSurvivesTheRoundTrip(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	task := newStatusTask(t, database, "Verify the SOC2 evidence collector", StatusProcessing)
	markStarted(t, database, task.ID)

	ev := Evidence{
		Observed:   "HEAD is 3 commits past base and pushed",
		BaseCommit: "8f2c19aa5b4e",
		HeadCommit: "d41d8cd98f00",
		PRNumber:   204,
		PRState:    "MERGED",
		Gate:       "go test ./internal/audit/",
	}
	if err := database.SetTaskStatus(task.ID, StatusDone, ActorSweep,
		"work committed, pushed and verified", ev); err != nil {
		t.Fatalf("SetTaskStatus: %v", err)
	}
	events, _ := database.GetStatusEvents(task.ID)
	got := events[len(events)-1].Evidence
	if got != ev {
		t.Fatalf("evidence did not round-trip:\n got %+v\nwant %+v", got, ev)
	}
	rendered := got.String()
	for _, want := range []string{"8f2c19aa", "d41d8cd9", "PR #204 MERGED", "go test ./internal/audit/"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered evidence %q is missing %q", rendered, want)
		}
	}
	// And it is stored as JSON, not as prose the next reader has to parse.
	var raw string
	if err := database.QueryRow(
		`SELECT evidence FROM task_status_events WHERE task_id = ? ORDER BY id DESC LIMIT 1`,
		task.ID).Scan(&raw); err != nil {
		t.Fatalf("read raw evidence: %v", err)
	}
	var decoded Evidence
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("stored evidence is not JSON: %v (%q)", err, raw)
	}
}

// TestReleasingDependentsIsItselfLogged: when a blocker finishes, its
// dependents move — and that move is a transition like any other, attributed
// to the system rather than appearing from nowhere.
func TestReleasingDependentsIsItselfLogged(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	blocker := newStatusTask(t, database, "Publish the new pricing schema", StatusProcessing)
	markStarted(t, database, blocker.ID)
	dependent := newStatusTask(t, database, "Migrate the billing UI to the new schema", StatusBlocked)
	if err := database.AddDependency(blocker.ID, dependent.ID, false); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	if err := database.SetTaskStatus(blocker.ID, StatusDone, ActorMCP,
		"the agent signalled completion", Observedf("agent called taskyou_complete")); err != nil {
		t.Fatalf("complete blocker: %v", err)
	}

	events, err := database.GetStatusEvents(dependent.ID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	last := events[len(events)-1]
	if last.Actor != ActorSystem {
		t.Fatalf("the dependent moved without attribution: %+v", last)
	}
	if !strings.Contains(last.Reason, "blocker") {
		t.Errorf("the reason does not explain the release: %q", last.Reason)
	}
	reloaded, _ := database.GetTask(dependent.ID)
	if got := FoldStatus(events); got != reloaded.Status {
		t.Fatalf("fold %q disagrees with the row %q after a cascade", got, reloaded.Status)
	}
}
