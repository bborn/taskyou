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

// A passing verify lets completion through. The step has a dependent so the
// completion it reaches is the done-write automation may make.
func TestVerifyPassAllowsCompletion(t *testing.T) {
	database := testDB(t)
	branch := "pipeline/4-goal"
	task := mkTask(t, database, "[build] goal", db.StatusProcessing, "pipeline", branch)
	next := mkTask(t, database, "[ship] goal", db.StatusBlocked, "pipeline", branch)
	if err := database.AddDependency(task.ID, next.ID, true); err != nil {
		t.Fatalf("wire dependency: %v", err)
	}
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

// A plain task with no PR is finished, not done: only a human closes it, so it
// parks in 'blocked' for review.
func TestPlainTaskParksForReview(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "move a file", db.StatusProcessing, "", "")

	outcome, err := Complete(database, task.ID, "moved it", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind != KindReview {
		t.Fatalf("kind = %q, want %q", outcome.Kind, KindReview)
	}
	if got := statusOf(t, database, task.ID); got != db.StatusBlocked {
		t.Errorf("status = %q, want blocked awaiting a human close", got)
	}
}

// The last step of a workflow has nothing waiting on it, so it is the
// workflow's result and a human closes it like any other task.
func TestTerminalWorkflowStepParksForReview(t *testing.T) {
	database := testDB(t)
	branch := "pipeline/5-goal"
	root := mkTask(t, database, "[research] goal", db.StatusProcessing, "pipeline", branch)
	last := mkTask(t, database, "[ship] goal", db.StatusProcessing, "pipeline", branch)
	if err := database.AddDependency(root.ID, last.ID, true); err != nil {
		t.Fatalf("wire dependency: %v", err)
	}

	outcome, err := Complete(database, last.ID, "shipped", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind != KindReview {
		t.Fatalf("kind = %q, want %q", outcome.Kind, KindReview)
	}
	if got := statusOf(t, database, last.ID); got != db.StatusBlocked {
		t.Errorf("status = %q, want blocked", got)
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
	if err := database.SetTaskStatus(ctlGate.ID, db.StatusDone, db.ActorCLI, "test fixture", db.ByHuman("test fixture")); err != nil {
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

// A remotely placed task keeps its branch in remote_branch and its worktree on
// another host, so branch_name and worktree_path are both empty. PR lookup must
// still resolve a branch to ask about and a local checkout to ask in — otherwise
// it reports "no PR" for every placed task and Complete routes finished work to
// done instead of parking it in blocked.
func TestPRLookupTargetUsesRemoteBranchAndProjectDir(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "placed work", db.StatusProcessing, "", "")
	if err := database.SetTaskRemoteWorktree(task.ID,
		"/home/olgm/projects/engineering/.task-worktrees/5316-x", "task/5316-x"); err != nil {
		t.Fatalf("set remote worktree: %v", err)
	}
	task, _ = database.GetTask(task.ID)

	repoDir, branch := prLookupTarget(database, task)
	if branch != "task/5316-x" {
		t.Errorf("branch = %q, want the remote branch task/5316-x", branch)
	}
	proj, _ := database.GetProjectByName("proj")
	if repoDir != proj.Path {
		t.Errorf("repoDir = %q, want the local project checkout %q", repoDir, proj.Path)
	}
}

// A worktree path belonging to another machine must never be handed to git as if
// it were a local checkout.
func TestPRLookupTargetIgnoresANonLocalWorktreePath(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "placed work", db.StatusProcessing, "", "")
	task.WorktreePath = "/home/olgm/definitely/not/on/this/machine"
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("set worktree path: %v", err)
	}
	task, _ = database.GetTask(task.ID)

	repoDir, _ := prLookupTarget(database, task)
	if repoDir == task.WorktreePath {
		t.Fatalf("repoDir = %q, want the local project checkout, not the remote path", repoDir)
	}
	proj, _ := database.GetProjectByName("proj")
	if repoDir != proj.Path {
		t.Errorf("repoDir = %q, want %q", repoDir, proj.Path)
	}
}

// The whole point: a placed task that opened a PR must park in blocked, where it
// is visible, rather than being reported PR-less and buried in done.
func TestPlacedTaskWithPRParksForReview(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "placed ship", db.StatusProcessing, "", "")
	if err := database.SetTaskRemoteWorktree(task.ID, "/home/olgm/wt/5316", "task/5316-x"); err != nil {
		t.Fatalf("set remote worktree: %v", err)
	}
	if err := database.UpdateTaskPRInfo(task.ID, "https://example.com/pull/3598", 3598, ""); err != nil {
		t.Fatalf("seed PR: %v", err)
	}

	outcome, err := Complete(database, task.ID, "opened PR 3598", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind != KindPRReview {
		t.Fatalf("kind = %q, want %q", outcome.Kind, KindPRReview)
	}
	if got := statusOf(t, database, task.ID); got != db.StatusBlocked {
		t.Errorf("status = %q, want blocked so it shows up for review", got)
	}
}

// --- verify gate + remote placement -----------------------------------------
//
// `Complete`'s evidence gate used to resolve the directory a step's `verify:`
// command ran in using only task.WorktreePath with an emptiness check, and fell
// back to the daemon host's local project checkout. For a remotely placed step,
// whose worktree lives on ANOTHER machine and whose local worktree_path is
// empty by design, that ran `verify:` against the WRONG tree — either
// false-rejecting correct pushed work as "Verification failed" or false-passing
// it because the local checkout happened to satisfy the command. The fix mirrors
// prLookupTarget's isDir guard and DEFERS the gate (skips the local run) when a
// remote_worktree_path is on file, rather than evaluating the wrong tree.

// logContains reports whether any task log line contains want.
func logContains(logs []*db.TaskLog, want string) bool {
	for _, l := range logs {
		if strings.Contains(l.Content, want) {
			return true
		}
	}
	return false
}

// TestVerifyDirLocalWorktreeUsed: a local task with a usable worktree runs the
// gate in that worktree — the work was done there, so verifying there is the
// backstop's original behaviour.
func TestVerifyDirLocalWorktreeUsed(t *testing.T) {
	database := testDB(t)
	wtDir := t.TempDir()
	task := mkTask(t, database, "local build", db.StatusProcessing, "", "")
	task.WorktreePath = wtDir
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	task, _ = database.GetTask(task.ID)

	dir, deferRemote := verifyDir(database, task, task.ID)
	if deferRemote {
		t.Errorf("deferRemote = true, want false for a local task with a usable worktree")
	}
	if dir != wtDir {
		t.Errorf("dir = %q, want the task's worktree %q", dir, wtDir)
	}
}

// TestVerifyDirLocalStaleWorktreeFallsBackToProject: a worktree path on the
// local task that is not a directory on THIS machine is unusable (it belongs to
// another machine, or was cleaned up) — same isDir guard prLookupTarget uses.
// For a LOCAL task (no remote_worktree_path) the verify gate then falls back to
// the project checkout, exactly as prLookupTarget already did for git/gh.
func TestVerifyDirLocalStaleWorktreeFallsBackToProject(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "build it", db.StatusProcessing, "", "")
	task.WorktreePath = "/home/olgm/definitely/not/on/this/machine"
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("set worktree path: %v", err)
	}
	task, _ = database.GetTask(task.ID)

	dir, deferRemote := verifyDir(database, task, task.ID)
	if deferRemote {
		t.Errorf("deferRemote = true, want false — a local task with a stale " +
			"worktree path is not a remotely placed step")
	}
	proj, _ := database.GetProjectByName("proj")
	if dir != proj.Path {
		t.Errorf("dir = %q, want the local project checkout %q (stale local worktree is unusable)", dir, proj.Path)
	}
}

