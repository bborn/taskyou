package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/executorlock"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// Placement changes carry tracked work through Git before committing the new
// destination. CLI, TUI and HTTP callers share this orchestration.

// moveIgnoredWarnLimit is how many ignored files are listed before the rest are
// summarised. The list is a warning, not an inventory.
const moveIgnoredWarnLimit = 10

func carryAndPlace(ctx context.Context, database *db.DB, task *db.Task, current db.TaskPlacement,
	rawTarget, dir string, force bool, log func(string)) error {

	target := normalizePlacementTarget(rawTarget)
	if target == "local" {
		target = ""
	}
	// Already there: say so and stop. This is a no-op, not a failure — re-running
	// a move you already made should not look like something went wrong, and it
	// must not overwrite the reason the task is there.
	if current.Decided && current.Target == target {
		log(fmt.Sprintf("Task #%d already runs %s.", task.ID, placementWhere(target)))
		// "Already runs here" with no worktree here is not a no-op, it is the
		// stuck state an earlier move left behind: the placement was written, the
		// worktree never was, and every start refuses with "task has no worktree
		// yet". Running the command again is exactly how someone asks for that to
		// be fixed, so fix it.
		if target == "" && strings.TrimSpace(task.WorktreePath) == "" {
			log("  It has no worktree here yet, so this is making one.")
			if err := landLocally(database, task); err != nil {
				log("  Could not create the worktree here: " + err.Error())
				return nil
			}
			log("  Worktree: " + task.WorktreePath)
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
		resolved, err := (RemoteRunner{Host: target, WorkDir: workDir}).Preflight(ctx)
		if err != nil {
			return err
		}
		workDir = resolved
	}

	var rep CarryReport
	switch {
	case !hasWork:
		// Nothing has run yet, so there is nothing to carry and no agent to ask.
		// Recording the decision IS the whole move.
		log("Nothing has run yet, so there is no work to carry.")
	case force:
		log(fmt.Sprintf("Leaving the work on %s behind (--force).", src.Where()))
	default:
		log("Asking the agent for a handoff...")
		handoff, fromAgent := RequestHandoff(ctx, src,
			HandoffTask{ID: task.ID, Title: task.Title, Host: src.Host},
			target, AgentSender(src, task.DaemonSession, task.ID), 0)
		if fromAgent {
			log("  The agent wrote a handoff.")
		} else {
			log("  No handoff from the agent; the new one will be told to read the diff.")
		}

		log("Carrying the work...")
		var err error
		rep, err = CarryWork(ctx, src, handoff, moveDestinationName(target))
		if err != nil {
			log("  " + strings.ReplaceAll(err.Error(), "\n", "\n  "))
			log("  Pass --force to move the task anyway and leave the work behind.")
			return fmt.Errorf("the work could not be carried, so task #%d has NOT been moved", task.ID)
		}
		if rep.WIPCommit {
			log("  Committed uncommitted work as a wip: commit.")
		}
		log(fmt.Sprintf("  %s is on origin at %s.", rep.Branch, shortSHA(rep.Commit)))
	}

	reason := fmt.Sprintf("moved %s by hand", placementWhere(target))
	if rep.Branch != "" {
		reason += ", carrying " + rep.Branch
	}
	if err := database.CommitTaskPlacement(task.ID, target, reason, workDir, rep.Branch); err != nil {
		return err
	}

	// Land it. Everything from here is best effort: the carry has been proven and
	// the placement is written, so the task HAS moved. A failure below is a task
	// that starts a little later, not a task that did not move — and reporting it
	// as a failed move would be a lie about where the work is.
	if rep.Branch != "" {
		task.SourceBranch = rep.Branch
		task.BranchName = rep.Branch
	}

	// A placement alone is a promise about where the task will run; the worktree
	// is what makes that promise keepable, and nothing used to keep it on this
	// side of the move. Remote targets provision at spawn, over ssh, which is the
	// only moment that host can be reached.
	if target == "" {
		if err := landLocally(database, task); err != nil {
			log("  Could not create the worktree here: " + err.Error())
			log("  The task is still moved; its next run will try again.")
		}
	}

	moved := fmt.Sprintf("Task #%d now runs %s.", task.ID, placementWhere(target))
	if rep.Branch != "" {
		moved = fmt.Sprintf("Task #%d now runs %s, with its work.", task.ID, placementWhere(target))
	}
	log(moved)
	if workDir != "" {
		log("  Dir: " + workDir)
	}
	// Ignored files are a warning, not a refusal. They stay on the old host, which
	// is not destruction — but discovering their absence on the far side, hours
	// later, is why they are named here.
	if len(rep.LeftBehind) > 0 {
		log(fmt.Sprintf("  Not carried (git-ignored, still on %s): %s",
			src.Where(), formatLeftBehind(rep.LeftBehind)))
	}
	if rep.Branch != "" {
		log("  Its next run starts from " + rep.Branch + " and opens with " + HandoffPath + ".")
	}
	return nil
}

// landLocally gives a task that has just arrived here the worktree its next run
// needs. Every start path but the daemon's refuses a task without one, so a move
// that stops at the placement leaves the task un-startable by hand.
func landLocally(database *db.DB, task *db.Task) error {
	path, _, err := New(database, config.New(database)).EnsureLocalWorktree(task)
	if err != nil {
		return err
	}
	task.WorktreePath = path
	return nil
}

// moveSource locates the work: the host holding it and the worktree path there.
// hasWork is false when the task has never run, which is not an error — there is
// simply nothing to carry, and recording the decision is the entire move.
func moveSource(database *db.DB, task *db.Task, current db.TaskPlacement) (WorkSource, bool) {
	if current.Decided && current.Target != "" {
		path, _, err := database.GetTaskRemoteWorktree(task.ID)
		if err != nil || strings.TrimSpace(path) == "" {
			return WorkSource{}, false
		}
		return WorkSource{
			Runner:  RemoteRunner{Host: current.Target, WorkDir: current.WorkDir},
			Host:    current.Target,
			WorkDir: path,
		}, true
	}

	if strings.TrimSpace(task.WorktreePath) == "" {
		return WorkSource{}, false
	}
	return WorkSource{Runner: LocalRunner{}, WorkDir: task.WorktreePath}, true
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

func placementWhere(target string) string {
	if target == "" {
		return "here"
	}
	return "on " + target
}

// PlacementResult contains user-facing progress and warnings from a completed move.
type PlacementResult struct {
	Messages []string `json:"messages"`
}

// PlaceTask carries work and changes placement through one service for all UIs.
func PlaceTask(ctx context.Context, database *db.DB, taskID int64, target, workDir string, force bool) (PlacementResult, error) {
	target = normalizePlacementTarget(target)
	result := PlacementResult{Messages: []string{}}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	lockDir := filepath.Join(filepath.Dir(database.Path()), "placement-locks")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return result, err
	}
	release, err := executorlock.AcquireSpawn(lockDir, taskID, time.Second)
	if err != nil {
		return result, fmt.Errorf("a placement change is already running: %w", err)
	}
	defer release()
	task, err := database.GetTask(taskID)
	if err != nil {
		return result, err
	}
	if task == nil {
		return result, fmt.Errorf("task #%d not found", taskID)
	}
	current, err := database.GetTaskPlacementDecision(taskID)
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(target) == "" {
		return result, fmt.Errorf("specify an SSH destination or local")
	}
	if target != "local" && target != "here" && target != "localhost" {
		name := task.Executor
		if name == "" {
			name = db.DefaultExecutor()
		}
		if !SupportsRemoteExecutor(name) {
			return result, fmt.Errorf("%s does not support remote execution", name)
		}
	}
	err = carryAndPlace(ctx, database, task, current, target, workDir, force, func(line string) { result.Messages = append(result.Messages, line) })
	return result, err
}

func normalizePlacementTarget(target string) string {
	target = strings.TrimSpace(target)
	switch strings.ToLower(target) {
	case "local", "here", "localhost":
		return "local"
	}
	return target
}
