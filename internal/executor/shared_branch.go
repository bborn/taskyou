package executor

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executorlock"
)

// ErrBranchBusy means a step could not start because the branch it needs is
// checked out by a worktree whose task is still RUNNING.
//
// This is a wait, not a failure. The old behaviour — bubbling the raw git error
// out of setupWorktree — parked the step 'blocked' with started_at and
// completed_at one second apart: a step that reads as "ran and finished" on
// every surface, having never launched an agent. executeTask leaves an
// ErrBranchBusy task 'queued' instead, so the next daemon tick retries it once
// the holder is done.
var ErrBranchBusy = errors.New("branch is checked out by a running step")

// Branch-contention retry policy.
//
// Re-queueing a contended step is correct, but on its own it is a hot loop: the
// worker ticks every 2s, and each pass re-enters executeTask, re-logs "Starting
// task #N" and re-queues. One branch that never freed produced ~1,100 such
// passes in 38 minutes and flushed the task's entire history out of the log ring
// buffer — so the one record of what the step had actually done was destroyed by
// the retries.
//
// A deferred step therefore backs off geometrically, and if the branch still has
// not freed after branchWaitGiveUp it parks in 'blocked' where a human can see
// it. Parking sets a started_at with no real run behind it, which this file
// otherwise avoids — but a step that has genuinely waited half an hour is a
// stall someone needs to look at, and silence is the worse failure.
const (
	branchWaitInitialBackoff = 5 * time.Second
	branchWaitMaxBackoff     = 2 * time.Minute
	branchWaitGiveUp         = 30 * time.Minute
	// branchWaitMaxShift caps the exponent so the shift below cannot overflow
	// on a long-lived wait; the backoff is clamped to branchWaitMaxBackoff well
	// before this matters.
	branchWaitMaxShift = 8
)

// branchWait records how long a step has been waiting for a contended branch.
type branchWait struct {
	first     time.Time
	nextRetry time.Time
	attempts  int
}

// deferForBusyBranch records one branch-contention deferral for a task and
// reports how long to wait before the next attempt. keepWaiting is false once
// the step has been waiting longer than branchWaitGiveUp, meaning the caller
// should park it rather than re-queue it again.
func (e *Executor) deferForBusyBranch(taskID int64) (retryIn time.Duration, keepWaiting bool) {
	e.branchWaitMu.Lock()
	defer e.branchWaitMu.Unlock()

	if e.branchWaits == nil {
		e.branchWaits = make(map[int64]*branchWait)
	}
	now := time.Now()
	wait := e.branchWaits[taskID]
	if wait == nil {
		wait = &branchWait{first: now}
		e.branchWaits[taskID] = wait
	}
	wait.attempts++

	if now.Sub(wait.first) >= branchWaitGiveUp {
		delete(e.branchWaits, taskID)
		return 0, false
	}

	shift := wait.attempts - 1
	if shift > branchWaitMaxShift {
		shift = branchWaitMaxShift
	}
	backoff := branchWaitInitialBackoff << shift
	if backoff > branchWaitMaxBackoff {
		backoff = branchWaitMaxBackoff
	}
	wait.nextRetry = now.Add(backoff)
	return backoff, true
}

// branchWaitDue reports whether a step deferred for branch contention has served
// its backoff and may be attempted again. A task with no recorded wait is always
// due, so this gate is invisible to everything except a contended step.
func (e *Executor) branchWaitDue(taskID int64) bool {
	e.branchWaitMu.Lock()
	defer e.branchWaitMu.Unlock()

	wait := e.branchWaits[taskID]
	if wait == nil {
		return true
	}
	return !time.Now().Before(wait.nextRetry)
}

// clearBranchWait forgets a step's contention history, so a step that waited
// once and then ran starts from a fresh backoff if it ever contends again.
func (e *Executor) clearBranchWait(taskID int64) {
	e.branchWaitMu.Lock()
	defer e.branchWaitMu.Unlock()
	delete(e.branchWaits, taskID)
}

// gitWorktreeHolder returns the path of the worktree that currently has branch
// checked out, or "" if no worktree holds it.
//
// git allows a branch to be attached to at most ONE worktree. Everything in a
// workflow that shares a branch — a finished step whose worktree lingers for
// review, the next step trying to attach — is contending for that single slot,
// so the first question on failure is always "who has it?".
func gitWorktreeHolder(projectDir, branch string) string {
	out, err := exec.Command("git", "-C", projectDir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}
	want := "refs/heads/" + branch
	current := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			current = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			if strings.TrimSpace(strings.TrimPrefix(line, "branch ")) == want {
				return current
			}
		}
	}
	return ""
}

