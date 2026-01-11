// task is the local CLI for managing tasks.
// It connects to a remote taskd server over SSH by default, or runs locally with -l flag.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/github"
	"github.com/bborn/workflow/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/term"
)

var (
	// Default server configuration
	defaultHost = "cloud-claude"
	defaultPort = "2222"

	// Styles for CLI output
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	boldStyle    = lipgloss.NewStyle().Bold(true)
)

// getSessionID returns a unique session identifier for this instance.
// Uses PID to ensure each task instance gets its own tmux sessions.
func getSessionID() string {
	// Check if SESSION_ID is already set (for child processes)
	if sid := os.Getenv("TASK_SESSION_ID"); sid != "" {
		return sid
	}
	// Generate new session ID based on PID
	return fmt.Sprintf("%d", os.Getpid())
}

// getUISessionName returns the task-ui session name for this instance.
func getUISessionName() string {
	return fmt.Sprintf("task-ui-%s", getSessionID())
}

// getDaemonSessionName returns the task-daemon session name for this instance.
func getDaemonSessionName() string {
	return fmt.Sprintf("task-daemon-%s", getSessionID())
}

func main() {
	var (
		host      string
		port      string
		local     bool
		dangerous bool
	)

	rootCmd := &cobra.Command{
		Use:   "task",
		Short: "Task queue manager",
		Long:  "A beautiful terminal UI for managing your task queue.",
		Run: func(cmd *cobra.Command, args []string) {
			// TUI requires tmux for split-pane Claude interaction
			if os.Getenv("TMUX") == "" {
				if err := execInTmux(); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
				return
			}

			if local {
				if err := runLocal(dangerous); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
				return
			}
			// Connect to remote server
			if err := connectRemote(host, port); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Connection failed: "+err.Error()))
				os.Exit(1)
			}
		},
	}

	rootCmd.PersistentFlags().StringVarP(&host, "host", "H", defaultHost, "Remote server host")
	rootCmd.PersistentFlags().StringVar(&port, "port", defaultPort, "Remote server port")
	rootCmd.PersistentFlags().BoolVarP(&local, "local", "l", false, "Run locally (use local database)")
	rootCmd.PersistentFlags().BoolVar(&dangerous, "dangerous", false, "Run Claude with --dangerously-skip-permissions (for sandboxed environments)")

	// Daemon subcommand - runs executor in background
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the task executor daemon",
		Long:  "Runs the background executor that processes queued tasks.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := runDaemon(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}

	// Daemon stop subcommand
	daemonStopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		Run: func(cmd *cobra.Command, args []string) {
			if err := stopDaemon(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			fmt.Println(successStyle.Render("Daemon stopped"))
		},
	}
	daemonCmd.AddCommand(daemonStopCmd)

	// Daemon restart subcommand
	daemonRestartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		Run: func(cmd *cobra.Command, args []string) {
			// Stop if running (ignore errors - might not be running)
			stopDaemon()

			// Small delay to ensure clean shutdown
			time.Sleep(100 * time.Millisecond)

			// Start daemon (inherit dangerous mode from environment if set)
			dangerousMode := os.Getenv("TASK_DANGEROUS_MODE") == "1"
			if err := ensureDaemonRunning(dangerousMode); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			fmt.Println(successStyle.Render("Daemon restarted"))
		},
	}
	daemonCmd.AddCommand(daemonRestartCmd)

	// Daemon status subcommand
	daemonStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check daemon status",
		Run: func(cmd *cobra.Command, args []string) {
			pidFile := getPidFilePath()
			if pid, err := readPidFile(pidFile); err == nil && processExists(pid) {
				fmt.Println(successStyle.Render(fmt.Sprintf("Daemon running (pid %d)", pid)))
			} else {
				fmt.Println(dimStyle.Render("Daemon not running"))
			}
		},
	}
	daemonCmd.AddCommand(daemonStatusCmd)

	rootCmd.AddCommand(daemonCmd)

	// Restart subcommand - restart daemon and TUI (preserves Claude sessions by default)
	var hardRestart bool
	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon and TUI (preserves Claude sessions)",
		Long: `Restarts the daemon and TUI while preserving running Claude sessions.
Use --hard to kill all tmux sessions for a complete reset.`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(dimStyle.Render("Stopping daemon..."))
			stopDaemon()

			if hardRestart {
				fmt.Println(dimStyle.Render("Killing tmux sessions..."))
				// Kill all task-daemon-* and task-ui-* sessions
				out, _ := osexec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
				for _, session := range strings.Split(string(out), "\n") {
					session = strings.TrimSpace(session)
					if strings.HasPrefix(session, "task-daemon-") || strings.HasPrefix(session, "task-ui-") {
						osexec.Command("tmux", "kill-session", "-t", session).Run()
					}
				}
			} else {
				// Soft restart: only kill the task-ui session, preserve task-daemon sessions with Claude windows
				fmt.Println(dimStyle.Render("Preserving Claude sessions..."))
				out, _ := osexec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
				for _, session := range strings.Split(string(out), "\n") {
					session = strings.TrimSpace(session)
					// Only kill task-ui sessions, keep task-daemon sessions with Claude windows
					if strings.HasPrefix(session, "task-ui-") {
						osexec.Command("tmux", "kill-session", "-t", session).Run()
					}
				}
			}

			fmt.Println(successStyle.Render("Restarting..."))
			time.Sleep(200 * time.Millisecond)

			// Re-exec task command
			executable, err := os.Executable()
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			// Pass through -l flag if we're in local mode
			execArgs := []string{executable}
			if local {
				execArgs = append(execArgs, "-l")
			}

			syscall.Exec(executable, execArgs, os.Environ())
		},
	}
	restartCmd.Flags().BoolVarP(&local, "local", "l", false, "Run locally")
	restartCmd.Flags().BoolVar(&hardRestart, "hard", false, "Kill all tmux sessions (full reset)")
	rootCmd.AddCommand(restartCmd)

	// Logs subcommand - tail claude session logs
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "Tail claude session logs for debugging",
		Long:  "Streams all claude session logs across all projects in real-time.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := tailClaudeLogs(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	rootCmd.AddCommand(logsCmd)

	// Claude hook subcommand - handles Claude Code hook callbacks (internal use)
	claudeHookCmd := &cobra.Command{
		Use:    "claude-hook",
		Short:  "Handle Claude Code hook callbacks",
		Hidden: true, // Internal use only
		Run: func(cmd *cobra.Command, args []string) {
			hookEvent, _ := cmd.Flags().GetString("event")
			if err := handleClaudeHook(hookEvent); err != nil {
				// Don't print errors - hooks should be silent
				os.Exit(1)
			}
		},
	}
	claudeHookCmd.Flags().String("event", "", "Hook event type (Notification, Stop, etc.)")
	rootCmd.AddCommand(claudeHookCmd)

	// Claudes subcommand - manage running Claude sessions
	claudesCmd := &cobra.Command{
		Use:   "claudes",
		Short: "Manage running Claude tmux sessions",
		Run: func(cmd *cobra.Command, args []string) {
			listClaudeSessions()
		},
	}

	claudesListCmd := &cobra.Command{
		Use:   "list",
		Short: "List running Claude sessions",
		Run: func(cmd *cobra.Command, args []string) {
			listClaudeSessions()
		},
	}
	claudesCmd.AddCommand(claudesListCmd)

	claudesKillCmd := &cobra.Command{
		Use:   "kill <task-id>",
		Short: "Kill a Claude session by task ID",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}
			if err := killClaudeSession(taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(err.Error()))
				os.Exit(1)
			}
			fmt.Println(successStyle.Render(fmt.Sprintf("Killed session for task %d", taskID)))
		},
	}
	claudesCmd.AddCommand(claudesKillCmd)

	claudesKillallCmd := &cobra.Command{
		Use:   "killall",
		Short: "Kill all Claude sessions",
		Run: func(cmd *cobra.Command, args []string) {
			count := killAllClaudeSessions()
			if count == 0 {
				fmt.Println(dimStyle.Render("No Claude sessions running"))
			} else {
				fmt.Println(successStyle.Render(fmt.Sprintf("Killed %d session(s)", count)))
			}
		},
	}
	claudesCmd.AddCommand(claudesKillallCmd)

	rootCmd.AddCommand(claudesCmd)

	// Delete subcommand - delete a task, kill its Claude session, and remove worktree
	deleteCmd := &cobra.Command{
		Use:   "delete <task-id>",
		Short: "Delete a task, kill its Claude session, and remove its worktree",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			// Get task info for confirmation
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			task, err := database.GetTask(taskID)
			database.Close()
			if err != nil || task == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found", taskID)))
				os.Exit(1)
			}

			// Confirm unless --force flag is set
			force, _ := cmd.Flags().GetBool("force")
			if !force {
				fmt.Printf("Delete task #%d: %s? [y/N] ", taskID, task.Title)
				reader := bufio.NewReader(os.Stdin)
				response, _ := reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response != "y" && response != "yes" {
					fmt.Println("Cancelled")
					return
				}
			}

			if err := deleteTask(taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			fmt.Println(successStyle.Render(fmt.Sprintf("Deleted task #%d", taskID)))
		},
	}
	deleteCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	rootCmd.AddCommand(deleteCmd)

	// Create subcommand - create a new task from command line
	createCmd := &cobra.Command{
		Use:   "create <title>",
		Short: "Create a new task",
		Long: `Create a new task from the command line.

Examples:
  task create "Fix login bug"
  task create "Add dark mode" --type code --project myapp
  task create "Write documentation" --body "Document the API endpoints" --execute`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			title := args[0]
			body, _ := cmd.Flags().GetString("body")
			taskType, _ := cmd.Flags().GetString("type")
			project, _ := cmd.Flags().GetString("project")
			execute, _ := cmd.Flags().GetBool("execute")
			outputJSON, _ := cmd.Flags().GetBool("json")

			// Set defaults
			if taskType == "" {
				taskType = db.TypeCode
			}

			// Open database
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			// Validate task type against database types
			if taskType == "" {
				taskType = db.TypeCode // Default to code if not specified
			}
			taskTypes, _ := database.ListTaskTypes()
			validType := false
			var typeNames []string
			for _, t := range taskTypes {
				typeNames = append(typeNames, t.Name)
				if t.Name == taskType {
					validType = true
				}
			}
			if !validType {
				if len(typeNames) > 0 {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid type. Must be one of: "+strings.Join(typeNames, ", ")))
				} else {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid type. Must be: code, writing, or thinking"))
				}
				os.Exit(1)
			}

			// If project not specified, try to detect from cwd
			if project == "" {
				if cwd, err := os.Getwd(); err == nil {
					if p, err := database.GetProjectByPath(cwd); err == nil && p != nil {
						project = p.Name
					}
				}
			}

			// Set initial status
			status := db.StatusBacklog
			if execute {
				status = db.StatusQueued
			}

			// Create the task
			task := &db.Task{
				Title:   title,
				Body:    body,
				Status:  status,
				Type:    taskType,
				Project: project,
			}

			if err := database.CreateTask(task); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			if outputJSON {
				output := map[string]interface{}{
					"id":      task.ID,
					"title":   task.Title,
					"status":  task.Status,
					"type":    task.Type,
					"project": task.Project,
				}
				jsonBytes, _ := json.Marshal(output)
				fmt.Println(string(jsonBytes))
			} else {
				msg := fmt.Sprintf("Created task #%d: %s", task.ID, task.Title)
				if execute {
					msg += " (queued for execution)"
				}
				fmt.Println(successStyle.Render(msg))
			}
		},
	}
	createCmd.Flags().String("body", "", "Task body/description")
	createCmd.Flags().StringP("type", "t", "", "Task type: code, writing, thinking (default: code)")
	createCmd.Flags().StringP("project", "p", "", "Project name (auto-detected from cwd if not specified)")
	createCmd.Flags().BoolP("execute", "x", false, "Queue task for immediate execution")
	createCmd.Flags().Bool("json", false, "Output in JSON format")
	rootCmd.AddCommand(createCmd)

	// List subcommand - list tasks
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Long: `List tasks with optional filtering.

Examples:
  task list
  task list --status queued
  task list --project myapp
  task list --pr           # Show PR/CI status
  task list --all --json`,
		Run: func(cmd *cobra.Command, args []string) {
			status, _ := cmd.Flags().GetString("status")
			project, _ := cmd.Flags().GetString("project")
			taskType, _ := cmd.Flags().GetString("type")
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			outputJSON, _ := cmd.Flags().GetBool("json")
			showPR, _ := cmd.Flags().GetBool("pr")

			// Open database
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			opts := db.ListTasksOptions{
				Status:        status,
				Project:       project,
				Type:          taskType,
				Limit:         limit,
				IncludeClosed: all,
			}

			tasks, err := database.ListTasks(opts)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			// Fetch PR info if requested
			var prCache *github.PRCache
			var cfg *config.Config
			prInfoMap := make(map[int64]*github.PRInfo)
			if showPR {
				prCache = github.NewPRCache()
				cfg = config.New(database)
				for _, t := range tasks {
					if t.BranchName != "" {
						repoDir := t.WorktreePath
						if repoDir == "" {
							repoDir = cfg.GetProjectDir(t.Project)
						}
						if prInfo := prCache.GetPRForBranch(repoDir, t.BranchName); prInfo != nil {
							prInfoMap[t.ID] = prInfo
						}
					}
				}
			}

			if outputJSON {
				var output []map[string]interface{}
				for _, t := range tasks {
					item := map[string]interface{}{
						"id":         t.ID,
						"title":      t.Title,
						"status":     t.Status,
						"type":       t.Type,
						"project":    t.Project,
						"created_at": t.CreatedAt.Time.Format(time.RFC3339),
					}
					// Add PR info to JSON output if available
					if prInfo, ok := prInfoMap[t.ID]; ok {
						item["pr"] = map[string]interface{}{
							"number":       prInfo.Number,
							"url":          prInfo.URL,
							"state":        string(prInfo.State),
							"check_state":  string(prInfo.CheckState),
							"description":  prInfo.StatusDescription(),
						}
					}
					output = append(output, item)
				}
				jsonBytes, _ := json.Marshal(output)
				fmt.Println(string(jsonBytes))
			} else {
				if len(tasks) == 0 {
					fmt.Println(dimStyle.Render("No tasks found"))
					return
				}

				// Define status colors
				statusStyle := func(status string) lipgloss.Style {
					switch status {
					case db.StatusQueued:
						return lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
					case db.StatusProcessing:
						return lipgloss.NewStyle().Foreground(lipgloss.Color("#3B82F6"))
					case db.StatusBlocked:
						return lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
					case db.StatusDone:
						return lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
					default:
						return lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
					}
				}

				// PR status styling
				prStatusStyle := func(prInfo *github.PRInfo) string {
					if prInfo == nil {
						return ""
					}
					var icon, desc string
					var color lipgloss.Color
					switch prInfo.State {
					case github.PRStateMerged:
						icon, desc, color = "M", "merged", lipgloss.Color("#C678DD")
					case github.PRStateClosed:
						icon, desc, color = "X", "closed", lipgloss.Color("#EF4444")
					case github.PRStateDraft:
						icon, desc, color = "D", "draft", lipgloss.Color("#6B7280")
					case github.PRStateOpen:
						switch prInfo.CheckState {
						case github.CheckStatePassing:
							if prInfo.Mergeable == "MERGEABLE" {
								icon, desc, color = "R", "ready", lipgloss.Color("#10B981")
							} else if prInfo.Mergeable == "CONFLICTING" {
								icon, desc, color = "C", "conflicts", lipgloss.Color("#EF4444")
							} else {
								icon, desc, color = "P", "passing", lipgloss.Color("#10B981")
							}
						case github.CheckStateFailing:
							icon, desc, color = "F", "failing", lipgloss.Color("#EF4444")
						case github.CheckStatePending:
							icon, desc, color = "W", "running", lipgloss.Color("#F59E0B")
						default:
							icon, desc, color = "O", "open", lipgloss.Color("#10B981")
						}
					}
					badge := lipgloss.NewStyle().
						Foreground(lipgloss.Color("#FFFFFF")).
						Background(color).
						Bold(true).
						Render(icon)
					descStyled := lipgloss.NewStyle().Foreground(color).Render(desc)
					return fmt.Sprintf(" %s %s", badge, descStyled)
				}

				for _, t := range tasks {
					id := dimStyle.Render(fmt.Sprintf("#%-4d", t.ID))
					status := statusStyle(t.Status).Render(fmt.Sprintf("%-10s", t.Status))
					project := ""
					if t.Project != "" {
						project = dimStyle.Render(fmt.Sprintf("[%s] ", t.Project))
					}
					prStatus := ""
					if showPR {
						prStatus = prStatusStyle(prInfoMap[t.ID])
					}
					fmt.Printf("%s %s %s%s%s\n", id, status, project, t.Title, prStatus)
				}
			}
		},
	}
	listCmd.Flags().StringP("status", "s", "", "Filter by status: backlog, queued, processing, blocked, done")
	listCmd.Flags().StringP("project", "p", "", "Filter by project")
	listCmd.Flags().StringP("type", "t", "", "Filter by type: code, writing, thinking")
	listCmd.Flags().BoolP("all", "a", false, "Include completed tasks")
	listCmd.Flags().IntP("limit", "n", 50, "Maximum number of tasks to return")
	listCmd.Flags().Bool("json", false, "Output in JSON format")
	listCmd.Flags().Bool("pr", false, "Show PR/CI status (requires network)")
	rootCmd.AddCommand(listCmd)

	// Show subcommand - show task details
	showCmd := &cobra.Command{
		Use:   "show <task-id>",
		Short: "Show task details",
		Long: `Show detailed information about a task.

Examples:
  task show 42
  task show 42 --json
  task show 42 --logs`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			outputJSON, _ := cmd.Flags().GetBool("json")
			showLogs, _ := cmd.Flags().GetBool("logs")

			// Open database
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			task, err := database.GetTask(taskID)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if task == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found", taskID)))
				os.Exit(1)
			}

			// Fetch PR info if task has a branch
			var prInfo *github.PRInfo
			if task.BranchName != "" {
				cfg := config.New(database)
				repoDir := task.WorktreePath
				if repoDir == "" {
					repoDir = cfg.GetProjectDir(task.Project)
				}
				prCache := github.NewPRCache()
				prInfo = prCache.GetPRForBranch(repoDir, task.BranchName)
			}

			if outputJSON {
				output := map[string]interface{}{
					"id":         task.ID,
					"title":      task.Title,
					"body":       task.Body,
					"status":     task.Status,
					"type":       task.Type,
					"project":    task.Project,
					"worktree":   task.WorktreePath,
					"branch":     task.BranchName,
					"created_at": task.CreatedAt.Time.Format(time.RFC3339),
					"updated_at": task.UpdatedAt.Time.Format(time.RFC3339),
				}
				if task.StartedAt != nil {
					output["started_at"] = task.StartedAt.Time.Format(time.RFC3339)
				}
				if task.CompletedAt != nil {
					output["completed_at"] = task.CompletedAt.Time.Format(time.RFC3339)
				}
				// Add PR info to JSON output
				if prInfo != nil {
					output["pr"] = map[string]interface{}{
						"number":       prInfo.Number,
						"url":          prInfo.URL,
						"state":        string(prInfo.State),
						"check_state":  string(prInfo.CheckState),
						"description":  prInfo.StatusDescription(),
						"mergeable":    prInfo.Mergeable,
					}
				}
				if showLogs {
					logs, _ := database.GetTaskLogs(taskID, 1000)
					var logEntries []map[string]interface{}
					for _, l := range logs {
						logEntries = append(logEntries, map[string]interface{}{
							"type":       l.LineType,
							"content":    l.Content,
							"created_at": l.CreatedAt.Time.Format(time.RFC3339),
						})
					}
					output["logs"] = logEntries
				}
				jsonBytes, _ := json.MarshalIndent(output, "", "  ")
				fmt.Println(string(jsonBytes))
			} else {
				// Header
				fmt.Printf("%s %s\n", boldStyle.Render(fmt.Sprintf("Task #%d:", task.ID)), task.Title)
				fmt.Println(strings.Repeat("─", 50))

				// Status line
				statusColor := lipgloss.Color("#6B7280")
				switch task.Status {
				case db.StatusQueued:
					statusColor = lipgloss.Color("#F59E0B")
				case db.StatusProcessing:
					statusColor = lipgloss.Color("#3B82F6")
				case db.StatusBlocked:
					statusColor = lipgloss.Color("#EF4444")
				case db.StatusDone:
					statusColor = lipgloss.Color("#10B981")
				}
				fmt.Printf("Status:   %s\n", lipgloss.NewStyle().Foreground(statusColor).Render(task.Status))
				fmt.Printf("Type:     %s\n", task.Type)
				if task.Project != "" {
					fmt.Printf("Project:  %s\n", task.Project)
				}

				// Timestamps
				fmt.Printf("Created:  %s\n", task.CreatedAt.Time.Format("2006-01-02 15:04:05"))
				if task.StartedAt != nil {
					fmt.Printf("Started:  %s\n", task.StartedAt.Time.Format("2006-01-02 15:04:05"))
				}
				if task.CompletedAt != nil {
					fmt.Printf("Completed: %s\n", task.CompletedAt.Time.Format("2006-01-02 15:04:05"))
				}

				// Worktree info
				if task.WorktreePath != "" {
					fmt.Printf("Worktree: %s\n", task.WorktreePath)
				}
				if task.BranchName != "" {
					fmt.Printf("Branch:   %s\n", task.BranchName)
				}

				// PR info
				if prInfo != nil {
					fmt.Printf("PR:       #%d %s\n", prInfo.Number, prInfo.URL)
					// PR status with color
					var prStatusColor lipgloss.Color
					switch prInfo.State {
					case github.PRStateMerged:
						prStatusColor = lipgloss.Color("#C678DD")
					case github.PRStateClosed:
						prStatusColor = lipgloss.Color("#EF4444")
					case github.PRStateDraft:
						prStatusColor = lipgloss.Color("#6B7280")
					case github.PRStateOpen:
						switch prInfo.CheckState {
						case github.CheckStatePassing:
							prStatusColor = lipgloss.Color("#10B981")
						case github.CheckStateFailing:
							prStatusColor = lipgloss.Color("#EF4444")
						case github.CheckStatePending:
							prStatusColor = lipgloss.Color("#F59E0B")
						default:
							prStatusColor = lipgloss.Color("#10B981")
						}
					}
					prStatusStyled := lipgloss.NewStyle().Foreground(prStatusColor).Render(prInfo.StatusDescription())
					fmt.Printf("CI:       %s\n", prStatusStyled)
				}

				// Body
				if task.Body != "" {
					fmt.Println()
					fmt.Println(boldStyle.Render("Description:"))
					fmt.Println(task.Body)
				}

				// Logs
				if showLogs {
					logs, _ := database.GetTaskLogs(taskID, 100)
					if len(logs) > 0 {
						fmt.Println()
						fmt.Println(boldStyle.Render("Recent Logs:"))
						for _, l := range logs {
							prefix := ""
							switch l.LineType {
							case "system":
								prefix = dimStyle.Render("[system] ")
							case "error":
								prefix = errorStyle.Render("[error] ")
							case "tool":
								prefix = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B5CF6")).Render("[tool] ")
							}
							fmt.Printf("%s%s\n", prefix, truncate(l.Content, 100))
						}
					}
				}
			}
		},
	}
	showCmd.Flags().Bool("json", false, "Output in JSON format")
	showCmd.Flags().Bool("logs", false, "Show task logs")
	rootCmd.AddCommand(showCmd)

	// Update subcommand - update task fields
	updateCmd := &cobra.Command{
		Use:   "update <task-id>",
		Short: "Update a task",
		Long: `Update task fields.

Examples:
  task update 42 --title "New title"
  task update 42 --body "Updated description"`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			title, _ := cmd.Flags().GetString("title")
			body, _ := cmd.Flags().GetString("body")
			taskType, _ := cmd.Flags().GetString("type")
			project, _ := cmd.Flags().GetString("project")

			// Open database
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			// Validate task type against database types if provided
			if taskType != "" {
				taskTypes, _ := database.ListTaskTypes()
				validType := false
				var typeNames []string
				for _, t := range taskTypes {
					typeNames = append(typeNames, t.Name)
					if t.Name == taskType {
						validType = true
					}
				}
				if !validType {
					if len(typeNames) > 0 {
						fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid type. Must be one of: "+strings.Join(typeNames, ", ")))
					} else {
						fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid type. Must be: code, writing, or thinking"))
					}
					os.Exit(1)
				}
			}

			task, err := database.GetTask(taskID)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if task == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found", taskID)))
				os.Exit(1)
			}

			// Update fields if specified
			if title != "" {
				task.Title = title
			}
			if cmd.Flags().Changed("body") {
				task.Body = body
			}
			if taskType != "" {
				task.Type = taskType
			}
			if cmd.Flags().Changed("project") {
				task.Project = project
			}

			if err := database.UpdateTask(task); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render(fmt.Sprintf("Updated task #%d", taskID)))
		},
	}
	updateCmd.Flags().String("title", "", "Update task title")
	updateCmd.Flags().String("body", "", "Update task body/description")
	updateCmd.Flags().StringP("type", "t", "", "Update task type: code, writing, thinking")
	updateCmd.Flags().StringP("project", "p", "", "Update project name")
	rootCmd.AddCommand(updateCmd)

	// Execute subcommand - queue a task for execution
	executeCmd := &cobra.Command{
		Use:     "execute <task-id>",
		Aliases: []string{"queue", "run"},
		Short:   "Queue a task for execution",
		Long: `Queue a task to be executed by the daemon.

Examples:
  task execute 42
  task queue 42
  task run 42`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			// Open database
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			task, err := database.GetTask(taskID)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if task == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found", taskID)))
				os.Exit(1)
			}

			// Check if already queued/processing
			if task.Status == db.StatusQueued {
				fmt.Println(dimStyle.Render(fmt.Sprintf("Task #%d is already queued", taskID)))
				return
			}
			if task.Status == db.StatusProcessing {
				fmt.Println(dimStyle.Render(fmt.Sprintf("Task #%d is already processing", taskID)))
				return
			}

			if err := database.UpdateTaskStatus(taskID, db.StatusQueued); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render(fmt.Sprintf("Queued task #%d: %s", taskID, task.Title)))
		},
	}
	rootCmd.AddCommand(executeCmd)

	// Close subcommand - mark a task as done
	closeCmd := &cobra.Command{
		Use:     "close <task-id>",
		Aliases: []string{"done", "complete"},
		Short:   "Mark a task as done",
		Long: `Mark a task as completed.

Examples:
  task close 42
  task done 42
  task complete 42`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			// Open database
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			task, err := database.GetTask(taskID)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if task == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found", taskID)))
				os.Exit(1)
			}

			if task.Status == db.StatusDone {
				fmt.Println(dimStyle.Render(fmt.Sprintf("Task #%d is already done", taskID)))
				return
			}

			// Note: We intentionally do NOT kill the Claude session when closing a task.
			// The tmux window is kept around so users can review Claude's work.
			// Use 'task claudes kill <id>' or 'task delete <id>' to clean up windows.

			if err := database.UpdateTaskStatus(taskID, db.StatusDone); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render(fmt.Sprintf("Closed task #%d: %s", taskID, task.Title)))
		},
	}
	rootCmd.AddCommand(closeCmd)

	// Retry subcommand - retry a blocked/failed task
	retryCmd := &cobra.Command{
		Use:   "retry <task-id>",
		Short: "Retry a blocked or failed task",
		Long: `Retry a task that is blocked or failed, optionally with feedback.

Examples:
  task retry 42
  task retry 42 --feedback "Try a different approach"
  task retry 42 -m "Focus on the error handling"`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			feedback, _ := cmd.Flags().GetString("feedback")

			// Open database
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			task, err := database.GetTask(taskID)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if task == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found", taskID)))
				os.Exit(1)
			}

			if err := database.RetryTask(taskID, feedback); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render(fmt.Sprintf("Retrying task #%d: %s", taskID, task.Title)))
		},
	}
	retryCmd.Flags().StringP("feedback", "m", "", "Feedback for the retry")
	rootCmd.AddCommand(retryCmd)

	// Cloud subcommand
	rootCmd.AddCommand(createCloudCommand())

	// Sprite subcommand (cloud execution via Fly.io Sprites)
	rootCmd.AddCommand(createSpriteCommand())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
}