// TestVerifyDirRemoteStepDefers: a remotely placed step keeps its worktree on
// another host (worktree_path column empty, remote_worktree_path set). The
// daemon's local project checkout here is a DIFFERENT tree from where the work
// was done — running `verify:` against it would test the wrong tree. The gate is
// deferred (dir="", deferRemote=true) so the caller skips the local run rather
// than evaluating the wrong tree.
func TestVerifyDirRemoteStepDefers(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "placed build", db.StatusProcessing, "", "")
	if err := database.SetTaskRemoteWorktree(task.ID,
		"/home/olgm/wt/5316-not-on-this-machine", "task/5316-x"); err != nil {
		t.Fatalf("set remote worktree: %v", err)
	}
	task, _ = database.GetTask(task.ID)
	if task.WorktreePath != "" {
		t.Fatalf("precondition: want empty local worktree for a remote step, got %q", task.WorktreePath)
	}

	dir, deferRemote := verifyDir(database, task, task.ID)
	if !deferRemote {
		t.Fatalf("deferRemote = false, want true for a remotely placed step")
	}
	if dir != "" {
		t.Errorf("dir = %q, want \"\" (no local run for a remote step)", dir)
	}
	proj, _ := database.GetProjectByName("proj")
	if dir == proj.Path {
		t.Errorf("dir fell back to the daemon's local project checkout %q — that is the WRONG tree for a remote step", proj.Path)
	}
}

