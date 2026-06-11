package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/routine"
)

// newRunCmd builds `ty run`. A numeric argument keeps the historic behavior
// (alias for `ty execute <task-id>`); a name runs a routine in the foreground.
func newRunCmd(executeCmd *cobra.Command) *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "run <routine-name|task-id>",
		Short: "Run a routine, or queue a task by ID",
		Long: `Run a routine in the foreground, recording the run and its output.

Routines are named, unattended agent runs defined in ~/.config/task/routines/.
There is no built-in scheduler: point launchd, cron, or anything else at
"ty run <name>" and TaskYou handles the rest — state, logs, history, and a
pinned alert task when a run fails.

A numeric argument keeps the historic alias behavior and queues that task for
execution by the daemon.

Examples:
  ty run twitter-monitor        # run a routine now
  ty run 42                     # same as: ty execute 42
  ty routines list              # health of all routines`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if _, err := strconv.ParseInt(args[0], 10, 64); err == nil {
				copyFlag(cmd, executeCmd, "dangerous")
				copyFlag(cmd, executeCmd, "permission-mode")
				executeCmd.Run(executeCmd, args)
				return
			}
			runRoutine(args[0])
		},
	}
	runCmd.Flags().Bool("dangerous", false, "When queueing a task by ID: execute in dangerous mode")
	runCmd.Flags().String("permission-mode", "", "When queueing a task by ID: override permission mode")
	return runCmd
}

func copyFlag(from, to *cobra.Command, name string) {
	if f := from.Flags().Lookup(name); f != nil && f.Changed {
		_ = to.Flags().Set(name, f.Value.String())
	}
}

func runRoutine(name string) {
	rt, err := routine.Load(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		fmt.Fprintln(os.Stderr, dimStyle.Render("Create it with: ty routines create "+name))
		os.Exit(1)
	}
	if rt.Disabled {
		// Exit 0 so an external scheduler doesn't treat a paused routine as a failure.
		fmt.Println(dimStyle.Render(fmt.Sprintf("Routine %q is disabled (enable with: ty routines enable %s)", name, name)))
		return
	}

	database, err := openTaskDB(db.DefaultPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()
	defer waitForEventHooks()

	runner := &routine.Runner{DB: database, Emitter: taskEmitter, Stdout: os.Stdout}
	fmt.Println(dimStyle.Render(fmt.Sprintf("Running routine %q (model %s, timeout %s)...", rt.Name, rt.Model, rt.Timeout)))
	result, runErr := runner.Run(context.Background(), rt)
	if result == nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+runErr.Error()))
		os.Exit(1)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Run #%d failed after %s: %v", result.RunID, result.Duration.Round(time.Second), runErr)))
		fmt.Fprintln(os.Stderr, dimStyle.Render("Log: "+result.LogPath))
		os.Exit(1)
	}
	fmt.Println(successStyle.Render(fmt.Sprintf("Run #%d ok (%s)", result.RunID, result.Duration.Round(time.Second))))
}

