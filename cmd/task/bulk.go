package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/completion"
	"github.com/bborn/workflow/internal/db"
)

// parseTaskIDs parses a slice of string arguments into task IDs.
func parseTaskIDs(args []string) ([]int64, error) {
	var ids []int64
	for _, arg := range args {
		var id int64
		if _, err := fmt.Sscanf(arg, "%d", &id); err != nil {
			return nil, fmt.Errorf("invalid task ID: %s", arg)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// completeMultipleTaskIDs provides completion for commands that accept multiple task IDs.
func completeMultipleTaskIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return fetchTaskCompletions(toComplete)
}

// completeStatusThenMultipleTaskIDs completes status for first arg, then task IDs.
func completeStatusThenMultipleTaskIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return validStatuses(), cobra.ShellCompDirectiveNoFileComp
	}
	return fetchTaskCompletions(toComplete)
}

// newBulkCmd creates the bulk parent command and its subcommands.
func newBulkCmd() *cobra.Command {
	bulkCmd := &cobra.Command{
		Use:   "bulk",
		Short: "Perform operations on multiple tasks at once",
		Long: `Bulk operations let you act on multiple tasks in a single command.

Examples:
  ty bulk status done 10 11 12
  ty bulk delete 5 6 7
  ty bulk close 10 11 12
  ty bulk execute 10 11 12
  ty bulk archive 10 11 12`,
	}

	// bulk status <status> <task-id> [task-id...]
	bulkStatusCmd := &cobra.Command{
		Use:               "status <status> <task-id> [task-id...]",
		Short:             "Set status on multiple tasks",
		ValidArgsFunction: completeStatusThenMultipleTaskIDs,
		Long: `Change the status of multiple tasks at once.

Valid statuses: backlog, queued, processing, blocked, done, archived.

Examples:
  ty bulk status done 10 11 12
  ty bulk status backlog 5 6
  ty bulk status archived 1 2 3`,
		Args: cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			status := strings.ToLower(strings.TrimSpace(args[0]))
			if !isValidStatus(status) {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid status. Must be one of: "+strings.Join(validStatuses(), ", ")))
				os.Exit(1)
			}

			ids, err := parseTaskIDs(args[1:])
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(err.Error()))
				os.Exit(1)
			}

			database, err := db.Open(db.DefaultPath())
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			var succeeded, failed int
			for _, id := range ids {
				task, err := database.GetTask(id)
				if err != nil || task == nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found, skipping", id)))
					failed++
					continue
				}
				// `ty bulk status done` reaches the same buried-with-an-open-PR outcome
				// as `ty bulk close`, so it gets the same guard.
				if status == db.StatusDone {
					if guard := completion.CheckDoneWrite(task); guard != nil {
						fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Skipping task #%d: %s", id, guard.Reason())))
						failed++
						continue
					}
				}
				if err := database.UpdateTaskStatus(id, status); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error updating task #%d: %v", id, err)))
					failed++
					continue
				}
				if status == db.StatusDone {
					completion.RecordStatusWrite(database, id, db.StatusDone, "`ty bulk status done`")
				}
				fmt.Println(successStyle.Render(fmt.Sprintf("Task #%d moved to %s", id, status)))
				succeeded++
			}

			printBulkSummary("status change", succeeded, failed)
		},
	}
	bulkCmd.AddCommand(bulkStatusCmd)

	// bulk delete <task-id> [task-id...]
	bulkDeleteCmd := &cobra.Command{
		Use:               "delete <task-id> [task-id...]",
		Short:             "Delete multiple tasks",
		ValidArgsFunction: completeMultipleTaskIDs,
		Long: `Delete multiple tasks, killing their agent sessions and removing worktrees.

Examples:
  ty bulk delete 5 6 7
  ty bulk delete --force 1 2 3`,
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			ids, err := parseTaskIDs(args)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(err.Error()))
				os.Exit(1)
			}

			force, _ := cmd.Flags().GetBool("force")

			// Confirm unless --force
			if !force {
				// Show what will be deleted
				database, err := db.Open(db.DefaultPath())
				if err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
				fmt.Printf("Tasks to delete:\n")
				for _, id := range ids {
					task, err := database.GetTask(id)
					if err != nil || task == nil {
						fmt.Printf("  #%d (not found)\n", id)
					} else {
						fmt.Printf("  #%d: %s\n", id, task.Title)
					}
				}
				database.Close()

				fmt.Printf("\nDelete %d task(s)? [y/N] ", len(ids))
				reader := bufio.NewReader(os.Stdin)
				response, _ := reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response != "y" && response != "yes" {
					fmt.Println("Cancelled")
					return
				}
			}

			var succeeded, failed int
			for _, id := range ids {
				if err := softDeleteTask(id); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error trashing task #%d: %v", id, err)))
					failed++
					continue
				}
				fmt.Println(successStyle.Render(fmt.Sprintf("Trashed task #%d", id)))
				succeeded++
			}

			printBulkSummary("trash", succeeded, failed)
		},
	}
	bulkDeleteCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	bulkCmd.AddCommand(bulkDeleteCmd)

	// bulk close <task-id> [task-id...]
	bulkCloseCmd := &cobra.Command{
		Use:               "close <task-id> [task-id...]",
		Aliases:           []string{"done", "complete"},
		Short:             "Mark multiple tasks as done",
		ValidArgsFunction: completeMultipleTaskIDs,
		Long: `Mark multiple tasks as completed.

Examples:
  ty bulk close 10 11 12
  ty bulk done 5 6`,
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			ids, err := parseTaskIDs(args)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(err.Error()))
				os.Exit(1)
			}

			database, err := db.Open(db.DefaultPath())
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			var succeeded, failed int
			for _, id := range ids {
				task, err := database.GetTask(id)
				if err != nil || task == nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found, skipping", id)))
					failed++
					continue
				}
				if task.Status == db.StatusDone {
					fmt.Println(dimStyle.Render(fmt.Sprintf("Task #%d is already done, skipping", id)))
					continue
				}
				// Bulk is where an unguarded write does the most damage: one command
				// can bury a dozen tasks that were each waiting on a human.
				if guard := completion.CheckDoneWrite(task); guard != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Skipping task #%d: %s", id, guard.Reason())))
					failed++
					continue
				}
				if err := database.UpdateTaskStatus(id, db.StatusDone); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error closing task #%d: %v", id, err)))
					failed++
					continue
				}
				completion.RecordStatusWrite(database, id, db.StatusDone, "`ty bulk close`")
				fmt.Println(successStyle.Render(fmt.Sprintf("Closed task #%d: %s", id, task.Title)))
				succeeded++
			}

			printBulkSummary("close", succeeded, failed)
		},
	}
	bulkCmd.AddCommand(bulkCloseCmd)

	// bulk execute <task-id> [task-id...]
	bulkExecuteCmd := &cobra.Command{
		Use:               "execute <task-id> [task-id...]",
		Aliases:           []string{"queue", "run"},
		Short:             "Queue multiple tasks for execution",
		ValidArgsFunction: completeMultipleTaskIDs,
		Long: `Queue multiple tasks for execution by the daemon.

Examples:
  ty bulk execute 10 11 12
  ty bulk queue 5 6
  ty bulk execute --dangerous 10 11 12`,
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			ids, err := parseTaskIDs(args)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(err.Error()))
				os.Exit(1)
			}

			executeDangerous, _ := cmd.Flags().GetBool("dangerous")

			database, err := db.Open(db.DefaultPath())
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			var succeeded, failed int
			for _, id := range ids {
				task, err := database.GetTask(id)
				if err != nil || task == nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found, skipping", id)))
					failed++
					continue
				}
				if task.Status == db.StatusQueued {
					fmt.Println(dimStyle.Render(fmt.Sprintf("Task #%d is already queued, skipping", id)))
					continue
				}
				if task.Status == db.StatusProcessing {
					fmt.Println(dimStyle.Render(fmt.Sprintf("Task #%d is already processing, skipping", id)))
					continue
				}

				if executeDangerous {
					if err := database.UpdateTaskDangerousMode(id, true); err != nil {
						fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error setting dangerous mode for task #%d: %v", id, err)))
						failed++
						continue
					}
				}

				if err := database.UpdateTaskStatus(id, db.StatusQueued); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error queueing task #%d: %v", id, err)))
					failed++
					continue
				}
				msg := fmt.Sprintf("Queued task #%d: %s", id, task.Title)
				if executeDangerous {
					msg += " (dangerous mode)"
				}
				fmt.Println(successStyle.Render(msg))
				succeeded++
			}

			printBulkSummary("queue", succeeded, failed)
			if succeeded > 0 {
				ensureDaemonForQueuedWork()
			}
		},
	}
	bulkExecuteCmd.Flags().Bool("dangerous", false, "Execute in dangerous mode (skip permission prompts)")
	bulkCmd.AddCommand(bulkExecuteCmd)

	// bulk archive <task-id> [task-id...]
	bulkArchiveCmd := &cobra.Command{
		Use:               "archive <task-id> [task-id...]",
		Short:             "Archive multiple tasks",
		ValidArgsFunction: completeMultipleTaskIDs,
		Long: `Archive multiple tasks at once.

Examples:
  ty bulk archive 1 2 3`,
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			ids, err := parseTaskIDs(args)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(err.Error()))
				os.Exit(1)
			}

			database, err := db.Open(db.DefaultPath())
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			var succeeded, failed int
			for _, id := range ids {
				task, err := database.GetTask(id)
				if err != nil || task == nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found, skipping", id)))
					failed++
					continue
				}
				if task.Status == db.StatusArchived {
					fmt.Println(dimStyle.Render(fmt.Sprintf("Task #%d is already archived, skipping", id)))
					continue
				}
				if err := database.UpdateTaskStatus(id, db.StatusArchived); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error archiving task #%d: %v", id, err)))
					failed++
					continue
				}
				fmt.Println(successStyle.Render(fmt.Sprintf("Archived task #%d: %s", id, task.Title)))
				succeeded++
			}

			printBulkSummary("archive", succeeded, failed)
		},
	}
	bulkCmd.AddCommand(bulkArchiveCmd)

	return bulkCmd
}

// printBulkSummary prints a summary line for bulk operations.
func printBulkSummary(operation string, succeeded, failed int) {
	if succeeded+failed == 0 {
		return
	}
	if failed == 0 {
		fmt.Println(successStyle.Render(fmt.Sprintf("\nBulk %s complete: %d succeeded", operation, succeeded)))
	} else {
		fmt.Println(dimStyle.Render(fmt.Sprintf("\nBulk %s complete: %d succeeded, %d failed", operation, succeeded, failed)))
	}
}