// execInTmux re-executes the current command inside a new tmux session.
// Creates a split-pane layout with the task TUI on top and Claude on bottom.
func execInTmux() error {
	// Get the executable and rebuild the command
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}

	// Get unique session name for this instance
	sessionName := getUISessionName()
	sessionID := getSessionID()

	// Build command with all original args
	args := append([]string{executable}, os.Args[1:]...)
	cmdStr := strings.Join(args, " ")

	// Set TASK_SESSION_ID env var so child processes use the same session ID
	envCmd := fmt.Sprintf("TASK_SESSION_ID=%s %s", sessionID, cmdStr)

	// Check if session already exists
	if osexec.Command("tmux", "has-session", "-t", sessionName).Run() == nil {
		// Session exists, attach to it instead
		cmd := osexec.Command("tmux", "attach-session", "-t", sessionName)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Get current working directory for Claude
	cwd, _ := os.Getwd()

	// Create detached session first so we can configure it
	// The task TUI runs in the main (top) pane
	if err := osexec.Command("tmux", "new-session", "-d", "-s", sessionName, "-c", cwd, envCmd).Run(); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}

	// Configure status bar
	osexec.Command("tmux", "set-option", "-t", sessionName, "status", "on").Run()
	osexec.Command("tmux", "set-option", "-t", sessionName, "status-style", "bg=#1e293b,fg=#94a3b8").Run()
	osexec.Command("tmux", "set-option", "-t", sessionName, "status-left", " ").Run()
	osexec.Command("tmux", "set-option", "-t", sessionName, "status-right", " ").Run()

	// Enable pane border labels
	osexec.Command("tmux", "set-option", "-t", sessionName, "pane-border-status", "top").Run()
	osexec.Command("tmux", "set-option", "-t", sessionName, "pane-border-format", " #{pane_title} ").Run()
	osexec.Command("tmux", "set-option", "-t", sessionName, "pane-border-style", "fg=#374151").Run()
	osexec.Command("tmux", "set-option", "-t", sessionName, "pane-active-border-style", "fg=#61AFEF").Run()

	// Set pane title for the task TUI
	osexec.Command("tmux", "select-pane", "-t", sessionName+":.0", "-T", "Tasks").Run()

	// Now attach to the session
	cmd := osexec.Command("tmux", "attach-session", "-t", sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runLocal runs the TUI locally with a local SQLite database.
func runLocal(dangerousMode bool) error {
	// Ensure daemon is running
	if err := ensureDaemonRunning(dangerousMode); err != nil {
		fmt.Fprintln(os.Stderr, dimStyle.Render("Warning: could not start daemon: "+err.Error()))
	}

	// Open database
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	fmt.Fprintln(os.Stderr, dimStyle.Render("Using local database: "+dbPath))

	// Load config from database
	cfg := config.New(database)

	// Create a non-running executor (just for status display)
	// The actual execution happens in the daemon process
	exec := executor.New(database, cfg)

	// Get current working directory for project detection
	cwd, _ := os.Getwd()

	// Create and run TUI
	model := ui.NewAppModel(database, exec, cwd)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}

	// Kill task-ui tmux session on exit (if we're in it)
	// This cleans up the session that was created by execInTmux()
	if os.Getenv("TMUX") != "" {
		sessionName := getUISessionName()
		// Check if we're in this instance's session
		tmuxCmd := osexec.Command("tmux", "display-message", "-p", "#{session_name}")
		out, err := tmuxCmd.Output()
		if err == nil && strings.TrimSpace(string(out)) == sessionName {
			osexec.Command("tmux", "kill-session", "-t", sessionName).Run()
		}
	}

	return nil
}