func newRoutinesCmd() *cobra.Command {
	routinesCmd := &cobra.Command{
		Use:     "routines",
		Aliases: []string{"routine"},
		Short:   "Manage routines (named unattended agent runs)",
		Long: `Routines are named, unattended agent runs: a prompt.md (plus optional env.sh
for secrets/fail-fast checks) in ~/.config/task/routines/<name>/.

TaskYou deliberately has no scheduler — trigger runs with "ty run <name>" from
launchd, cron, or anything else. TaskYou owns the run: per-routine state dir,
logs, run history, and failure alerting (a pinned task + routine.failed hook).`,
		Run: func(cmd *cobra.Command, args []string) {
			listRoutines()
		},
	}

	routinesCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List routines with their last run status",
		Run: func(cmd *cobra.Command, args []string) {
			listRoutines()
		},
	})

	routinesCmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Show a routine's config and recent runs",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			showRoutine(args[0])
		},
	})

	routinesCmd.AddCommand(&cobra.Command{
		Use:   "create <name>",
		Short: "Scaffold a new routine",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			rt, err := routine.Scaffold(args[0])
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			fmt.Println(successStyle.Render("Created routine " + rt.Name))
			fmt.Printf("  %s %s\n", dimStyle.Render("Edit the prompt:"), rt.Dir+"/prompt.md")
			fmt.Printf("  %s %s\n", dimStyle.Render("Optional secrets/checks:"), rt.Dir+"/env.sh (sourced before each run)")
			fmt.Printf("  %s ty run %s\n", dimStyle.Render("Run it:"), rt.Name)
			fmt.Printf("  %s\n", dimStyle.Render("Schedule it with anything, e.g. cron: */30 * * * * ty run "+rt.Name))
		},
	})

	routinesCmd.AddCommand(&cobra.Command{
		Use:   "enable <name>",
		Short: "Enable a disabled routine",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			setRoutineDisabled(args[0], false)
		},
	})

	routinesCmd.AddCommand(&cobra.Command{
		Use:   "disable <name>",
		Short: "Disable a routine (ty run becomes a no-op until re-enabled)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			setRoutineDisabled(args[0], true)
		},
	})

	routinesCmd.AddCommand(&cobra.Command{
		Use:   "edit <name>",
		Short: "Open the routine's prompt.md in $EDITOR",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			editRoutine(args[0])
		},
	})

	deleteCmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a routine, its state dir, and its run history",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			force, _ := cmd.Flags().GetBool("force")
			deleteRoutine(args[0], force)
		},
	}
	deleteCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	routinesCmd.AddCommand(deleteCmd)

	logsCmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Print the log of a routine's most recent run",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runID, _ := cmd.Flags().GetInt64("run")
			printRoutineLog(args[0], runID)
		},
	}
	logsCmd.Flags().Int64("run", 0, "Show a specific run ID instead of the latest")
	routinesCmd.AddCommand(logsCmd)

	return routinesCmd
}

func listRoutines() {
	routines, err := routine.List()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if len(routines) == 0 {
		fmt.Println(dimStyle.Render("No routines yet. Create one with: ty routines create <name>"))
		return
	}

	database, err := openTaskDB(db.DefaultPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	latest, err := database.LatestRoutineRuns()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}

	fmt.Println(boldStyle.Render("Routines"))
	fmt.Println(dimStyle.Render(strings.Repeat("─", 60)))
	for _, rt := range routines {
		state := successStyle.Render("●")
		if rt.Disabled {
			state = dimStyle.Render("◌")
		}
		fmt.Printf("%s %s %s\n", state, boldStyle.Render(rt.Name), dimStyle.Render("("+rt.Model+")"))
		if rt.Disabled {
			fmt.Printf("  %s\n", dimStyle.Render("disabled"))
		}
		if run, ok := latest[rt.Name]; ok {
			fmt.Printf("  %s\n", renderRunLine(run))
		} else {
			fmt.Printf("  %s\n", dimStyle.Render("never run"))
		}
	}
}

