package executor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// sharedBranchRepo builds a real git repo with one commit on a shared pipeline
// branch. These tests are about what git actually permits (one worktree per
// branch), so a fake would prove nothing.
func sharedBranchRepo(t *testing.T) (repo, branch string) {
	t.Helper()
	repo = t.TempDir()
	branch = "pipeline/1-demo"
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "base")
	run("branch", branch)
	return repo, branch
}

func sharedBranchExecutor(t *testing.T, repo string) (*Executor, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "p", Path: repo}); err != nil {
		t.Fatal(err)
	}
	return New(database, &config.Config{}), database
}

// holdBranch gives an existing task a worktree attached to branch, the way a
// step that has run leaves things behind.
func holdBranch(t *testing.T, e *Executor, database *db.DB, repo, branch, status string) *db.Task {
	t.Helper()
	holderPath := filepath.Join(t.TempDir(), "holder")
	cmd := exec.Command("git", "worktree", "add", holderPath, branch)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed holder worktree: %v\n%s", err, out)
	}
	task := &db.Task{Title: "[Plan] x", Status: status, Project: "p", Tags: "pipeline", BranchName: branch}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	task.WorktreePath = holderPath
	task.Status = status
	if err := database.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskStatus(task.ID, status, db.ActorCLI, "test fixture", db.ByHuman("test fixture")); err != nil {
		t.Fatal(err)
	}
	return task
}

// The bug this whole change exists for: a pipeline's first step finishes, its
// worktree keeps the shared branch checked out, and the NEXT step can never
// attach — "fatal: '<branch>' is already checked out at ...". A finished holder
// must hand the branch over.
func TestSourceBranchWorktreeReclaimsBranchFromFinishedStep(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holder := holdBranch(t, e, database, repo, branch, db.StatusDone)

	next := filepath.Join(t.TempDir(), "next")
	if err := e.addSourceBranchWorktree(repo, next, branch); err != nil {
		t.Fatalf("next step could not take the shared branch: %v", err)
	}

	got, err := gitCurrentBranch(next)
	if err != nil {
		t.Fatal(err)
	}
	if got != branch {
		t.Fatalf("next step worktree is on %q, want it attached to %q", got, branch)
	}
	// The finished step keeps its files; only its HEAD moved off the branch.
	if _, err := os.Stat(filepath.Join(holder.WorktreePath, "README.md")); err != nil {
		t.Fatalf("finished step's worktree should stay readable: %v", err)
	}
	if head, err := gitCurrentBranch(holder.WorktreePath); err != nil || head != "HEAD" {
		t.Fatalf("holder should be detached, got %q (err %v)", head, err)
	}
}

// A holder that is still RUNNING keeps the branch, and the waiting step must be
// told to come back later rather than being failed. A hard failure here is what
// stamped started_at/completed_at on a step that never launched an agent.
func TestSourceBranchWorktreeDefersWhileHolderIsRunning(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holdBranch(t, e, database, repo, branch, db.StatusProcessing)

	err := e.addSourceBranchWorktree(repo, filepath.Join(t.TempDir(), "next"), branch)
	if !errors.Is(err, ErrBranchBusy) {
		t.Fatalf("want ErrBranchBusy while the holder runs, got %v", err)
	}
}

