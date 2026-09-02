package executor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
)

// stickyPlacementHandler is the handler name recorded on a placement that was
// read back from the database rather than asked for again. It keeps
// Placement.Consulted() true — the decision WAS made by a handler, once — while
// naming the reuse so a log line cannot be mistaken for a fresh answer.
const stickyPlacementHandler = "recorded"

// resolvePlacement decides where a task runs and returns the Runner that will
// build its commands.
//
// Placement is decided ONCE, at the first spawn, and reused for every retry,
// restart and resume after that. Asking on every spawn — which is what this used
// to do — moves a retried task to whichever host happens to have the most free
// memory at that moment, orphaning the worktree, the branch and (for a resumed
// agent) the session file the first attempt left on the old host, since the
// session file only exists on the machine that created it.
//
// The outcomes:
//
//   - The task is archived. Placement is skipped entirely and the task runs
//     locally: its restorable state (archive_ref, archive_commit) lives in the
//     LOCAL repo, so shipping it to another machine would strand it.
//   - A decision is already recorded. It is reused verbatim; no handler is
//     consulted and nothing is written.
//   - No placement handler installed and nothing recorded. Nothing is asked,
//     nothing is logged, nothing is written, and the local runner comes back.
//     This is every existing user, and their behaviour must be byte-for-byte
//     what it was.
//   - A handler answered "local" (or failed to answer at all — see
//     hooks.ResolvePlacement, where every failure means local). The local runner
//     comes back, and the answer is recorded — with its timestamp — so the user
//     can see WHY their fleet was not used, and so the next retry does not ask
//     again.
//   - A handler named a host. The host is checked before anything is run on it;
//     if it cannot be reached, this returns an error and the task fails visibly.
//     It is deliberately NOT downgraded to a local run: falling back would put
//     the load straight back on the machine placement exists to unload. A
//     recorded host that has gone away fails the same way, rather than silently
//     re-placing — `ty retry --replace` is the explicit way to move a task.
func (e *Executor) resolvePlacement(ctx context.Context, task *db.Task) (Runner, hooks.Placement, error) {
	if task == nil {
		return LocalRunner{}, hooks.Placement{}, nil
	}

	// An archived task never gets placed. Task 5198 became the bug report for
	// this: `ty worktrees cleanup` archived it (archive_ref set, worktree_path
	// cleared), something restarted it, and with no worktree_path the placement
	// path shipped a cleanly-archived, already-merged task to another machine —
	// where its archive ref, which lives in the local repo, could not be restored.
	//
	// Skipping placement rather than refusing to start is deliberate: unarchiving
	// and re-running a task is a normal, working local flow (setupWorktree calls
	// UnarchiveWorktree), and refusing outright would break it to fix a bug that
	// is only about WHERE the task runs.
	if task.ArchiveRef != "" {
		if e.hooks != nil && e.hooks.HasPlacementHandler() {
			e.logLine(task.ID, "system",
				"Running here: an archived task keeps its saved worktree in this machine's repo, so placement is skipped.")
		}
		return LocalRunner{}, hooks.Placement{}, nil
	}

	// A decision already made is reused, whatever the resolver would say today.
	if recorded, err := e.db.GetTaskPlacementDecision(task.ID); err == nil && recorded.Decided {
		placement := hooks.Placement{
			Target:  recorded.Target,
			Reason:  recorded.Reason,
			WorkDir: recorded.WorkDir,
			Handler: stickyPlacementHandler,
		}
		task.PlacementTarget = recorded.Target
		task.PlacementReason = recorded.Reason
		if placement.IsLocal() {
			e.logger.Debug("placement: reusing recorded local placement", "task", task.ID)
			return LocalRunner{}, placement, nil
		}
		e.logger.Info("placement: reusing recorded host", "task", task.ID, "host", recorded.Target)
		e.logLine(task.ID, "system", fmt.Sprintf(
			"Staying on %s (decided on the first run; use `ty retry %d --replace` to choose again)",
			recorded.Target, task.ID))
		runner, placement, err := e.remoteRunnerFor(ctx, placement)
		if err != nil {
			// The host this task was placed on has gone away. Fail visibly instead of
			// quietly choosing another one: the first attempt's worktree, branch and
			// session all live on THAT machine, and a silent move loses them.
			return nil, placement, fmt.Errorf(
				"%w (re-place it deliberately with `ty retry %d --replace`)", err, task.ID)
		}
		return runner, placement, nil
	} else if err != nil {
		e.logger.Warn("could not read recorded task placement", "task", task.ID, "error", err)
	}

	if e.hooks == nil || !e.hooks.HasPlacementHandler() {
		return LocalRunner{}, hooks.Placement{}, nil
	}

	// A task that has already run on THIS machine stays on it, and the decision is
	// recorded so it is only ever made once.
	//
	// Task 5206 is the bug report. It ran locally for six days across five resumes,
	// then a continuation reached this function — and because it predates placement
	// it had no recorded decision, so the resolver was asked for the first time and
	// shipped it to another host. There it branched from main, saw none of the six
	// days of work sitting on the local branch, and parked idle. The local worktree,
	// branch and Claude session were all orphaned in one spawn.
	//
	// The recorded-decision check above is what normally prevents this, but it can
	// only protect tasks that HAVE a decision. Every task created before placement
	// existed has none, so its first retry is free to teleport it. Local state is
	// the missing evidence: a worktree on this disk, or a session file that only
	// exists here, both say "the first attempt ran here" just as loudly as a row in
	// the placement columns would have.
	if what, ok := HasLocalState(task); ok {
		reason := what + " is on this machine"
		e.logLine(task.ID, "system", "Staying here: "+reason+
			fmt.Sprintf(" (use `ty place %d <host>` to move it deliberately)", task.ID))
		e.recordPlacement(task, "", reason, "")
		return LocalRunner{}, hooks.Placement{Reason: reason, Handler: stickyPlacementHandler}, nil
	}

	executorName := task.Executor
	if executorName == "" {
		executorName = db.DefaultExecutor()
	}
	placement := e.hooks.ResolvePlacement(ctx, hooks.PlacementTaskInfo{
		ID:       task.ID,
		Title:    task.Title,
		Project:  task.Project,
		RepoPath: e.getProjectDir(task.Project),
		Executor: executorName,
	})

	// Record the answer — including a deliberate "local", and including a host that
	// then turns out to be unreachable — so a result can be traced to the machine
	// that produced it, and so every later retry uses THIS decision rather than
	// asking again. An unreachable host is recorded on purpose: the fix is to look
	// at the host or to re-place the task deliberately, not to have ty quietly try
	// somewhere else on the next tick.
	if placement.Consulted() {
		e.recordPlacement(task, placement.Target, placement.Reason, placement.WorkDir)
	}

	if placement.IsLocal() {
		if placement.Consulted() {
			e.logger.Debug("placement: running locally", "task", task.ID,
				"plugin", placement.Handler, "reason", placement.Reason)
		}
		return LocalRunner{}, placement, nil
	}

	runner, placement, err := e.remoteRunnerFor(ctx, placement)
	if err != nil {
		return nil, placement, fmt.Errorf(
			"%w (re-place it deliberately with `ty retry %d --replace`)", err, task.ID)
	}
	// Preflight resolved the inventory's "~/projects/..." to an absolute remote
	// path. Store that, so a retry reaches the same directory without re-resolving.
	if remote, ok := placedRemotely(runner); ok && placement.Consulted() && remote.WorkDir != placement.WorkDir {
		e.recordPlacement(task, placement.Target, placement.Reason, remote.WorkDir)
	}
	return runner, placement, nil
}

