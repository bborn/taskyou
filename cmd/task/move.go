package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// `ty place --carry` is `ty place` for people who meant it.
//
// place changes where a task RUNS and says, accurately, that the worktree and
// session stay behind. --carry takes the work: it asks the agent for a handoff,
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
	rawTarget, dir string, force, noHandoff bool) error {

	target := strings.TrimSpace(rawTarget)
	if localTargetNames[strings.ToLower(target)] {
		target = ""
	}
	if current.Decided && current.Target == target {
		return fmt.Errorf("task #%d already runs %s; there is nothing to move", task.ID, placeWhere(target))
	}

	src, err := moveSource(database, task, current)
	if err != nil {
		return err
	}

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

	handoff, fromAgent := executor.SyntheticHandoff(
		executor.HandoffTask{ID: task.ID, Title: task.Title, Host: src.Host},
		target, "ty was told not to ask for one"), false
	if !noHandoff {
		fmt.Println(dimStyle.Render("Asking the agent for a handoff..."))
		handoff, fromAgent = executor.RequestHandoff(ctx, src,
			executor.HandoffTask{ID: task.ID, Title: task.Title, Host: src.Host},
			target, executor.AgentSender(src, task.DaemonSession, task.ID), 0)
	}
	if fromAgent {
		fmt.Println(successStyle.Render("  The agent wrote a handoff."))
	} else {
		fmt.Println(warnStyle.Render("  No handoff from the agent; the new one will be told to read the diff."))
	}

	fmt.Println(dimStyle.Render("Carrying the work..."))
	rep, err := executor.CarryWork(ctx, src, handoff, moveDestinationName(target))
	if err != nil {
		fmt.Println(dimStyle.Render("  " + strings.ReplaceAll(err.Error(), "\n", "\n  ")))
		return fmt.Errorf("the work could not be carried, so task #%d has NOT been moved", task.ID)
	}
	if rep.WIPCommit {
		fmt.Println(dimStyle.Render("  Committed uncommitted work as a wip: commit."))
	}
	fmt.Println(successStyle.Render(fmt.Sprintf("  %s is on origin at %s.", rep.Branch, shortSHA(rep.Commit))))

	if len(rep.LeftBehind) > 0 && !force {
		// The detail is printed rather than folded into the error: lipgloss pads a
		// multi-line block out to its widest line, so a multi-line error renders as
		// a ragged box. The "Error:" line stays short and scannable.
		fmt.Println(warnStyle.Render(fmt.Sprintf("  %d git-ignored file(s) would be left behind on %s:",
			len(rep.LeftBehind), src.Where())))
		fmt.Println(warnStyle.Render("    " + formatLeftBehind(rep.LeftBehind)))
		fmt.Println(dimStyle.Render("  These do not travel. If the task needs them on the far side, put them there first."))
		fmt.Println(dimStyle.Render("  The work is already committed and pushed, so re-running with --force carries nothing extra."))
		return fmt.Errorf("task #%d has NOT been moved (pass --force to move without those files)", task.ID)
	}

	if err := database.ClearTaskPlacement(task.ID); err != nil {
		return err
	}
	reason := fmt.Sprintf("moved %s by hand, carrying %s", placeWhere(target), rep.Branch)
	if err := database.SetTaskPlacementDecision(task.ID, target, reason, workDir); err != nil {
		return err
	}

	fmt.Println(successStyle.Render(fmt.Sprintf("Task #%d now runs %s, with its work.", task.ID, placeWhere(target))))
	if workDir != "" {
		fmt.Println(dimStyle.Render("  Dir: " + workDir))
	}
	if len(rep.LeftBehind) > 0 {
		fmt.Println(warnStyle.Render(fmt.Sprintf("  Left behind on %s: %s", src.Where(), formatLeftBehind(rep.LeftBehind))))
	}
	fmt.Println(dimStyle.Render("  Its next run starts from " + rep.Branch + " and opens with " + executor.HandoffPath + "."))
	return nil
}

// moveSource locates the work: the host holding it and the worktree path there.
func moveSource(database *db.DB, task *db.Task, current db.TaskPlacement) (executor.WorkSource, error) {
	if current.Decided && current.Target != "" {
		path, _, err := database.GetTaskRemoteWorktree(task.ID)
		if err != nil {
			return executor.WorkSource{}, err
		}
		if strings.TrimSpace(path) == "" {
			return executor.WorkSource{}, fmt.Errorf(
				"task #%d is placed on %s but ty never recorded a worktree there, so there is no work to carry.\n"+
					"Use `ty place` if you only want to change where it runs next",
				task.ID, current.Target)
		}
		return executor.WorkSource{
			Runner:  executor.RemoteRunner{Host: current.Target, WorkDir: current.WorkDir},
			Host:    current.Target,
			WorkDir: path,
		}, nil
	}

	if strings.TrimSpace(task.WorktreePath) == "" {
		return executor.WorkSource{}, fmt.Errorf(
			"task #%d has no worktree on this machine, so there is no work to carry.\n"+
				"Use `ty place` if you only want to change where it runs next", task.ID)
	}
	return executor.WorkSource{Runner: executor.LocalRunner{}, WorkDir: task.WorktreePath}, nil
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