// Fan-out: every sibling gets a worktree on its OWN branch, cut from the shared
// branch, and none of them contends with the others or with the root's worktree.
func TestStepBranchWorktreesDoNotContend(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holdBranch(t, e, database, repo, branch, db.StatusProcessing) // root still running

	for _, name := range []string{"planreviewa", "planreviewb", "planreviewc"} {
		stepBranch := branch + "-" + name
		path := filepath.Join(t.TempDir(), name)
		if err := e.addStepBranchWorktree(repo, path, stepBranch, branch); err != nil {
			t.Fatalf("%s could not get its own branch: %v", name, err)
		}
		got, err := gitCurrentBranch(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != stepBranch {
			t.Fatalf("%s is on %q, want %q", name, got, stepBranch)
		}
	}
}

// A retried fan-out step reuses its own branch, keeping whatever it committed
// the first time rather than re-cutting from the shared branch.
func TestStepBranchWorktreeReusesExistingBranchOnRetry(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, _ := sharedBranchExecutor(t, repo)
	stepBranch := branch + "-planreviewa"

	first := filepath.Join(t.TempDir(), "first")
	if err := e.addStepBranchWorktree(repo, first, stepBranch, branch); err != nil {
		t.Fatal(err)
	}
	// The step commits, then its worktree goes away (crash, cleanup, retry).
	writeAndCommit(t, first, "REVIEW.md", "findings")
	want := gitHeadCommit(first)
	rm := exec.Command("git", "worktree", "remove", "--force", first)
	rm.Dir = repo
	if out, err := rm.CombinedOutput(); err != nil {
		t.Fatalf("remove worktree: %v\n%s", err, out)
	}

	second := filepath.Join(t.TempDir(), "second")
	if err := e.addStepBranchWorktree(repo, second, stepBranch, branch); err != nil {
		t.Fatalf("retry could not reattach: %v", err)
	}
	if got := gitHeadCommit(second); got != want {
		t.Fatalf("retry started at %s, want the step's own commit %s", got, want)
	}
}

func writeAndCommit(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", name}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// Fan-out siblings spawn in the same instant, and `git worktree add` is not safe
// to run concurrently in one repository: it writes .git/config under a lock of
// git's own and the loser fails outright with "could not lock config file
// .git/config: File exists". Creating every sibling's worktree at once must
// still produce every worktree.
func TestConcurrentStepBranchWorktreesAllSucceed(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, _ := sharedBranchExecutor(t, repo)

	const siblings = 6
	base := t.TempDir()
	errs := make(chan error, siblings)
	start := make(chan struct{})
	for i := 0; i < siblings; i++ {
		go func(i int) {
			<-start // release them together
			name := fmt.Sprintf("review%d", i)
			errs <- e.addStepBranchWorktree(repo, filepath.Join(base, name), branch+"-"+name, branch)
		}(i)
	}
	close(start)
	for i := 0; i < siblings; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent worktree add failed: %v", err)
		}
	}

	for i := 0; i < siblings; i++ {
		name := fmt.Sprintf("review%d", i)
		got, err := gitCurrentBranch(filepath.Join(base, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := branch + "-" + name; got != want {
			t.Errorf("%s is on %q, want %q", name, got, want)
		}
	}
}

// A fan-out step's branch must NOT track the shared branch: `worktree add -b X
// <path> origin/<shared>` sets that upstream by default, so a later bare
// `git push` would land the step's commits on the branch its instructions
// explicitly tell it not to touch.
func TestStepBranchDoesNotTrackTheSharedBranch(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, _ := sharedBranchExecutor(t, repo)

	// Give the repo an "origin" so the remote-ref path is the one exercised.
	remote := t.TempDir()
	mustGit(t, remote, "init", "--bare")
	mustGit(t, repo, "remote", "add", "origin", remote)
	mustGit(t, repo, "push", "origin", branch)
	mustGit(t, repo, "fetch", "origin")

	path := filepath.Join(t.TempDir(), "reviewa")
	stepBranch := branch + "-reviewa"
	if err := e.addStepBranchWorktree(repo, path, stepBranch, branch); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("git", "-C", repo, "config", "--get", "branch."+stepBranch+".merge").Output()
	if upstream := strings.TrimSpace(string(out)); err == nil && upstream != "" {
		t.Errorf("%s tracks %q; a bare `git push` would clobber the shared branch", stepBranch, upstream)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// The stall this fix exists for: the holder is DONE, but its executor tmux
// window was never reaped. taskIsLive used to read that leftover window as
// "still working", so the branch was never reclaimed and every downstream step
// re-queued itself on ErrBranchBusy forever. A terminal status wins over tmux.
func TestSourceBranchWorktreeReclaimsFromDoneHolderWithStaleTmuxWindow(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holder := holdBranch(t, e, database, repo, branch, db.StatusDone)

	// The window outlives the agent: the pane falls back to a shell and nothing
	// reaps the window.
	e.windowExistsFn = func(taskID int64) bool { return taskID == holder.ID }

	next := filepath.Join(t.TempDir(), "next")
	if err := e.addSourceBranchWorktree(repo, next, branch); err != nil {
		t.Fatalf("a done holder's stale tmux window still pins the branch: %v", err)
	}
	got, err := gitCurrentBranch(next)
	if err != nil {
		t.Fatal(err)
	}
	if got != branch {
		t.Fatalf("next step worktree is on %q, want %q", got, branch)
	}
}

// The correction must not go too far: a step that has not reached a terminal
// status may still have a live agent behind that window (a 'blocked' step
// sitting on a question), and its branch stays put.
func TestSourceBranchWorktreeKeepsBranchForBlockedHolderWithLiveWindow(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holder := holdBranch(t, e, database, repo, branch, db.StatusBlocked)
	e.windowExistsFn = func(taskID int64) bool { return taskID == holder.ID }

	err := e.addSourceBranchWorktree(repo, filepath.Join(t.TempDir(), "next"), branch)
	if !errors.Is(err, ErrBranchBusy) {
		t.Fatalf("want ErrBranchBusy while a blocked holder still owns its window, got %v", err)
	}
}

// A blocked holder whose window is gone is finished for our purposes — nothing
// is going to come back and use that worktree.
func TestSourceBranchWorktreeReclaimsFromBlockedHolderWithNoWindow(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holdBranch(t, e, database, repo, branch, db.StatusBlocked)
	e.windowExistsFn = func(int64) bool { return false }

	if err := e.addSourceBranchWorktree(repo, filepath.Join(t.TempDir(), "next"), branch); err != nil {
		t.Fatalf("abandoned blocked holder should release the branch: %v", err)
	}
}

// Re-queueing a contended step every 2s tick is what flushed a task's whole log
// history out of the ring buffer. Deferrals must back off, and the gate must
// actually hold the step back between attempts.
func TestBranchWaitBacksOff(t *testing.T) {
	e := &Executor{}

	first, keepWaiting := e.deferForBusyBranch(7)
	if !keepWaiting {
		t.Fatal("first deferral should keep waiting")
	}
	if first != branchWaitInitialBackoff {
		t.Fatalf("first backoff = %s, want %s", first, branchWaitInitialBackoff)
	}
	if e.branchWaitDue(7) {
		t.Fatal("step is due again immediately; the 2s spin is still possible")
	}
	// An unrelated task is never gated by someone else's contention.
	if !e.branchWaitDue(8) {
		t.Fatal("a task with no recorded wait must always be due")
	}

	second, _ := e.deferForBusyBranch(7)
	if second <= first {
		t.Fatalf("backoff did not grow: %s then %s", first, second)
	}

	for i := 0; i < 20; i++ {
		got, keep := e.deferForBusyBranch(7)
		if !keep {
			t.Fatal("give-up fired on elapsed attempts rather than elapsed time")
		}
		if got > branchWaitMaxBackoff {
			t.Fatalf("backoff %s exceeded the %s cap", got, branchWaitMaxBackoff)
		}
	}

	e.clearBranchWait(7)
	if !e.branchWaitDue(7) {
		t.Fatal("cleared wait should leave the task due")
	}
}

// A branch that never frees must not be retried forever in silence: past the
// give-up window the step is parked so a human sees the stall.
func TestBranchWaitGivesUpAfterDeadline(t *testing.T) {
	e := &Executor{}
	if _, keepWaiting := e.deferForBusyBranch(9); !keepWaiting {
		t.Fatal("should still be waiting on the first deferral")
	}

	e.branchWaitMu.Lock()
	e.branchWaits[9].first = time.Now().Add(-branchWaitGiveUp - time.Second)
	e.branchWaitMu.Unlock()

	if _, keepWaiting := e.deferForBusyBranch(9); keepWaiting {
		t.Fatalf("still waiting after %s; the step would spin indefinitely", branchWaitGiveUp)
	}
	if !e.branchWaitDue(9) {
		t.Fatal("giving up should drop the wait record")
	}
}