// gitDetachWorktree points a worktree's HEAD at its current commit instead of a
// branch, freeing that branch for another worktree.
//
// This is the cheapest possible release: `checkout --detach` with no ref moves
// nothing in the working tree, keeps any uncommitted files exactly as they are,
// and leaves the branch ref pointing at the same commit. The worktree stays on
// disk and stays readable — which matters, because a finished step's worktree is
// where a human goes to see what it did.
func gitDetachWorktree(worktreePath string) error {
	cmd := exec.Command("git", "-C", worktreePath, "checkout", "--detach")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("detach %s: %v: %s", worktreePath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// releaseBranchFromFinishedHolder frees branch when the worktree holding it
// belongs to a step that is no longer running, and reports whether the branch is
// now available.
//
// A workflow's steps run one after another on one shared branch, but a step's
// worktree is not torn down when it completes — it is kept so its work can be
// inspected, and it goes on holding the branch forever. So the FIRST downstream
// step of every pipeline hit "fatal: '<branch>' is already checked out at ..."
// and died at spawn. Nothing about that is exceptional; it is the normal shape of
// a finished step, and the fix is to take the branch back rather than to refuse.
//
// Releasing is safe precisely because the holder is finished: its commits are on
// the branch ref (that is what "finished" means here — WorkflowStepFinished
// requires a commit that was pushed), and detaching cannot move or discard them.
//
// A holder that is still working keeps the branch. That returns (false, nil):
// not an error, just "not yet".
func (e *Executor) releaseBranchFromFinishedHolder(projectDir, branch string) (bool, error) {
	holder := gitWorktreeHolder(projectDir, branch)
	if holder == "" {
		return true, nil // nobody has it
	}

	task, err := e.taskForWorktree(holder)
	if err != nil {
		return false, fmt.Errorf("look up the task holding %s: %w", branch, err)
	}
	if task == nil {
		// A worktree with no task behind it (hand-made, or its task was deleted).
		// Leave it alone: we only ever reclaim a branch from a step we can prove
		// has finished.
		e.logger.Warn("branch held by a worktree with no task; not reclaiming",
			"branch", branch, "worktree", holder)
		return false, nil
	}

	if e.taskIsLive(task) {
		return false, nil
	}

	if err := gitDetachWorktree(holder); err != nil {
		return false, err
	}
	e.logger.Info("reclaimed shared branch from a finished step",
		"branch", branch, "from_task", task.ID, "worktree", holder)
	e.logLine(task.ID, "system", fmt.Sprintf(
		"Detached this worktree's HEAD so branch %s could pass to the next step. Your commits are on the branch; the files here are untouched.", branch))
	return true, nil
}

// taskForWorktree finds the task that owns a worktree git just named for us.
//
// git reports a worktree by its SYMLINK-RESOLVED path, while the DB holds the
// path the executor built when it created the worktree — on macOS those differ
// for anything under /tmp or /var ("/private/var/..." vs "/var/..."), and a
// plain string comparison then finds nothing. Reading "nothing" as "no task owns
// this" would make us refuse to reclaim a branch from a step that is long
// finished, which is the exact stall this code exists to clear. So compare the
// forms the two sides can legitimately disagree about.
func (e *Executor) taskForWorktree(holder string) (*db.Task, error) {
	seen := make(map[string]bool, 4)
	for _, candidate := range worktreePathForms(holder) {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		task, err := e.db.GetTaskByWorktreePath(candidate)
		if err != nil {
			return nil, err
		}
		if task != nil {
			return task, nil
		}
	}
	return nil, nil
}

// worktreePathForms returns the equivalent spellings of a path that the DB might
// hold: as given, symlink-resolved, and with macOS's /private prefix added or
// removed.
func worktreePathForms(path string) []string {
	forms := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		forms = append(forms, resolved)
	}
	if strings.HasPrefix(path, "/private/") {
		forms = append(forms, strings.TrimPrefix(path, "/private"))
	} else {
		forms = append(forms, filepath.Join("/private", path))
	}
	return forms
}

// taskIsLive reports whether a task is actively executing right now: mid-flight
// in this daemon, in a running status, or still owning a tmux window. Any of the
// three means "hands off".
//
// A task in a TERMINAL status is never live, whatever tmux still shows. An
// executor window routinely outlives the step that opened it: when the agent
// exits, the pane falls back to a plain shell and the window lingers until
// something reaps it. Trusting that stale window is what let a finished step pin
// its shared branch indefinitely — releaseBranchFromFinishedHolder read the
// leftover window as "still working", refused to reclaim, and the next step
// re-queued itself on ErrBranchBusy every tick until a human intervened. Once a
// status is terminal it is the authority; the window is only a tiebreaker for a
// task that has not reached one (a 'blocked' step, for instance, may still have
// a live agent sitting on a question).
func (e *Executor) taskIsLive(task *db.Task) bool {
	if task == nil {
		return false
	}
	e.mu.Lock()
	running := e.runningTasks[task.ID]
	e.mu.Unlock()
	if running {
		return true
	}
	if task.Status == db.StatusProcessing || task.Status == db.StatusQueued {
		return true
	}
	if task.Status == db.StatusDone || task.Status == db.StatusArchived {
		return false
	}
	return e.windowExists(task.ID)
}

// addStepBranchWorktree creates a worktree for a FAN-OUT step on its own branch,
// cut from the shared branch.
//
// Steps that run at the same time cannot share one branch — git attaches a
// branch to a single worktree, so the second sibling's `worktree add` fails
// outright. Each therefore gets `<shared>-<step slug>`: the exact branch its
// composed instructions already tell it to push to, and the branch its dependent
// step is already told to read back with `git show origin/<that branch>:<file>`.
// Branching FROM the shared branch is unrestricted (only checking it out is), so
// no number of siblings can contend.
func (e *Executor) addStepBranchWorktree(projectDir, worktreePath, stepBranch, sharedBranch string) error {
	// A retry after the step already ran once: keep its existing branch, which
	// carries whatever it committed before, instead of re-cutting from the base.
	if gitRefExists(projectDir, "refs/heads/"+stepBranch) {
		if holder := gitWorktreeHolder(projectDir, stepBranch); holder != "" && holder != worktreePath {
			if freed, err := e.releaseBranchFromFinishedHolder(projectDir, stepBranch); err != nil {
				return err
			} else if !freed {
				return fmt.Errorf("%w: %s is checked out at %s", ErrBranchBusy, stepBranch, holder)
			}
		}
		return runGitWorktreeAdd(projectDir, stepBranch, "worktree", "add", worktreePath, stepBranch)
	}

	base := "origin/" + sharedBranch
	if !gitRefExists(projectDir, "refs/remotes/"+base) {
		// The shared branch may be local-only: a document root hands off through
		// the artifact store and is told not to push.
		base = sharedBranch
		if !gitRefExists(projectDir, "refs/heads/"+base) {
			return fmt.Errorf("shared branch %s not found on origin or locally", sharedBranch)
		}
	}
	// --no-track: `worktree add -b X <path> origin/<shared>` would set X's
	// upstream to the SHARED branch, so a later bare `git push` from the step
	// would push its commits onto the branch its instructions explicitly tell it
	// not to touch.
	return runGitWorktreeAdd(projectDir, stepBranch, "worktree", "add", "--no-track", "-b", stepBranch, worktreePath, base)
}

// runGitWorktreeAdd runs a `git worktree add`, serialized against any other
// worktree creation in the same repository, and reports a failure with the git
// output — the only thing that explains what actually went wrong.
//
// The lock matters because `git worktree add` writes .git/config and fails
// outright rather than retrying when a concurrent add holds git's own config
// lock. Fan-out steps spawn in the same instant, so without this the second
// sibling dies with "could not lock config file .git/config: File exists".
func runGitWorktreeAdd(projectDir, branch string, args ...string) error {
	out, err := runGitWorktreeAddOutput(projectDir, args...)
	if err != nil {
		return fmt.Errorf("create worktree on branch %s: %v\n%s", branch, err, string(out))
	}
	return nil
}

// runGitWorktreeAddOutput is runGitWorktreeAdd for callers that need to inspect
// git's output themselves (the ordinary-task path branches on "already exists"
// and "already checked out").
func runGitWorktreeAddOutput(projectDir string, args ...string) ([]byte, error) {
	if release, err := executorlock.AcquireRepo(executorSpawnLockDir(), projectDir, repoLockTimeout); err == nil {
		defer release()
	} else {
		// Liveness over safety: a wedged holder must not stall every task
		// forever. Worst case we are back to the unserialized behaviour, which
		// fails loudly and is retried.
		log.Warn("proceeding without the repo worktree lock", "repo", projectDir, "error", err)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = projectDir
	return cmd.CombinedOutput()
}

// repoLockTimeout bounds the wait for another worktree creation in the same
// repo. A `git worktree add` on a large repo (checkout of every tracked file)
// can take a while, so this is generous compared with the spawn lock.
const repoLockTimeout = 120 * time.Second
