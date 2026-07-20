package completion

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "proj", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return database
}

func mkTask(t *testing.T, database *db.DB, title, status, tags, sourceBranch string) *db.Task {
	t.Helper()
	task := &db.Task{Title: title, Status: status, Project: "proj", Tags: tags, SourceBranch: sourceBranch}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

func statusOf(t *testing.T, database *db.DB, id int64) string {
	t.Helper()
	task, err := database.GetTask(id)
	if err != nil || task == nil {
		t.Fatalf("get task %d: %v", id, err)
	}
	return task.Status
}

// A failing verify command must REJECT the completion and leave the task
// running. This is the backstop against completion-by-assertion: if it ever
// regresses, agents can mark unbuilt work done.
func TestVerifyFailureRejectsCompletion(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "build it", db.StatusProcessing, "", "")
	if err := database.SetStepVerify(task.ID, "exit 1"); err != nil {
		t.Fatalf("set verify: %v", err)
	}

	outcome, err := Complete(database, task.ID, "claimed done", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind != KindVerifyFailed {
		t.Fatalf("kind = %q, want %q", outcome.Kind, KindVerifyFailed)
	}
	// The crucial assertion: status is UNCHANGED, the task keeps running.
	if got := statusOf(t, database, task.ID); got != db.StatusProcessing {
		t.Errorf("status = %q, want it left at %q", got, db.StatusProcessing)
	}
	if outcome.VerifyCommand != "exit 1" {
		t.Errorf("verify command = %q", outcome.VerifyCommand)
	}
}

func TestVerifyPassAllowsCompletion(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "build it", db.StatusProcessing, "", "")
	if err := database.SetStepVerify(task.ID, "true"); err != nil {
		t.Fatalf("set verify: %v", err)
	}

	outcome, err := Complete(database, task.ID, "really done", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind != KindDone {
		t.Fatalf("kind = %q, want %q", outcome.Kind, KindDone)
	}
	if got := statusOf(t, database, task.ID); got != db.StatusDone {
		t.Errorf("status = %q, want done", got)
	}
}

// A human-review gate must PARK, never advance — and critically must not go to
// 'done', because 'done' would release its dependents and skip the human.
func TestGateStepParksAndHoldsDependents(t *testing.T) {
	database := testDB(t)
	branch := "pipeline/1-goal"
	gate := mkTask(t, database, "[plan] goal", db.StatusProcessing, "pipeline,gate", branch)
	next := mkTask(t, database, "[implement] goal", db.StatusBlocked, "pipeline", branch)
	if err := database.AddDependency(gate.ID, next.ID, true); err != nil {
		t.Fatalf("wire dependency: %v", err)
	}

	outcome, err := Complete(database, gate.ID, "planned", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind != KindGateParked {
		t.Fatalf("kind = %q, want %q", outcome.Kind, KindGateParked)
	}
	if got := statusOf(t, database, gate.ID); got != db.StatusBlocked {
		t.Errorf("gate status = %q, want blocked (NOT done — done would release the chain)", got)
	}
	// The dependent must still be held.
	if got := statusOf(t, database, next.ID); got != db.StatusBlocked {
		t.Errorf("dependent status = %q, want it still blocked", got)
	}
}

// A non-terminal, non-gate step must ADVANCE the DAG (go 'done'), even when it
// carries a PR number — the root step owns the shared branch and can match a
// spurious PR, which used to stall workflows at step one.
func TestNonTerminalStepAdvancesDespitePR(t *testing.T) {
	database := testDB(t)
	branch := "pipeline/2-goal"
	root := mkTask(t, database, "[research] goal", db.StatusProcessing, "pipeline", branch)
	root.BranchName = branch
	if err := database.UpdateTask(root); err != nil {
		t.Fatalf("set branch: %v", err)
	}
	if err := database.UpdateTaskPRInfo(root.ID, "https://example.com/pull/7", 7, ""); err != nil {
		t.Fatalf("seed spurious PR: %v", err)
	}
	next := mkTask(t, database, "[design] goal", db.StatusBlocked, "pipeline", branch)
	if err := database.AddDependency(root.ID, next.ID, true); err != nil {
		t.Fatalf("wire dependency: %v", err)
	}

	outcome, err := Complete(database, root.ID, "researched", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind != KindDone {
		t.Fatalf("kind = %q, want %q (must not park on a spurious PR)", outcome.Kind, KindDone)
	}
	if got := statusOf(t, database, root.ID); got != db.StatusDone {
		t.Errorf("status = %q, want done", got)
	}
}

// A terminal task carrying a PR parks for the human merge rather than being
// buried in Done with an open PR.
func TestTerminalTaskWithPRParksForReview(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "ship it", db.StatusProcessing, "", "")
	task.BranchName = "feature/x"
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("set branch: %v", err)
	}
	if err := database.UpdateTaskPRInfo(task.ID, "https://example.com/pull/42", 42, ""); err != nil {
		t.Fatalf("seed PR: %v", err)
	}

	outcome, err := Complete(database, task.ID, "opened a PR", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind != KindPRReview {
		t.Fatalf("kind = %q, want %q", outcome.Kind, KindPRReview)
	}
	if outcome.PRNumber != 42 {
		t.Errorf("pr number = %d, want 42", outcome.PRNumber)
	}
	if got := statusOf(t, database, task.ID); got != db.StatusBlocked {
		t.Errorf("status = %q, want blocked awaiting merge", got)
	}
}

func TestPlainTaskGoesDone(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "move a file", db.StatusProcessing, "", "")

	outcome, err := Complete(database, task.ID, "moved it", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind != KindDone {
		t.Fatalf("kind = %q, want done", outcome.Kind)
	}
	if got := statusOf(t, database, task.ID); got != db.StatusDone {
		t.Errorf("status = %q, want done", got)
	}
}

// The reason this package exists: `ty close` is a plain status write that skips
// every rule above. Completing the SAME gate step through Complete must park it,
// where a raw status write would wrongly mark it done and release the chain.
func TestCompleteDoesNotBypassGateTheWayAStatusWriteDoes(t *testing.T) {
	database := testDB(t)
	branch := "pipeline/3-goal"

	// Control: a raw status write (what `ty close` does) marks the gate done.
	ctlGate := mkTask(t, database, "[plan] goal", db.StatusProcessing, "pipeline,gate", branch)
	ctlNext := mkTask(t, database, "[implement] goal", db.StatusBlocked, "pipeline", branch)
	if err := database.AddDependency(ctlGate.ID, ctlNext.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskStatus(ctlGate.ID, db.StatusDone); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, database, ctlGate.ID); got != db.StatusDone {
		t.Fatalf("control: raw status write should have marked it done, got %q", got)
	}

	// Complete() on an equivalent gate must NOT do that.
	branch2 := "pipeline/4-goal"
	gate := mkTask(t, database, "[plan] goal2", db.StatusProcessing, "pipeline,gate", branch2)
	next := mkTask(t, database, "[implement] goal2", db.StatusBlocked, "pipeline", branch2)
	if err := database.AddDependency(gate.ID, next.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Complete(database, gate.ID, "planned", Options{}); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, database, gate.ID); got == db.StatusDone {
		t.Fatal("Complete() marked a human gate 'done' — it must park 'blocked' so the human still approves")
	}
}

func TestCompleteUnknownTaskErrors(t *testing.T) {
	database := testDB(t)
	if _, err := Complete(database, 999999, "x", Options{}); err == nil {
		t.Fatal("expected an error for an unknown task")
	}
}

// The gate-parked log must be the shared constant so the daemon sweep and the
// board recognise it and leave the step for the human.
func TestGateParkedLogIsWritten(t *testing.T) {
	database := testDB(t)
	branch := "pipeline/5-goal"
	gate := mkTask(t, database, "[plan] goal", db.StatusProcessing, "pipeline,gate", branch)
	next := mkTask(t, database, "[implement] goal", db.StatusBlocked, "pipeline", branch)
	if err := database.AddDependency(gate.ID, next.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Complete(database, gate.ID, "planned", Options{}); err != nil {
		t.Fatal(err)
	}

	logs, err := database.GetTaskLogs(gate.ID, 50)
	if err != nil {
		t.Fatalf("get logs: %v", err)
	}
	var found bool
	for _, l := range logs {
		if l.LineType == "question" && strings.Contains(l.Content, "human review") {
			found = true
		}
	}
	if !found {
		t.Error("expected a 'question' log marking the gate parked for human review")
	}
}