// HasLocalState reports whether a task has already done work on THIS machine,
// and names that work as a noun phrase ("the first attempt's worktree") for
// callers to compose into their own sentence — "Staying here: X is on this
// machine", "moving it would strand X on this machine".
//
// Two things count, and both are unfakeable evidence of a local first attempt:
//
//   - A worktree directory that exists on this disk. Its branch holds the
//     commits, and no other host can see them.
//   - A recorded executor session. A Claude session file lives only on the
//     machine that created it, so resuming one anywhere else silently starts a
//     new, empty conversation.
//
// The worktree is checked with a stat rather than trusted from the row: a task
// whose worktree was cleaned up has nothing left here to strand, and pinning it
// local for that would be pinning it to a path that no longer exists.
func HasLocalState(task *db.Task) (string, bool) {
	if task == nil {
		return "", false
	}
	// A task already sent to a host has its state THERE; that is the recorded
	// decision's business, not this guard's. (A decided placement has already
	// returned above; this catches the legacy target-only rows that predate it.)
	if task.PlacementTarget != "" {
		return "", false
	}
	if path := strings.TrimSpace(task.WorktreePath); path != "" {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return "the first attempt's worktree", true
		}
	}
	if strings.TrimSpace(task.ClaudeSessionID) != "" {
		return "the first attempt's executor session", true
	}
	return "", false
}

// recordPlacement writes a placement decision and keeps the in-memory task in
// step with it.
func (e *Executor) recordPlacement(task *db.Task, target, reason, workDir string) {
	if err := e.db.SetTaskPlacementDecision(task.ID, target, reason, workDir); err != nil {
		e.logger.Warn("could not record task placement", "task", task.ID, "error", err)
	}
	task.PlacementTarget = target
	task.PlacementReason = reason
}

// remoteRunnerFor builds the runner for a non-local placement and checks the
// host can actually run the task before anything is started on it.
//
// The workdir Preflight resolves is written back, so a recorded placement stores
// an absolute remote path rather than the inventory's "~/projects/..." — a
// retried task then reaches the same directory without re-resolving it.
func (e *Executor) remoteRunnerFor(ctx context.Context, placement hooks.Placement) (Runner, hooks.Placement, error) {
	remote := RemoteRunner{Host: placement.Target, WorkDir: placement.WorkDir}
	workDir, err := remote.Preflight(ctx)
	if err != nil {
		return nil, placement, fmt.Errorf("this task runs on %s, which cannot run it: %w", placement.Target, err)
	}
	remote.WorkDir = workDir
	return remote, placement, nil
}

// placedRemotely returns the remote runner a placement selected, if any.
func placedRemotely(r Runner) (RemoteRunner, bool) {
	remote, ok := r.(RemoteRunner)
	return remote, ok
}

// localHeadCommit returns the HEAD of a LOCAL workspace, and "" when the task was
// placed on another machine — where workDir names a directory this process cannot
// stat, and the local baseline it feeds (used to tell "this step produced a
// commit" from "this step never started") does not apply.
func localHeadCommit(placedRemote bool, workDir string) string {
	if placedRemote {
		return ""
	}
	return gitHeadCommit(workDir)
}
