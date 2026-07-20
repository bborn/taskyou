package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/completion"
	"github.com/bborn/workflow/internal/db"
)

// `ty close` is a plain status write. Finishing a task correctly is not: a step
// may have an evidence gate that can REJECT the completion, a human-review gate
// that must park instead of advancing the workflow, and a PR that must wait for
// a human merge. Those rules lived only behind the taskyou_complete MCP tool, so
// an agent whose MCP transport was down had no correct way to finish — and
// reaching for `ty close` silently skipped every one of them.
//
// `ty complete` runs the exact same decision (internal/completion.Complete) over
// the CLI. It is the completion twin of `ty artifact`.

func newCompleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "complete [task-id]",
		Short: "Complete a task the same way taskyou_complete does (gates, PR routing and all)",
		Long: "Signal that a task is finished.\n\n" +
			"Unlike `ty close` (a plain status write), this runs the full completion\n" +
			"decision: any configured verify command must pass, a human-review gate parks\n" +
			"for approval instead of advancing the workflow, and a task with a PR parks for\n" +
			"a human merge. This is the CLI equivalent of the taskyou_complete MCP tool,\n" +
			"for use when the MCP server is unavailable.\n\n" +
			"The task defaults to WORKTREE_TASK_ID, so an agent can call it with no arguments\n" +
			"inside its worktree.",
		Args: cobra.MaximumNArgs(1),
		// A rejected completion is a legitimate runtime outcome, not a usage error:
		// without these, cobra dumps the full help text and re-prints the error on
		// top of the detailed explanation below (root's Execute already prints it
		// once and exits non-zero).
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var taskID int64
			if len(args) == 1 {
				parsed, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(args[0]), "#"), 10, 64)
				if err != nil {
					return fmt.Errorf("invalid task id: %s", args[0])
				}
				taskID = parsed
			} else if s := strings.TrimSpace(os.Getenv("WORKTREE_TASK_ID")); s != "" {
				parsed, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid WORKTREE_TASK_ID: %s", s)
				}
				taskID = parsed
			}
			if taskID == 0 {
				return fmt.Errorf("task id is required (pass it, or set WORKTREE_TASK_ID)")
			}

			summary, _ := cmd.Flags().GetString("summary")
			if strings.TrimSpace(summary) == "" {
				return fmt.Errorf("--summary is required (one paragraph: what was done, PR link, follow-ups)")
			}

			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			// A short-lived CLI process must generate the activity summary inline —
			// a background goroutine would be killed when the process exits.
			outcome, err := completion.Complete(database, taskID, summary, completion.Options{AsyncSummary: false})
			if err != nil {
				return err
			}
			waitForEventHooks()

			switch outcome.Kind {
			case completion.KindVerifyFailed:
				// Exit non-zero: the task is deliberately still running, and a caller
				// scripting this must be able to tell that completion was refused.
				fmt.Fprintf(os.Stderr, "%s\n\n%s\n    %s\n",
					errorStyle.Render("✗ Verification failed — this task is NOT complete."),
					"The configured check exited non-zero, so completion was rejected:",
					outcome.VerifyCommand)
				// Only show the output section when the check actually printed something
				// — an empty "--- verify output ---" block reads like truncation.
				if out := strings.TrimSpace(outcome.VerifyOutput); out != "" {
					fmt.Fprintf(os.Stderr, "\n--- verify output (tail) ---\n%s\n", out)
				}
				fmt.Fprintf(os.Stderr, "\n%s\n", dimStyle.Render("Fix the problem, then run ty complete again."))
				return fmt.Errorf("completion rejected by verify command")
			case completion.KindGateParked:
				fmt.Println(successStyle.Render(fmt.Sprintf("Task #%d output saved.", taskID)))
				fmt.Println("This is a human-review gate — it is now 'blocked' awaiting approval.")
				fmt.Println(dimStyle.Render(fmt.Sprintf("Approve with: ty close %d   (releases the next phase)", taskID)))
			case completion.KindPRReview:
				fmt.Println(successStyle.Render(fmt.Sprintf("Task #%d finished — PR #%d is up for review.", taskID, outcome.PRNumber)))
				if outcome.PRURL != "" {
					fmt.Println(dimStyle.Render("  " + outcome.PRURL))
				}
				fmt.Println("It is now 'blocked' awaiting a human merge, and moves to 'done' automatically once the PR merges or closes.")
			default:
				fmt.Println(successStyle.Render(fmt.Sprintf("Task #%d marked done.", taskID)))
			}
			return nil
		},
	}
	cmd.Flags().String("summary", "", "One-paragraph summary of what was accomplished (required)")
	return cmd
}