// TestRemoteVerifyGateNotRejectingCorrectWork reproduces the false-reject bug
// from the report: a remote step with `verify: "pwd; exit 1"` used to run pwd in
// the daemon's local project checkout and then fail (exit 1),. parking correct,
// pushed remote work as "Verification failed". The fix defers the gate so the
// step is not rejected by a verify run against the wrong tree.
func TestRemoteVerifyGateNotRejectingCorrectWork(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "placed build", db.StatusProcessing, "", "")
	remoteWT := "/home/olgm/wt/5316-not-on-this-machine"
	if err := database.SetTaskRemoteWorktree(task.ID, remoteWT, "task/5316-x"); err != nil {
		t.Fatalf("set remote worktree: %v", err)
	}
	if err := database.SetStepVerify(task.ID, "pwd; exit 1"); err != nil {
		t.Fatalf("set verify: %v", err)
	}

	outcome, err := Complete(database, task.ID, "claimed done", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind == KindVerifyFailed {
		t.Fatalf("kind = %q, want the gate DEFERRED (not rejected against the wrong tree). "+
			"a remote step must not be rejected by a verify run in the local checkout.\noutput: %q",
			outcome.Kind, outcome.VerifyOutput)
	}
	proj, _ := database.GetProjectByName("proj")
	if outcome.VerifyOutput != "" && strings.Contains(outcome.VerifyOutput, proj.Path) {
		t.Errorf("gate ran against proj.Path %q (the WRONG tree) — should have been deferred.\noutput: %q",
			proj.Path, outcome.VerifyOutput)
	}
	if outcome.VerifyOutput != "" && strings.Contains(outcome.VerifyOutput, remoteWT) {
		t.Errorf("gate ran in the remote worktree %q — verify: must run on the remote host, not locally.\noutput: %q",
			remoteWT, outcome.VerifyOutput)
	}
	logs, _ := database.GetTaskLogs(task.ID, 50)
	if !logContains(logs, "Verify gate deferred") {
		t.Errorf("expected a 'Verify gate deferred' log line; got logs: %v", logs)
	}
	if logContains(logs, "Verification failed") {
		t.Errorf("did not expect a 'Verification failed' log — the gate must be deferred, not run against the wrong tree")
	}
}

// TestRemoteVerifyGateNotPassingOnLocalCheckout reproduces the false-pass bug
// from the report: a remote step with `verify: "test -d ."` used to run the
// command in the daemon's existing local checkout and pass, waving a remote
// step through as verified even though the remote worktree (where the agent
// committed) was never tested. The fix defers the gate so the step is never
// waved through on evidence collected from the wrong tree.
func TestRemoteVerifyGateNotPassingOnLocalCheckout(t *testing.T) {
	database := testDB(t)
	task := mkTask(t, database, "placed ship", db.StatusProcessing, "", "")
	if err := database.SetTaskRemoteWorktree(task.ID,
		"/home/olgm/wt/9000-not-on-this-machine", "task/9000-x"); err != nil {
		t.Fatalf("set remote worktree: %v", err)
	}
	// `test -d .` passes in any existing directory (the local checkout) and the
	// OLD bug waved the step through on that. It would have FAILED against the
	// remote path, which does not exist on this host.
	if err := database.SetStepVerify(task.ID, "test -d ."); err != nil {
		t.Fatalf("set verify: %v", err)
	}

	outcome, err := Complete(database, task.ID, "claimed done", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Kind == KindVerifyFailed {
		t.Fatalf("kind = %q, want the gate DEFERRED (not rejected) — the bug is the false-pass, not the false-reject here", outcome.Kind)
	}
	logs, _ := database.GetTaskLogs(task.ID, 50)
	if logContains(logs, "Verification passed") {
		t.Errorf("did not expect a 'Verification passed' log — the gate must be DEFERRED, " +
			"not run (and pass) against the wrong tree")
	}
	if !logContains(logs, "Verify gate deferred") {
		t.Errorf("expected a 'Verify gate deferred' log line so the deferral is visible; " +
			"the remote+verify backstop belongs on the remote done path, not in this checkout")
	}
}

// The existing local-verify tests above (TestVerifyFailureRejectsCompletion
// and TestVerifyPassAllowsCompletion) are the control: they pin the local-only
// behaviour through Complete, so a regression in the local case (e.g. deferring
// when worktree_path is empty AND no remote info is present) shows up. The
// TestVerifyDirLocalWorktreeUsed helper test covers the local-task-with-a-real
// worktree case for the resolver; together they confirm the fix does not
// regress the local happy path.
