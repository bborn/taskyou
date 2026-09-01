package executor

import (
	"context"
	"fmt"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
)

// resolvePlacement asks the task.placement hook where a task should run and
// turns the answer into the Runner that will build its commands.
//
// The three outcomes:
//
//   - No placement handler installed. Nothing is asked, nothing is logged,
//     nothing is written, and the local runner comes back. This is every
//     existing user, and their behaviour must be byte-for-byte what it was.
//   - A handler answered "local" (or failed to answer at all — see
//     hooks.ResolvePlacement, where every failure means local). The local runner
//     comes back, and the answer is recorded so the user can see WHY their fleet
//     was not used.
//   - A handler named a host. The host is checked before anything is run on it;
//     if it cannot be reached, this returns an error and the task fails visibly.
//     It is deliberately NOT downgraded to a local run: falling back would put
//     the load straight back on the machine placement exists to unload.
func (e *Executor) resolvePlacement(ctx context.Context, task *db.Task) (Runner, hooks.Placement, error) {
	if task == nil || e.hooks == nil || !e.hooks.HasPlacementHandler() {
		return LocalRunner{}, hooks.Placement{}, nil
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

	// Record the answer — including a deliberate "local" — so a result can be
	// traced to the machine that produced it.
	if placement.Consulted() {
		if err := e.db.SetTaskPlacement(task.ID, placement.Target, placement.Reason); err != nil {
			e.logger.Warn("could not record task placement", "task", task.ID, "error", err)
		}
		task.PlacementTarget = placement.Target
		task.PlacementReason = placement.Reason
	}

	if placement.IsLocal() {
		if placement.Consulted() {
			e.logger.Debug("placement: running locally", "task", task.ID,
				"plugin", placement.Handler, "reason", placement.Reason)
		}
		return LocalRunner{}, placement, nil
	}

	remote := RemoteRunner{Host: placement.Target, WorkDir: placement.WorkDir}
	workDir, err := remote.Preflight(ctx)
	if err != nil {
		return nil, placement, fmt.Errorf("placement chose %s but it cannot run the task: %w", placement.Target, err)
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
