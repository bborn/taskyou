package executor

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bborn/workflow/internal/db"
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
	return tmuxWindowExistsForTask(task.ID)
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
	return runGitWorktreeAdd(projectDir, stepBranch, "worktree", "add", "-b", stepBranch, worktreePath, base)
}

// runGitWorktreeAdd runs a `git worktree add` and reports a failure with the
// git output, which is the only thing that explains what actually went wrong.
func runGitWorktreeAdd(projectDir, branch string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = projectDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create worktree on branch %s: %v\n%s", branch, err, string(out))
	}
	return nil
}