func showRoutine(name string) {
	rt, err := routine.Load(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}

	fmt.Println(boldStyle.Render(rt.Name))
	fmt.Printf("%s %s\n", dimStyle.Render("Dir:"), rt.Dir)
	fmt.Printf("%s %s\n", dimStyle.Render("State:"), routine.StateDir(rt.Name))
	fmt.Printf("%s %s\n", dimStyle.Render("Model:"), rt.Model)
	if rt.Project != "" {
		fmt.Printf("%s %s\n", dimStyle.Render("Project:"), rt.Project)
	}
	fmt.Printf("%s %s\n", dimStyle.Render("Timeout:"), rt.Timeout)
	fmt.Printf("%s %s\n", dimStyle.Render("Permissions:"), rt.PermissionMode)
	if _, ok := rt.EnvPath(); ok {
		fmt.Printf("%s env.sh sourced before each run\n", dimStyle.Render("Env:"))
	}
	if rt.Disabled {
		fmt.Println(warnStyle.Render("Disabled — ty run is a no-op until: ty routines enable " + rt.Name))
	}

	database, err := openTaskDB(db.DefaultPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	runs, err := database.ListRoutineRuns(rt.Name, 10)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	fmt.Println()
	if len(runs) == 0 {
		fmt.Println(dimStyle.Render("No runs yet. Start one with: ty run " + rt.Name))
		return
	}
	fmt.Println(boldStyle.Render("Recent runs"))
	for _, run := range runs {
		fmt.Printf("  #%d %s\n", run.ID, renderRunLine(run))
	}
	fmt.Println(dimStyle.Render("\nLogs: ty routines logs " + rt.Name))
}

func setRoutineDisabled(name string, disabled bool) {
	rt, err := routine.Load(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if err := rt.SetDisabled(disabled); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if disabled {
		fmt.Println(successStyle.Render(fmt.Sprintf("Disabled %q — ty run %s is now a no-op", name, name)))
	} else {
		fmt.Println(successStyle.Render(fmt.Sprintf("Enabled %q", name)))
	}
}

func editRoutine(name string) {
	rt, err := routine.Load(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}
	// $EDITOR may be a command with flags (e.g. "code --wait"), so run via shell.
	promptPath := filepath.Join(rt.Dir, "prompt.md")
	editCmd := exec.Command("sh", "-c", editor+" "+shellQuote(promptPath))
	editCmd.Stdin = os.Stdin
	editCmd.Stdout = os.Stdout
	editCmd.Stderr = os.Stderr
	if err := editCmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Editor failed: "+err.Error()))
		os.Exit(1)
	}
	// Re-validate so a bad frontmatter edit fails here, not at the next cron fire.
	if _, err := routine.Load(name); err != nil {
		fmt.Fprintln(os.Stderr, warnStyle.Render("Warning: routine no longer loads: "+err.Error()))
		os.Exit(1)
	}
	fmt.Println(successStyle.Render("Saved — routine loads cleanly"))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func deleteRoutine(name string, force bool) {
	rt, err := routine.Load(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}

	// Confirm unless --force flag is set
	if !force {
		fmt.Printf("Delete routine %q, its state dir, and its run history? [y/N] ", rt.Name)
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Cancelled")
			return
		}
	}

	database, err := openTaskDB(db.DefaultPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	if err := database.DeleteRoutineRuns(rt.Name); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if err := routine.Delete(rt.Name); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	fmt.Println(successStyle.Render(fmt.Sprintf("Deleted routine %q", rt.Name)))
}

func printRoutineLog(name string, runID int64) {
	database, err := openTaskDB(db.DefaultPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	var run *db.RoutineRun
	if runID > 0 {
		run, err = database.GetRoutineRun(runID)
	} else {
		var runs []*db.RoutineRun
		runs, err = database.ListRoutineRuns(name, 1)
		if len(runs) > 0 {
			run = runs[0]
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if run == nil {
		fmt.Println(dimStyle.Render("No runs found for routine " + name))
		return
	}

	data, err := os.ReadFile(run.LogPath)
	if err != nil {
		// The log file may have been cleaned up; the DB still has the tail.
		fmt.Println(dimStyle.Render(fmt.Sprintf("Log file unavailable (%v); stored output tail:", err)))
		fmt.Println(run.Output)
		return
	}
	fmt.Printf("%s\n", dimStyle.Render(fmt.Sprintf("Run #%d (%s) — %s", run.ID, run.Status, run.LogPath)))
	os.Stdout.Write(data)
}

func renderRunLine(run *db.RoutineRun) string {
	var status string
	switch run.Status {
	case db.RoutineRunStatusOK:
		status = successStyle.Render("ok")
	case db.RoutineRunStatusFailed:
		status = errorStyle.Render("failed")
	default:
		status = warnStyle.Render(run.Status)
	}
	line := fmt.Sprintf("%s %s", status, dimStyle.Render(timeAgo(run.StartedAt.Time)+" ago"))
	if run.FinishedAt != nil {
		line += dimStyle.Render(fmt.Sprintf(" · took %s", run.FinishedAt.Sub(run.StartedAt.Time).Round(time.Second)))
	}
	return line
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
