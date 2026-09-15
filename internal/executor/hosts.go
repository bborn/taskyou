package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
)

// Choosing a host when the task is created.
//
// Placement is normally automatic: a resolver plugin is asked once, at the first
// spawn, and its answer sticks. That is right when you have no opinion and wrong
// when you do — "run this one on the big machine" used to mean creating the
// task, letting the resolver pick, and then moving it with `ty place`, which
// carries work through Git for a task that has not run yet.
//
// So the forms ask the same plugin what the choices ARE (task.hosts), and record
// the chosen one as a placement decision before the task ever spawns. The
// executor already prefers a recorded decision over asking the resolver, so a
// choice made here is simply the decision it finds — no new precedence rule, and
// `ty place` still moves it afterwards.

// PlacementChoiceReason is recorded as the reason for a hand-picked placement.
// It is shown wherever a placement reason is (ty show, the board, the detail
// view), so it says who decided rather than what the policy was.
const PlacementChoiceReason = "chosen by hand when the task was created"

// PlacementChoices returns the hosts a user may choose between for a new task in
// this project, newest question first: is anything offering a choice at all?
//
// An empty slice means no — no placement plugin installed, no host serving this
// project, or a handler that could not answer in time — and every surface then
// shows no host picker, which is the behaviour every user without a fleet has
// always had.
func PlacementChoices(ctx context.Context, database *db.DB, project, executorName string) []hooks.Host {
	if strings.TrimSpace(project) == "" {
		return nil
	}
	if executorName == "" {
		executorName = db.DefaultExecutor()
	}
	// Only the executors that can be launched over SSH can be placed remotely, so
	// offering hosts for the others would be offering a choice that cannot be
	// honoured.
	if !SupportsRemoteExecutor(executorName) {
		return nil
	}
	repoPath := ""
	if database != nil {
		repoPath = config.New(database).GetProjectDir(project)
	}
	return hooks.NewSilent(hooks.DefaultHooksDir()).ListHosts(ctx, hooks.PlacementTaskInfo{
		Project:  project,
		RepoPath: repoPath,
		Executor: executorName,
	})
}

// ChoosePlacement records a hand-picked placement on a task that has just been
// created, before anything has run.
//
// target is what the user picked: "" (or "auto") leaves placement to the
// resolver and writes nothing, "local" pins the task to this machine, and
// anything else is an SSH destination. workDir is that project's directory on
// that host; when it is empty the host list is asked for it, so a caller only
// has to pass along the name the user chose.
//
// This deliberately does NOT go through PlaceTask: that carries work through Git
// and asks the outgoing agent for a handoff, and a task created five
// milliseconds ago has neither. The host is not probed either — an unreachable
// host fails visibly at spawn, the same way a resolver's answer does, and making
// the new-task form wait on an SSH round trip to save that would be a bad trade.
func ChoosePlacement(ctx context.Context, database *db.DB, task *db.Task, target, workDir string) error {
	if task == nil {
		return fmt.Errorf("no task to place")
	}
	target = strings.TrimSpace(target)
	if target == "" || strings.EqualFold(target, "auto") {
		return nil // leave it to the resolver: the default, and not a decision
	}

	executorName := task.Executor
	if executorName == "" {
		executorName = db.DefaultExecutor()
	}

	if normalizePlacementTarget(target) == "local" {
		return database.SetTaskPlacementDecision(task.ID, "", PlacementChoiceReason, "")
	}

	if !SupportsRemoteExecutor(executorName) {
		return fmt.Errorf("%s does not support remote execution", executorName)
	}

	workDir = strings.TrimSpace(workDir)

	// Look the host up among the offered ones. This is what lets a caller pass
	// only the name a user picked: the directory comes from the same plugin that
	// offered the host, and a host named by its inventory label is recorded as
	// the ssh destination that label stands for — writing down "mona" when the
	// fleet calls it "mona.example" would fail at launch, not here.
	if offered, ok := offeredHost(ctx, database, task.Project, executorName, target); ok {
		target = offered.Target
		if workDir == "" {
			workDir = offered.WorkDir
		}
	}
	if workDir == "" {
		return fmt.Errorf("say which directory on %s the task should use", target)
	}
	return database.SetTaskPlacementDecision(task.ID, target, PlacementChoiceReason, workDir)
}

// offeredHost finds a host among the choices for this project, by either the
// name a person reads or the destination ty records.
func offeredHost(ctx context.Context, database *db.DB, project, executorName, target string) (hooks.Host, bool) {
	for _, h := range PlacementChoices(ctx, database, project, executorName) {
		if h.Target == target || h.Name == target {
			return h, true
		}
	}
	return hooks.Host{}, false
}
