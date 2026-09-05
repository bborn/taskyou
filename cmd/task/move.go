package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// Moving a task moves its WORK. That is the whole feature; there is no flag for
// it, because there was never a version of "move this task" that sensibly meant
// "leave the code behind".
//
// It was briefly a --carry flag, on the theory that most placements happen to
// tasks that have never run and so have nothing to carry. That theory was made
// up. Every placement decision in a real board — 31 of 31 — was on a task that
// had already run. So carrying is the default and the flag is gone.
//
// The move asks the agent for a handoff,
// commits and pushes everything in the task's worktree, PROVES the push
// arrived, and only then rewrites the placement. If the work cannot be proven
// safe the placement is never touched, so a failed move leaves the task exactly
// where it was rather than half-moved.
//
// It is a flag on place rather than its own verb for two reasons: `ty move` is
// already taken (it moves a task between projects), and place's refusal — "this
// would strand your worktree" — is exactly where someone needs to be told that
// carrying it is an option.

// moveIgnoredWarnLimit is how many ignored files are listed before the rest are
// summarised. The list is a warning, not an inventory.
const moveIgnoredWarnLimit = 10

func carryAndPlace(ctx context.Context, database *db.DB, task *db.Task, current db.TaskPlacement,
	rawTarget, dir string, force bool) error {

	target := strings.TrimSpace(rawTarget)
	if localTargetNames[strings.ToLower(target)] {
		target = ""
	}
	// Already there: say so and stop. This is a no-op, not a failure — re-running
	// a move you already made should not look like something went wrong, and it
	// must not overwrite the reason the task is there.
	if current.Decided && current.Target == target {
		fmt.Println(dimStyle.Render(fmt.Sprintf("Task #%d already runs %s.", task.ID, placeWhere(target))))
		// "Already runs here" with no worktree here is not a no-op, it is the
		// stuck state an earlier move left behind: the placement was written, the
		// worktree never was, and every start refuses with "task has no worktree
		// yet". Running the command again is exactly how someone asks for that to
		// be fixed, so fix it.
		if target == "" && strings.TrimSpace(task.WorktreePath) == "" {
			fmt.Println(dimStyle.Render("  It has no worktree here yet, so this is making one."))
			if err := landLocally(database, task); err != nil {
				fmt.Println(warnStyle.Render("  Could not create the worktree here: " + err.Error()))
				return nil
			}
			fmt.Println(successStyle.Render("  Worktree: " + task.WorktreePath))
		}
		return nil
	}

	src, hasWork := moveSource(database, task, current)

	// Resolve the destination FIRST. A typo in a hostname should fail before the
	// agent is interrupted and a wip commit is pushed on its behalf.
	workDir := dir
	if target != "" {
		if workDir == "" && current.Target == target {
			workDir = current.WorkDir
		}
		if workDir == "" {
			return fmt.Errorf("say which directory on %s the task should use: --dir <path>", target)
		}
		resolved, err := preflightHost(ctx, target, workDir)
		if err != nil {
			return err
		}
		workDir = resolved
	}

	var rep executor.CarryReport
	switch {
	case !hasWork:
		// Nothing has run yet, so there is nothing to carry and no agent to ask.
		// Recording the decision IS the whole move.
		fmt.Println(dimStyle.Render("Nothing has run yet, so there is no work to carry."))
	case force:
		fmt.Println(warnStyle.Render(fmt.Sprintf("Leaving the work on %s behind (--force).", src.Where())))
	default:
		fmt.Println(dimStyle.Render("Asking the agent for a handoff..."))
		handoff, fromAgent := executor.RequestHandoff(ctx, src,
			executor.HandoffTask{ID: task.ID, Title: task.Title, Host: src.Host},
			target, executor.AgentSender(src, task.DaemonSession, task.ID), 0)
		if fromAgent {
			fmt.Println(successStyle.Render("  The agent wrote a handoff."))
		} else {
			fmt.Println(warnStyle.Render("  No handoff from the agent; the new one will be told to read the diff."))
		}

		fmt.Println(dimStyle.Render("Carrying the work..."))
		var err error
		rep, err = executor.CarryWork(ctx, src, handoff, moveDestinationName(target))
		if err != nil {
			fmt.Println(dimStyle.Render("  " + strings.ReplaceAll(err.Error(), "\n", "\n  ")))
			fmt.Println(dimStyle.Render("  Pass --force to move the task anyway and leave the work behind."))
			return fmt.Errorf("the work could not be carried, so task #%d has NOT been moved", task.ID)
		}
		if rep.WIPCommit {
			fmt.Println(dimStyle.Render("  Committed uncommitted work as a wip: commit."))
		}
		fmt.Println(successStyle.Render(fmt.Sprintf("  %s is on origin at %s.", rep.Branch, shortSHA(rep.Commit))))
	}

	if err := database.ClearTaskPlacement(task.ID); err != nil {
		return err
	}
	reason := fmt.Sprintf("moved %s by hand", placeWhere(target))
	if rep.Branch != "" {
		reason += ", carrying " + rep.Branch
	}
	if err := database.SetTaskPlacementDecision(task.ID, target, reason, workDir); err != nil {
		return err
	}

	// Land it. Everything from here is best effort: the carry has been proven and
	// the placement is written, so the task HAS moved. A failure below is a task
	// that starts a little later, not a task that did not move — and reporting it
	// as a failed move would be a lie about where the work is.
	if rep.Branch != "" {
		if err := recordCarriedBranch(database, task, rep.Branch); err != nil {
			fmt.Println(warnStyle.Render("  Could not record the carried branch: " + err.Error()))
		}
	}
	// A placement alone is a promise about where the task will run; the worktree
	// is what makes that promise keepable, and nothing used to keep it on this
	// side of the move. Remote targets provision at spawn, over ssh, which is the
	// only moment that host can be reached.
	if target == "" {
		if err := landLocally(database, task); err != nil {
			fmt.Println(warnStyle.Render("  Could not create the worktree here: " + err.Error()))
			fmt.Println(dimStyle.Render("  The task is still moved; its next run will try again."))
		}
	}

	moved := fmt.Sprintf("Task #%d now runs %s.", task.ID, placeWhere(target))
	if rep.Branch != "" {
		moved = fmt.Sprintf("Task #%d now runs %s, with its work.", task.ID, placeWhere(target))
	}
	fmt.Println(successStyle.Render(moved))
	if workDir != "" {
		fmt.Println(dimStyle.Render("  Dir: " + workDir))
	}
	// Ignored files are a warning, not a refusal. They stay on the old host, which
	// is not destruction — but discovering their absence on the far side, hours
	// later, is why they are named here.
	if len(rep.LeftBehind) > 0 {
		fmt.Println(warnStyle.Render(fmt.Sprintf("  Not carried (git-ignored, still on %s): %s",
			src.Where(), formatLeftBehind(rep.LeftBehind))))
	}
	if rep.Branch != "" {
		fmt.Println(dimStyle.Render("  Its next run starts from " + rep.Branch + " and opens with " + executor.HandoffPath + "."))
	}
	return nil
}

