package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/bborn/workflow/internal/db"
	"github.com/spf13/cobra"
)

// addStatusDebugCommands wires the two tools that make the status log
// inspectable from a shell: the invariant check, and the audit trail itself.
//
// They live under `ty debug` rather than in the everyday surface because they
// answer a question you only ask when something looks wrong — "does the board
// still agree with the log", "who moved this task and why".
func addStatusDebugCommands(debugCmd *cobra.Command) {
	debugCmd.AddCommand(newStatusConsistencyCmd())
	debugCmd.AddCommand(newStatusLogCmd())
}

// newStatusConsistencyCmd asserts, for every task, that the cached status
// column equals the fold over that task's status events.
//
// A mismatch means something wrote tasks.status outside SetTaskStatus — the
// exact bug class the log-first design exists to make impossible. It exits
// non-zero on a mismatch so it can be a CI or cron check and not just prose.
func newStatusConsistencyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status-consistency",
		Short: "Check that every task's cached status matches the fold over its status log",
		Long: `Recompute each task's status from its append-only transition log and
compare it against the cached tasks.status column.

They can only disagree if something wrote the status column without going
through SetTaskStatus. Exits 1 if any task mismatches.

Examples:
  ty debug status-consistency`,
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			mismatches, err := database.CheckStatusConsistency()
			if err != nil {
				return err
			}
			total, evErr := database.CountStatusEvents()
			if evErr != nil {
				return evErr
			}
			if len(mismatches) == 0 {
				fmt.Println(successStyle.Render("✓ every task's cached status matches the fold over its status log"))
				fmt.Println(dimStyle.Render(fmt.Sprintf("  %d status event(s) checked", total)))
				return nil
			}
			fmt.Fprintln(os.Stderr, errorStyle.Render(
				fmt.Sprintf("✗ %d task(s) disagree with their status log:", len(mismatches))))
			for _, m := range mismatches {
				fmt.Fprintln(os.Stderr, "  "+m.String())
			}
			fmt.Fprintln(os.Stderr, dimStyle.Render(
				"\nA mismatch means tasks.status was written outside SetTaskStatus."))
			os.Exit(1)
			return nil
		},
	}
}

// newStatusLogCmd prints one task's transition history: from, to, actor,
// reason, evidence — refusals included, because an attempt to bury a task is
// as interesting as a move that succeeded.
func newStatusLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status-log <task-id>",
		Short: "Show a task's status transition history (from/to/actor/reason/evidence)",
		Long: `Print the append-only status log for one task.

Refused transitions are shown too, marked with the gate that rejected them —
an attempt to close a task with an open PR leaves a trace here rather than
vanishing as a silent no-op.

Examples:
  ty debug status-log 42`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid task id %q", args[0])
			}
			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			task, err := database.GetTask(id)
			if err != nil {
				return err
			}
			if task == nil {
				return fmt.Errorf("task #%d not found", id)
			}
			events, err := database.GetStatusEvents(id)
			if err != nil {
				return err
			}
			fmt.Println(RenderStatusLog(task, events))
			return nil
		},
	}
}

// RenderStatusLog formats a task's transition history for a terminal. Kept
// separate from the command so a test can assert on the rendering without
// spawning a process.
func RenderStatusLog(task *db.Task, events []db.StatusEvent) string {
	out := fmt.Sprintf("Status log for #%d %s\n", task.ID, task.Title)
	out += dimStyle.Render(fmt.Sprintf("cached status: %s   folded from log: %s   %d event(s)",
		task.Status, orNone(db.FoldStatus(events)), len(events))) + "\n\n"
	if len(events) == 0 {
		return out + dimStyle.Render("  (no events)") + "\n"
	}
	for _, e := range events {
		from := e.From
		if from == "" {
			from = "∅"
		}
		arrow := fmt.Sprintf("%s → %s", from, e.To)
		if e.Outcome == db.OutcomeRefused {
			arrow = errorStyle.Render(fmt.Sprintf("%s ⊘ %s [refused: %s]", from, e.To, e.Gate))
		}
		out += fmt.Sprintf("  %s  %s  %s\n", e.CreatedAt.Format("2006-01-02 15:04:05"),
			warnStyle.Render(fmt.Sprintf("%-8s", e.Actor)), arrow)
		out += dimStyle.Render("      why: "+e.Reason) + "\n"
		if ev := e.Evidence.String(); ev != "" {
			out += dimStyle.Render("      evidence: "+ev) + "\n"
		}
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "<no events>"
	}
	return s
}
