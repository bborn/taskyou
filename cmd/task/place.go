package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// `ty place` is how a human overrules the placement resolver.
//
// Until now the only way a placed task moved was `ty retry --replace`, which
// forgets the decision and asks the resolver again — so you could say "not
// there" but never "here", and the answer you got back was whichever host had
// the most free memory at that second. That is the wrong shape for the two
// things people actually want: pinning a task to the machine it has been running
// on, and sending a specific task to a specific host.
//
// A decision written here is a decision like any other: sticky, reused by every
// later retry, and visible with its reason.

// localTargetNames are the words that mean "this machine". The resolver's wire
// format spells local as an empty target, which is unusable on a command line.
var localTargetNames = map[string]bool{"local": true, "here": true, "localhost": true}

// placePreflightTimeout bounds the reachability check on a named host. Placing a
// task is interactive, so this can be more generous than the spawn path's.
const placePreflightTimeout = 30 * time.Second

func newPlaceCmd() *cobra.Command {
	var dir string
	var force bool

	cmd := &cobra.Command{
		Use:               "place <task-id> [host|local]",
		Short:             "Show or set the host a task runs on",
		ValidArgsFunction: completeTaskIDs,
		Long: `Show where a task runs, or move it to a different host.

With no target, this prints the recorded placement and the reason for it.

With a target, it records that placement by hand. The decision is sticky: every
later retry, restart and resume reuses it, and the resolver is not asked again.
Use "local" (or "here") to pin a task to this machine.

Moving a task moves its work. Everything tracked in the task's worktree,
committed or not, is committed and pushed to its branch, and the outgoing agent
is asked to write a handoff for the one that takes over. The placement is only
rewritten once that push is verified, so a move either brings the work or does
not happen.

Files git ignores — .env, local databases, build output — stay on the old host.
They are named so their absence is not a surprise on the far side.

Pass --force to move WITHOUT the work: for a host that cannot be reached at all,
or a wrong turn you want to abandon.

Examples:
  task place 5206                        # where does it run, and why
  task place 5206 local                  # bring it here, work and all
  task place 5206 ol-agents --dir ~/projects/engineering
  task place 5206 ol-agents --force      # move it, abandoning the work`,
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(args[0]), "#"), 10, 64)
			if err != nil {
				return fmt.Errorf("invalid task id: %s", args[0])
			}

			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			task, err := database.GetTask(taskID)
			if err != nil {
				return err
			}
			if task == nil {
				return fmt.Errorf("task #%d not found", taskID)
			}

			current, err := database.GetTaskPlacementDecision(taskID)
			if err != nil {
				return err
			}

			if len(args) == 1 {
				fmt.Print(describePlacement(task, current))
				return nil
			}

			return carryAndPlace(cmd.Context(), database, task, current, args[1], dir, force)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "",
		"The task's directory on the target host (required the first time a host is named)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Move without the work: leave the worktree and session behind on the current host")
	return cmd
}

// describePlacement renders the recorded placement for a human.
//
// The undecided case is deliberately not called "local": a task that nobody has
// been asked about and a task the resolver deliberately kept here behave the
// same today and completely differently on the next retry, and conflating them
// is what made task 5206's move impossible to predict.
func describePlacement(task *db.Task, p db.TaskPlacement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task #%d: %s\n", task.ID, task.Title)

	switch {
	case !p.Decided:
		b.WriteString("  Runs:   here, but nothing has been decided yet\n")
		if what, ok := executor.HasLocalState(task); ok {
			fmt.Fprintf(&b, "  Note:   %s is on this machine, so it will be pinned here on its next run\n", what)
		} else {
			b.WriteString("  Note:   the placement resolver will be asked on its next run\n")
		}
	case p.Target == "":
		b.WriteString("  Runs:   here (decided)\n")
	default:
		fmt.Fprintf(&b, "  Runs:   %s\n", p.Target)
		if p.WorkDir != "" {
			fmt.Fprintf(&b, "  Dir:    %s\n", p.WorkDir)
		}
	}
	if p.Reason != "" {
		fmt.Fprintf(&b, "  Reason: %s\n", p.Reason)
	}
	return b.String()
}

// placeWhere names a target for a sentence about where a task RUNS.
func placeWhere(target string) string {
	if target == "" {
		return "here"
	}
	return "on " + target
}

// placeDestination names a target for a sentence about a task MOVING.
func placeDestination(target string) string {
	if target == "" {
		return "back to this machine"
	}
	return "to " + target
}

// preflightHost checks the host can actually run a task before a placement that
// names it is written, and returns the absolute directory it resolved to.
//
// Verifying at placement time rather than at spawn time is the whole point of
// naming a host by hand: a typo should fail here, in front of you, not six hours
// later when the daemon gets to the task.
func preflightHost(ctx context.Context, host, workDir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, placePreflightTimeout)
	defer cancel()

	resolved, err := executor.RemoteRunner{Host: host, WorkDir: workDir}.Preflight(ctx)
	if err != nil {
		return "", fmt.Errorf("%s cannot run this task: %w", host, err)
	}
	return resolved, nil
}