// ensureDaemonRunning starts the daemon if it's not already running.
// If dangerousMode is true, sets TASK_DANGEROUS_MODE=1 for the daemon.
// If the daemon is running with a different mode, it will be restarted.
func ensureDaemonRunning(dangerousMode bool) error {
	pidFile := getPidFilePath()
	modeFile := pidFile + ".mode"

	// Check if daemon is already running
	if pid, err := readPidFile(pidFile); err == nil {
		if processExists(pid) {
			// Check if running with correct mode
			currentMode, _ := os.ReadFile(modeFile)
			wantMode := "safe"
			if dangerousMode {
				wantMode = "dangerous"
			}
			if string(currentMode) == wantMode {
				return nil // Already running with correct mode
			}
			// Mode mismatch - restart daemon
			fmt.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf("Restarting daemon (switching to %s mode)...", wantMode)))
			stopDaemon()
			time.Sleep(100 * time.Millisecond)
		} else {
			// Stale pid file, remove it
			os.Remove(pidFile)
			os.Remove(modeFile)
		}
	}

	// Start daemon as background process
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}

	cmd := osexec.Command(executable, "daemon")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Pass session ID to daemon so it uses the same tmux sessions
	cmd.Env = append(os.Environ(), fmt.Sprintf("TASK_SESSION_ID=%s", getSessionID()))
	// Pass dangerous mode flag if enabled
	if dangerousMode {
		cmd.Env = append(cmd.Env, "TASK_DANGEROUS_MODE=1")
	}
	// Detach from parent process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// Write pid file
	if err := writePidFile(pidFile, cmd.Process.Pid); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}

	// Write mode file so we know what mode the daemon is running in
	modeStr := "safe"
	if dangerousMode {
		modeStr = "dangerous"
	}
	os.WriteFile(modeFile, []byte(modeStr), 0644)

	fmt.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf("Started daemon (pid %d, %s mode)", cmd.Process.Pid, modeStr)))
	return nil
}

func getPidFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "task", "daemon.pid")
}

func readPidFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, err
	}
	return pid, nil
}

func writePidFile(path string, pid int) error {
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0644)
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so we need to send signal 0
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func stopDaemon() error {
	pidFile := getPidFilePath()
	modeFile := pidFile + ".mode"
	pid, err := readPidFile(pidFile)
	if err != nil {
		return fmt.Errorf("daemon not running (no pid file)")
	}

	if !processExists(pid) {
		os.Remove(pidFile)
		os.Remove(modeFile)
		return fmt.Errorf("daemon not running (stale pid file)")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send signal: %w", err)
	}

	os.Remove(pidFile)
	os.Remove(modeFile)
	return nil
}

// runDaemon runs the background executor that processes queued tasks.
func runDaemon() error {
	pidFile := getPidFilePath()

	// Acquire exclusive lock on PID file to prevent duplicates
	lockFile, err := os.OpenFile(pidFile+".lock", os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("daemon already running (could not acquire lock)")
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	// Write our PID file
	if err := writePidFile(pidFile, os.Getpid()); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer os.Remove(pidFile)

	// Setup logger
	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		Prefix:          "task-daemon",
	})

	// Open database
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	logger.Info("Database opened", "path", dbPath)

	// Load config from database
	cfg := config.New(database)

	// Create executor with logging
	exec := executor.NewWithLogging(database, cfg, os.Stderr)

	// Start background executor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec.Start(ctx)

	logger.Info("Executor started, waiting for tasks...")

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Wait for signal
	sig := <-sigCh
	logger.Info("Received signal, shutting down", "signal", sig)
	exec.Stop()

	return nil
}