// recordCarriedBranch writes the branch the work travelled on onto the task, so
// the machine that picks it up can FIND that work.
//
// This is the half that is easy to skip and impossible to do without. A move
// used to leave the branch name only in the placement REASON — "moved here by
// hand, carrying task/5286-..." — which is prose. Worktree setup reads fields,
// not sentences, so it saw a task with no branch of its own and did what that
// means: cut a brand new branch from the default one. Both landings then looked
// perfect and contained none of the work, immediately after the carry gate had
// finished proving that work was safe.
//
// SourceBranch routes local setup through addSourceBranchWorktree, which
// attaches to origin/<branch> and fails loudly when the branch is nowhere rather
// than inventing an empty one. BranchName is what newWorktreeBranchName reads,
// so the remote host asks for the carried branch by name instead of rebuilding a
// name from the task's title — which a rename would have quietly changed.
func recordCarriedBranch(database *db.DB, task *db.Task, branch string) error {
	if strings.TrimSpace(branch) == "" {
		return nil
	}
	task.SourceBranch = branch
	task.BranchName = branch
	return database.UpdateTask(task)
}

// landLocally gives a task that has just arrived here the worktree its next run
// needs. Every start path but the daemon's refuses a task without one, so a move
// that stops at the placement leaves the task un-startable by hand.
func landLocally(database *db.DB, task *db.Task) error {
	path, _, err := executor.New(database, config.New(database)).EnsureLocalWorktree(task)
	if err != nil {
		return err
	}
	task.WorktreePath = path
	return nil
}

// moveSource locates the work: the host holding it and the worktree path there.
// hasWork is false when the task has never run, which is not an error — there is
// simply nothing to carry, and recording the decision is the entire move.
func moveSource(database *db.DB, task *db.Task, current db.TaskPlacement) (executor.WorkSource, bool) {
	if current.Decided && current.Target != "" {
		path, _, err := database.GetTaskRemoteWorktree(task.ID)
		if err != nil || strings.TrimSpace(path) == "" {
			return executor.WorkSource{}, false
		}
		return executor.WorkSource{
			Runner:  executor.RemoteRunner{Host: current.Target, WorkDir: current.WorkDir},
			Host:    current.Target,
			WorkDir: path,
		}, true
	}

	if strings.TrimSpace(task.WorktreePath) == "" {
		return executor.WorkSource{}, false
	}
	return executor.WorkSource{Runner: executor.LocalRunner{}, WorkDir: task.WorktreePath}, true
}

func moveDestinationName(target string) string {
	if target == "" {
		return "this machine"
	}
	return target
}

func formatLeftBehind(files []string) string {
	if len(files) <= moveIgnoredWarnLimit {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(files[:moveIgnoredWarnLimit], ", "),
		len(files)-moveIgnoredWarnLimit)
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
