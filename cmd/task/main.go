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

func main() {
	var (
		host  string
		port  string
		local bool
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
				if err := runLocal(); err != nil {
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

	rootCmd.Flags().StringVarP(&host, "host", "H", defaultHost, "Remote server host")
	rootCmd.Flags().StringVarP(&port, "port", "p", defaultPort, "Remote server port")
	rootCmd.Flags().BoolVarP(&local, "local", "l", false, "Run locally (use local database)")

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
  task create "Add dark mode" --type code --priority high --project myapp
  task create "Write documentation" --body "Document the API endpoints" --execute`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			title := args[0]
			body, _ := cmd.Flags().GetString("body")
			taskType, _ := cmd.Flags().GetString("type")
			project, _ := cmd.Flags().GetString("project")
			priority, _ := cmd.Flags().GetString("priority")
			execute, _ := cmd.Flags().GetBool("execute")
			outputJSON, _ := cmd.Flags().GetBool("json")

			// Set defaults
			if taskType == "" {
				taskType = db.TypeCode
			}
			if priority == "" {
				priority = "normal"
			}

			// Validate task type
			if taskType != db.TypeCode && taskType != db.TypeWriting && taskType != db.TypeThinking {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid type. Must be: code, writing, or thinking"))
				os.Exit(1)
			}

			// Validate priority
			if priority != "low" && priority != "normal" && priority != "high" {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid priority. Must be: low, normal, or high"))
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
				Title:    title,
				Body:     body,
				Status:   status,
				Type:     taskType,
				Project:  project,
				Priority: priority,
			}

			if err := database.CreateTask(task); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			if outputJSON {
				output := map[string]interface{}{
					"id":       task.ID,
					"title":    task.Title,
					"status":   task.Status,
					"type":     task.Type,
					"project":  task.Project,
					"priority": task.Priority,
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
	createCmd.Flags().String("priority", "", "Priority: low, normal, high (default: normal)")
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
  task list --project myapp --priority high
  task list --all --json`,
		Run: func(cmd *cobra.Command, args []string) {
			status, _ := cmd.Flags().GetString("status")
			project, _ := cmd.Flags().GetString("project")
			priority, _ := cmd.Flags().GetString("priority")
			taskType, _ := cmd.Flags().GetString("type")
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			outputJSON, _ := cmd.Flags().GetBool("json")

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
				Priority:      priority,
				Type:          taskType,
				Limit:         limit,
				IncludeClosed: all,
			}

			tasks, err := database.ListTasks(opts)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			if outputJSON {
				var output []map[string]interface{}
				for _, t := range tasks {
					output = append(output, map[string]interface{}{
						"id":         t.ID,
						"title":      t.Title,
						"status":     t.Status,
						"type":       t.Type,
						"project":    t.Project,
						"priority":   t.Priority,
						"created_at": t.CreatedAt.Time.Format(time.RFC3339),
					})
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

				priorityIndicator := func(priority string) string {
					switch priority {
					case "high":
						return lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Render("!")
					case "low":
						return lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render("↓")
					default:
						return " "
					}
				}

				for _, t := range tasks {
					id := dimStyle.Render(fmt.Sprintf("#%-4d", t.ID))
					status := statusStyle(t.Status).Render(fmt.Sprintf("%-10s", t.Status))
					priority := priorityIndicator(t.Priority)
					project := ""
					if t.Project != "" {
						project = dimStyle.Render(fmt.Sprintf("[%s] ", t.Project))
					}
					fmt.Printf("%s %s %s %s%s\n", id, status, priority, project, t.Title)
				}
			}
		},
	}
	listCmd.Flags().StringP("status", "s", "", "Filter by status: backlog, queued, processing, blocked, done")
	listCmd.Flags().StringP("project", "p", "", "Filter by project")
	listCmd.Flags().String("priority", "", "Filter by priority: low, normal, high")
	listCmd.Flags().StringP("type", "t", "", "Filter by type: code, writing, thinking")
	listCmd.Flags().BoolP("all", "a", false, "Include completed tasks")
	listCmd.Flags().IntP("limit", "n", 50, "Maximum number of tasks to return")
	listCmd.Flags().Bool("json", false, "Output in JSON format")
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

			if outputJSON {
				output := map[string]interface{}{
					"id":           task.ID,
					"title":        task.Title,
					"body":         task.Body,
					"status":       task.Status,
					"type":         task.Type,
					"project":      task.Project,
					"priority":     task.Priority,
					"worktree":     task.WorktreePath,
					"branch":       task.BranchName,
					"created_at":   task.CreatedAt.Time.Format(time.RFC3339),
					"updated_at":   task.UpdatedAt.Time.Format(time.RFC3339),
				}
				if task.StartedAt != nil {
					output["started_at"] = task.StartedAt.Time.Format(time.RFC3339)
				}
				if task.CompletedAt != nil {
					output["completed_at"] = task.CompletedAt.Time.Format(time.RFC3339)
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
				fmt.Printf("Priority: %s\n", task.Priority)
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
	showCmd.Flags().BoolP("logs", "l", false, "Show task logs")
	rootCmd.AddCommand(showCmd)

	// Update subcommand - update task fields
	updateCmd := &cobra.Command{
		Use:   "update <task-id>",
		Short: "Update a task",
		Long: `Update task fields.

Examples:
  task update 42 --title "New title"
  task update 42 --priority high
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
			priority, _ := cmd.Flags().GetString("priority")

			// Validate task type if provided
			if taskType != "" && taskType != db.TypeCode && taskType != db.TypeWriting && taskType != db.TypeThinking {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid type. Must be: code, writing, or thinking"))
				os.Exit(1)
			}

			// Validate priority if provided
			if priority != "" && priority != "low" && priority != "normal" && priority != "high" {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid priority. Must be: low, normal, or high"))
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
			if priority != "" {
				task.Priority = priority
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
	updateCmd.Flags().String("priority", "", "Update priority: low, normal, high")
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

			// Kill Claude session if running
			killClaudeSession(int(taskID))

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

	// Build command with all original args
	args := append([]string{executable}, os.Args[1:]...)
	cmdStr := strings.Join(args, " ")

	// Check if session already exists
	if osexec.Command("tmux", "has-session", "-t", "task-ui").Run() == nil {
		// Session exists, attach to it instead
		cmd := osexec.Command("tmux", "attach-session", "-t", "task-ui")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Get current working directory for Claude
	cwd, _ := os.Getwd()

	// Create detached session first so we can configure it
	// The task TUI runs in the main (top) pane
	if err := osexec.Command("tmux", "new-session", "-d", "-s", "task-ui", "-c", cwd, cmdStr).Run(); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}

	// Configure status bar
	osexec.Command("tmux", "set-option", "-t", "task-ui", "status", "on").Run()
	osexec.Command("tmux", "set-option", "-t", "task-ui", "status-style", "bg=#1e293b,fg=#94a3b8").Run()
	osexec.Command("tmux", "set-option", "-t", "task-ui", "status-left", " ").Run()
	osexec.Command("tmux", "set-option", "-t", "task-ui", "status-right", " ").Run()

	// Enable pane border labels
	osexec.Command("tmux", "set-option", "-t", "task-ui", "pane-border-status", "top").Run()
	osexec.Command("tmux", "set-option", "-t", "task-ui", "pane-border-format", " #{pane_title} ").Run()
	osexec.Command("tmux", "set-option", "-t", "task-ui", "pane-border-style", "fg=#374151").Run()
	osexec.Command("tmux", "set-option", "-t", "task-ui", "pane-active-border-style", "fg=#61AFEF").Run()

	// Set pane title for the task TUI
	osexec.Command("tmux", "select-pane", "-t", "task-ui:.0", "-T", "Tasks").Run()

	// Now attach to the session
	cmd := osexec.Command("tmux", "attach-session", "-t", "task-ui")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// buildCopilotClaudeCommand builds the claude command with copilot system prompt and permissions.
func buildCopilotClaudeCommand() string {
	systemPrompt := `You are the Task App Copilot - an AI assistant integrated into the task management application.

## Your Role
You help users manage their task queue by driving the task app UI in the tmux pane above you.
The task app is a terminal-based kanban board for managing AI coding tasks.

## Tmux Layout
- Session: task-ui
- Pane 0.0 (above): The task app TUI - a kanban board showing tasks in columns (Backlog, In Progress, Blocked, Done)
- Pane 0.1 (this pane): You, the copilot

## How to Control the Task App
Send keystrokes to the task app using: tmux send-keys -t task-ui:0.0 '<keys>'

### Key Bindings
- n: Create new task (opens form, type title, ctrl+s to submit)
- Enter: Open task details
- q/Esc: Go back / close
- h/l or Left/Right: Move between columns
- j/k or Down/Up: Move between tasks in a column
- Q: Queue selected task for execution
- r: Retry failed task
- d: Delete task (with confirmation)
- /: Filter tasks
- s: Open settings
- ?: Show help

### Creating a Task
1. tmux send-keys -t task-ui:0.0 'n'        # Open new task form
2. tmux send-keys -t task-ui:0.0 'Task title here'  # Type title
3. tmux send-keys -t task-ui:0.0 C-s        # Submit (Ctrl+S)
4. tmux send-keys -t task-ui:0.0 Enter      # Confirm (don't queue) or 'y' then Enter to queue

### Navigating
- To select a task: Use arrow keys or hjkl to navigate the kanban board
- To view details: Press Enter on selected task
- To go back: Press q or Esc

## Important Notes
- Always add small delays between commands (sleep 0.3) to let the UI update
- The task app polls for updates, so changes you make will appear shortly
- You can also directly interact with the SQLite database at ~/.local/share/task/tasks.db for queries
- Tasks are executed by Claude instances in separate tmux windows in the task-daemon session

## Be Helpful
- When users ask to create tasks, do it for them
- When users ask about task status, navigate to show them or query the database
- Offer suggestions for task management
- Be concise in your responses`

	// Escape single quotes in system prompt for shell
	escapedPrompt := strings.ReplaceAll(systemPrompt, "'", "'\"'\"'")

	// Build claude command with system prompt and dangerously skip permissions for Bash
	// This allows the copilot to send tmux commands without permission prompts
	return fmt.Sprintf("claude --system-prompt '%s' --dangerously-skip-permissions", escapedPrompt)
}

// runLocal runs the TUI locally with a local SQLite database.
func runLocal() error {
	// Ensure daemon is running
	if err := ensureDaemonRunning(); err != nil {
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
		// Check if we're in the task-ui session
		tmuxCmd := osexec.Command("tmux", "display-message", "-p", "#{session_name}")
		out, err := tmuxCmd.Output()
		if err == nil && strings.TrimSpace(string(out)) == "task-ui" {
			osexec.Command("tmux", "kill-session", "-t", "task-ui").Run()
		}
	}

	return nil
}

// ensureDaemonRunning starts the daemon if it's not already running.
func ensureDaemonRunning() error {
	pidFile := getPidFilePath()

	// Check if daemon is already running
	if pid, err := readPidFile(pidFile); err == nil {
		if processExists(pid) {
			return nil // Already running
		}
		// Stale pid file, remove it
		os.Remove(pidFile)
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

	fmt.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf("Started daemon (pid %d)", cmd.Process.Pid)))
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
	pid, err := readPidFile(pidFile)
	if err != nil {
		return fmt.Errorf("daemon not running (no pid file)")
	}

	if !processExists(pid) {
		os.Remove(pidFile)
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
	SessionID        string `json:"session_id"`
	TranscriptPath   string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type,omitempty"` // For Notification hooks
	Message          string `json:"message,omitempty"`
	StopReason       string `json:"stop_reason,omitempty"` // For Stop hooks
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
	default:
		// Unknown hook type, ignore
		return nil
	}
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
		// Only update if currently processing (avoid overwriting other states)
		if task != nil && task.Status == db.StatusProcessing {
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

	// After a tool completes, Claude is still working (will process tool results)
	// Ensure task remains in "processing" state
	if task.Status == db.StatusBlocked {
		database.UpdateTaskStatus(taskID, db.StatusProcessing)
		database.AppendTaskLog(taskID, "system", "Claude processing tool results")
	}

	return nil
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
	// List all windows in task-daemon session
	cmd := osexec.Command("tmux", "list-windows", "-t", executor.TmuxDaemonSession, "-F", "#{window_name}:#{window_activity}")
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
	windowTarget := executor.TmuxSessionName(int64(taskID))

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