func connectRemote(host, port string) error {
	// Get SSH auth methods
	authMethods, err := getAuthMethods()
	if err != nil {
		return fmt.Errorf("auth setup: %w", err)
	}

	// Get current user
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	username := currentUser.Username

	config := &ssh.ClientConfig{
		User:            username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: proper host key verification
	}

	// Connect
	addr := net.JoinHostPort(host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()

	// Create session
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("session: %w", err)
	}
	defer session.Close()

	// Set up terminal
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		// Get terminal size
		width, height, err := term.GetSize(fd)
		if err != nil {
			width, height = 80, 24
		}

		// Set raw mode
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("raw mode: %w", err)
		}
		defer term.Restore(fd, oldState)

		// Request PTY
		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
		if err := session.RequestPty("xterm-256color", height, width, modes); err != nil {
			return fmt.Errorf("pty: %w", err)
		}
	}

	// Connect I/O
	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// Send current working directory for project detection
	if cwd, err := os.Getwd(); err == nil {
		session.Setenv("TASK_CWD", cwd)
	}

	// Start shell (taskd serves TUI directly)
	if err := session.Shell(); err != nil {
		return fmt.Errorf("shell: %w", err)
	}

	return session.Wait()
}

func getAuthMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Try SSH agent first
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			signers, err := agentClient.Signers()
			if err == nil && len(signers) > 0 {
				methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
			}
		}
	}

	// Try default key files
	home, _ := os.UserHomeDir()
	keyFiles := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
	}

	for _, keyFile := range keyFiles {
		if key, err := loadPrivateKey(keyFile); err == nil {
			methods = append(methods, ssh.PublicKeys(key))
		}
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH keys found (checked agent and ~/.ssh/id_*)")
	}

	return methods, nil
}

func loadPrivateKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

// ClaudeHookInput is the JSON structure Claude sends to hooks via stdin.
type ClaudeHookInput struct {
	SessionID          string `json:"session_id"`
	TranscriptPath     string `json:"transcript_path"`
	Cwd                string `json:"cwd"`
	HookEventName      string `json:"hook_event_name"`
	NotificationType   string `json:"notification_type,omitempty"`   // For Notification hooks
	Message            string `json:"message,omitempty"`             // General message field
	StopReason         string `json:"stop_reason,omitempty"`         // For Stop hooks
	Trigger            string `json:"trigger,omitempty"`             // For PreCompact hooks: "manual" or "auto"
	CustomInstructions string `json:"custom_instructions,omitempty"` // For PreCompact hooks: user-specified compaction instructions
}

// handleClaudeHook processes Claude Code hook callbacks.
// It reads hook data from stdin and updates task status accordingly.
func handleClaudeHook(hookEvent string) error {
	// Get task ID from environment (set by executor when launching Claude)
	taskIDStr := os.Getenv("TASK_ID")
	if taskIDStr == "" {
		return fmt.Errorf("TASK_ID not set")
	}
	var taskID int64
	if _, err := fmt.Sscanf(taskIDStr, "%d", &taskID); err != nil {
		return fmt.Errorf("invalid TASK_ID: %s", taskIDStr)
	}

	// Read hook input from stdin
	var input ClaudeHookInput
	decoder := json.NewDecoder(os.Stdin)
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode hook input: %w", err)
	}

	// Open database
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Log session ID once (on first hook call for this task)
	logSessionIDOnce(database, taskID, &input)

	// Handle based on hook event type
	switch hookEvent {
	case "PreToolUse":
		return handlePreToolUseHook(database, taskID, &input)
	case "PostToolUse":
		return handlePostToolUseHook(database, taskID, &input)
	case "Notification":
		return handleNotificationHook(database, taskID, &input)
	case "Stop":
		return handleStopHook(database, taskID, &input)
	case "PreCompact":
		return handlePreCompactHook(database, taskID, &input)
	default:
		// Unknown hook type, ignore
		return nil
	}
}

// logSessionIDOnce logs the Claude session ID for a task, but only once.
// It checks if a session ID log already exists to avoid duplicate entries.
func logSessionIDOnce(database *db.DB, taskID int64, input *ClaudeHookInput) {
	if input.SessionID == "" {
		return
	}

	// Check if we've already logged a session ID for this task
	logs, err := database.GetTaskLogs(taskID, 50)
	if err != nil {
		return
	}

	sessionPrefix := "Claude session: "
	for _, log := range logs {
		if log.LineType == "system" && strings.HasPrefix(log.Content, sessionPrefix) {
			// Already logged
			return
		}
	}

	// Log the session ID
	database.AppendTaskLog(taskID, "system", sessionPrefix+input.SessionID)
}

// handleNotificationHook handles Notification hooks from Claude.
func handleNotificationHook(database *db.DB, taskID int64, input *ClaudeHookInput) error {
	switch input.NotificationType {
	case "idle_prompt", "permission_prompt":
		// Claude is waiting for input - mark task as blocked
		task, err := database.GetTask(taskID)
		if err != nil {
			return err
		}
		// Only update if:
		// 1. Task has actually started (StartedAt is set)
		// 2. Currently processing (avoid overwriting other states)
		if task != nil && task.StartedAt != nil && task.Status == db.StatusProcessing {
			database.UpdateTaskStatus(taskID, db.StatusBlocked)
			msg := "Waiting for user input"
			if input.NotificationType == "permission_prompt" {
				msg = "Waiting for permission"
			}
			database.AppendTaskLog(taskID, "system", msg)
		}
	}
	return nil
}

// handleStopHook handles Stop hooks from Claude (agent finished responding).
// The Stop hook fires when Claude Code finishes a response, with stop_reason indicating why:
// - "end_turn": Claude finished and is waiting for user input → task should be "blocked"
// - "tool_use": Claude finished with a tool call that's about to execute → task stays "processing"
//   (PreToolUse/PostToolUse hooks handle the actual tool execution state tracking)
func handleStopHook(database *db.DB, taskID int64, input *ClaudeHookInput) error {
	task, err := database.GetTask(taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return nil
	}

	// Only manage status if the task has actually started (StartedAt is set)
	// This prevents status changes for tasks that are new or in backlog/queued
	if task.StartedAt == nil {
		return nil
	}

	switch input.StopReason {
	case "end_turn":
		// Claude finished its turn and is waiting for user input
		// This is the key transition: processing → blocked (needs input)
		if task.Status == db.StatusProcessing {
			database.UpdateTaskStatus(taskID, db.StatusBlocked)
			database.AppendTaskLog(taskID, "system", "Waiting for user input")
		}
	case "tool_use":
		// Claude stopped because it's about to execute a tool
		// The PreToolUse hook will handle ensuring the task is in "processing" state
		// No state change needed here - task should already be or will be "processing"
	}

	return nil
}

// handlePreToolUseHook handles PreToolUse hooks from Claude (before tool execution).
// This is the most reliable indicator that Claude is actively working.
func handlePreToolUseHook(database *db.DB, taskID int64, input *ClaudeHookInput) error {
	task, err := database.GetTask(taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return nil
	}

	// Only manage status if the task has actually started (StartedAt is set)
	// This prevents status changes for tasks that are new or in backlog/queued
	if task.StartedAt == nil {
		return nil
	}

	// When Claude is about to use a tool, the task should be "processing"
	// This handles the case where:
	// 1. Task was blocked (waiting for input) and user responded
	// 2. Task was in any other state but Claude is now actively working
	if task.Status == db.StatusBlocked {
		database.UpdateTaskStatus(taskID, db.StatusProcessing)
		database.AppendTaskLog(taskID, "system", "Claude resumed working")
	}

	return nil
}

// handlePostToolUseHook handles PostToolUse hooks from Claude (after tool execution).
// This confirms Claude is still actively working after a tool completes.
func handlePostToolUseHook(database *db.DB, taskID int64, input *ClaudeHookInput) error {
	task, err := database.GetTask(taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return nil
	}

	// Only manage status if the task has actually started (StartedAt is set)
	// This prevents status changes for tasks that are new or in backlog/queued
	if task.StartedAt == nil {
		return nil
	}

	// After a tool completes, Claude is still working (will process tool results)
	// Ensure task remains in "processing" state
	if task.Status == db.StatusBlocked {
		database.UpdateTaskStatus(taskID, db.StatusProcessing)
		database.AppendTaskLog(taskID, "system", "Claude processing tool results")
	}

	return nil
}

// handlePreCompactHook handles PreCompact hooks from Claude (before context compaction).
// This hook fires when Claude is about to compact its context, either manually (/compact)
// or automatically when the context window approaches its limit.
//
// We use this to:
// 1. Read the current conversation from the transcript file
// 2. Extract key context that should persist across compaction
// 3. Store it in the database for retrieval on task resume
//
// This is better than letting Claude's normal compaction proceed alone because:
// - We can persist important task context in our database
// - We can provide this context back when the task resumes
// - We can supplement Claude's summary with task-specific metadata
// - The saved context survives Claude session cleanup
func handlePreCompactHook(database *db.DB, taskID int64, input *ClaudeHookInput) error {
	// Read the full transcript content before compaction
	transcript, preTokens, err := readTranscriptContent(input.TranscriptPath)
	if err != nil {
		// Log error but don't fail - compaction should still proceed
		database.AppendTaskLog(taskID, "system", fmt.Sprintf("Warning: Could not read transcript: %v", err))
		return nil
	}

	// Store the full transcript in the database
	compactionSummary := &db.CompactionSummary{
		TaskID:             taskID,
		SessionID:          input.SessionID,
		Trigger:            input.Trigger,
		PreTokens:          preTokens,
		Summary:            transcript, // Full JSONL transcript content
		CustomInstructions: input.CustomInstructions,
	}

	if err := database.SaveCompactionSummary(compactionSummary); err != nil {
		database.AppendTaskLog(taskID, "system", fmt.Sprintf("Warning: Could not save transcript: %v", err))
		return nil
	}

	// Log the compaction event
	triggerType := "auto"
	if input.Trigger == "manual" {
		triggerType = "manual (/compact)"
	}
	database.AppendTaskLog(taskID, "system", fmt.Sprintf("Context compacted (%s) - saved %d bytes of transcript", triggerType, len(transcript)))

	return nil
}

// readTranscriptContent reads the full Claude transcript file content.
// Returns the complete JSONL content and estimated token count.
// Saving the full transcript preserves all context without arbitrary truncation,
// allowing us to reconstruct or re-summarize the conversation later if needed.
func readTranscriptContent(transcriptPath string) (string, int, error) {
	if transcriptPath == "" {
		return "", 0, fmt.Errorf("no transcript path provided")
	}

	content, err := os.ReadFile(transcriptPath)
	if err != nil {
		return "", 0, fmt.Errorf("read transcript: %w", err)
	}

	// Estimate tokens (rough: ~4 chars per token)
	preTokens := len(content) / 4

	return string(content), preTokens, nil
}

// tailClaudeLogs tails all claude session logs for debugging.
func tailClaudeLogs() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	projectsDir := filepath.Join(home, ".claude", "projects")

	// Find all .jsonl files
	pattern := filepath.Join(projectsDir, "*", "*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob: %w", err)
	}

	if len(files) == 0 {
		return fmt.Errorf("no claude session files found in %s", projectsDir)
	}

	fmt.Fprintf(os.Stderr, "%s\n", dimStyle.Render(fmt.Sprintf("Watching %d session files...", len(files))))
	fmt.Fprintf(os.Stderr, "%s\n\n", dimStyle.Render("Press Ctrl+C to stop"))

	// Track file positions
	positions := make(map[string]int64)

	// Initialize positions to end of file (only show new content)
	for _, f := range files {
		info, err := os.Stat(f)
		if err == nil {
			positions[f] = info.Size()
		}
	}

	// Handle interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			fmt.Println()
			return nil
		case <-ticker.C:
			// Re-glob to catch new files
			files, _ = filepath.Glob(pattern)

			for _, f := range files {
				// Skip internal agent files (Claude Code sub-agents)
				if strings.HasPrefix(filepath.Base(f), "agent-") {
					continue
				}
				pos := positions[f]
				newPos, err := tailFile(f, pos)
				if err == nil {
					positions[f] = newPos
				}
			}
		}
	}
}

// tailFile reads new content from a file starting at the given position.
func tailFile(path string, pos int64) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return pos, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return pos, err
	}

	// File was truncated or rotated
	if info.Size() < pos {
		pos = 0
	}

	// No new content
	if info.Size() == pos {
		return pos, nil
	}

	// Seek to last position
	if _, err := file.Seek(pos, io.SeekStart); err != nil {
		return pos, err
	}

	// Extract project name from path for display
	project := filepath.Base(filepath.Dir(path))
	projectStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Parse JSON and extract useful content
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		// Format output based on entry type
		output := formatLogEntry(entry)
		if output != "" {
			fmt.Printf("%s %s\n", projectStyle.Render("["+project+"]"), output)
		}
	}

	return info.Size(), nil
}

// formatLogEntry formats a claude log entry for display.
// Only shows meaningful content: user input and claude's text responses.
// Tool calls and results are hidden - attach to tmux or view claude logs for details.
func formatLogEntry(entry map[string]interface{}) string {
	msgType, _ := entry["type"].(string)

	switch msgType {
	case "user":
		if msg, ok := entry["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				return boldStyle.Render("USER: ") + truncate(content, 200)
			}
		}
	case "assistant":
		if msg, ok := entry["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].([]interface{}); ok {
				// Only extract text blocks, skip tool calls
				var textParts []string
				for _, c := range content {
					if block, ok := c.(map[string]interface{}); ok {
						if text, ok := block["text"].(string); ok {
							text = strings.TrimSpace(text)
							if text != "" {
								textParts = append(textParts, text)
							}
						}
						// Skip tool_use blocks entirely - they're noise
					}
				}
				if len(textParts) > 0 {
					return successStyle.Render("CLAUDE: ") + truncate(strings.Join(textParts, " "), 200)
				}
			}
		}
	// Skip "result" type - tool results are noise for high-level view
	}

	return ""
}

// truncate shortens a string to maxLen, adding ellipsis if needed.
func truncate(s string, maxLen int) string {
	// Replace newlines with spaces for single-line display
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")

	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// listClaudeSessions lists all running Claude task windows in task-daemon.
func listClaudeSessions() {
	sessions := getClaudeSessions()
	if len(sessions) == 0 {
		fmt.Println(dimStyle.Render("No Claude sessions running"))
		return
	}

	fmt.Printf("%s\n\n", boldStyle.Render("Running Claude Sessions (in task-daemon):"))
	for _, s := range sessions {
		fmt.Printf("  %s  %s\n",
			successStyle.Render(fmt.Sprintf("task-%d", s.taskID)),
			dimStyle.Render(s.info))
	}
}

type claudeSession struct {
	taskID int
	info   string
}

// getClaudeSessions returns all running task-* windows in task-daemon session.
func getClaudeSessions() []claudeSession {
	// List all windows in task-daemon session for this instance
	daemonSession := getDaemonSessionName()
	cmd := osexec.Command("tmux", "list-windows", "-t", daemonSession, "-F", "#{window_name}:#{window_activity}")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var sessions []claudeSession
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 1 {
			continue
		}

		name := parts[0]
		// Skip placeholder window
		if name == "_placeholder" {
			continue
		}

		if !strings.HasPrefix(name, "task-") {
			continue
		}

		var taskID int
		if _, err := fmt.Sscanf(name, "task-%d", &taskID); err != nil {
			continue
		}

		info := ""
		if len(parts) >= 2 {
			// Parse activity timestamp
			var activity int64
			fmt.Sscanf(parts[1], "%d", &activity)
			if activity > 0 {
				t := time.Unix(activity, 0)
				info = fmt.Sprintf("last activity %s", t.Format("15:04:05"))
			}
		}

		sessions = append(sessions, claudeSession{taskID: taskID, info: info})
	}

	return sessions
}

// killClaudeSession kills a specific task's tmux window in task-daemon.
func killClaudeSession(taskID int) error {
	daemonSession := getDaemonSessionName()
	windowName := fmt.Sprintf("task-%d", taskID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Check if window exists
	if err := osexec.Command("tmux", "list-panes", "-t", windowTarget).Run(); err != nil {
		return fmt.Errorf("no window for task %d", taskID)
	}

	// Kill the window
	if err := osexec.Command("tmux", "kill-window", "-t", windowTarget).Run(); err != nil {
		return fmt.Errorf("failed to kill window: %w", err)
	}

	return nil
}

// killAllClaudeSessions kills all task-* tmux windows in task-daemon.
func killAllClaudeSessions() int {
	sessions := getClaudeSessions()
	count := 0
	for _, s := range sessions {
		if err := killClaudeSession(s.taskID); err == nil {
			count++
		}
	}
	return count
}

// deleteTask deletes a task, its Claude session, and its worktree.
func deleteTask(taskID int64) error {
	// Open database
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Get task to check for worktree
	task, err := database.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task #%d not found", taskID)
	}

	// Kill Claude session if running (ignore errors - session may not exist)
	killClaudeSession(int(taskID))

	// Clean up worktree if it exists
	if task.WorktreePath != "" {
		cfg := config.New(database)
		exec := executor.New(database, cfg)
		if err := exec.CleanupWorktree(task); err != nil {
			// Log warning but continue with deletion
			fmt.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf("Warning: could not remove worktree: %v", err)))
		}
	}

	// Delete from database
	if err := database.DeleteTask(taskID); err != nil {
		return fmt.Errorf("delete task: %w", err)
	}

	return nil
}
