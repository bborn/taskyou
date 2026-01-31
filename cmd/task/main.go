// task is the local CLI for managing tasks.
// It runs locally by default, or connects to a remote taskd server over SSH with -r flag.
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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bborn/workflow/internal/autocomplete"
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
	version = "dev"
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
	if sid := os.Getenv("WORKTREE_SESSION_ID"); sid != "" {
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
		remote    bool
		dangerous bool
	)

	rootCmd := &cobra.Command{
		Use:     "ty",
		Short:   "Task queue manager",
		Long:    "A beautiful terminal UI for managing your task queue.",
		Version: version,
		Run: func(cmd *cobra.Command, args []string) {
			// TUI requires tmux for split-pane Claude interaction
			if os.Getenv("TMUX") == "" {
				if err := execInTmux(); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
				return
			}

			if remote {
				// Connect to remote server
				if err := connectRemote(host, port); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Connection failed: "+err.Error()))
					os.Exit(1)
				}
				return
			}
			// Run locally (default)
			if err := runLocal(dangerous); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}

	rootCmd.SetVersionTemplate(`{{.Version}}
`)

	rootCmd.PersistentFlags().StringVarP(&host, "host", "H", defaultHost, "Remote server host")
	rootCmd.PersistentFlags().StringVar(&port, "port", defaultPort, "Remote server port")
	rootCmd.PersistentFlags().BoolVarP(&remote, "remote", "r", false, "Connect to remote server instead of running locally")
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
			dangerousMode := os.Getenv("WORKTREE_DANGEROUS_MODE") == "1"
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

			// Pass through -r flag if we're in remote mode
			execArgs := []string{executable}
			if remote {
				execArgs = append(execArgs, "-r")
			}

			syscall.Exec(executable, execArgs, os.Environ())
		},
	}
	restartCmd.Flags().BoolVarP(&remote, "remote", "r", false, "Connect to remote server")
	restartCmd.Flags().BoolVar(&hardRestart, "hard", false, "Kill all tmux sessions (full reset)")
	rootCmd.AddCommand(restartCmd)

	// Recover subcommand - fix stale references after crash
	recoverCmd := &cobra.Command{
		Use:   "recover",
		Short: "Fix stale tmux references after a crash",
		Long: `Clears stale daemon_session and tmux_window_id references from tasks.

Use this after your computer crashes or the daemon dies unexpectedly.
Tasks will automatically reconnect to their Claude sessions when viewed.`,
		Run: func(cmd *cobra.Command, args []string) {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			recoverStaleTmuxRefs(dryRun)
		},
	}
	recoverCmd.Flags().Bool("dry-run", false, "Show what would be cleaned up without making changes")
	rootCmd.AddCommand(recoverCmd)

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

	claudesCleanupCmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Kill orphaned Claude processes not tied to active task windows",
		Run: func(cmd *cobra.Command, args []string) {
			force, _ := cmd.Flags().GetBool("force")
			cleanupOrphanedClaudes(force)
		},
	}
	claudesCleanupCmd.Flags().BoolP("force", "f", false, "Use SIGKILL instead of SIGTERM to force kill processes")
	claudesCmd.AddCommand(claudesCleanupCmd)

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
		Use:   "create [title]",
		Short: "Create a new task",
		Long: `Create a new task from the command line.

Title is optional if --body is provided; AI will generate a title from the body.

Examples:
  task create "Fix login bug"
  task create "Add dark mode" --type code --project myapp
  task create "Write documentation" --body "Document the API endpoints" --execute
  task create "Refactor auth" --executor codex  # Use Codex instead of Claude
  task create --body "The login button is broken on mobile devices" # AI generates title`,
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var title string
			if len(args) > 0 {
				title = args[0]
			}
			body, _ := cmd.Flags().GetString("body")
			taskType, _ := cmd.Flags().GetString("type")
			project, _ := cmd.Flags().GetString("project")
			taskExecutor, _ := cmd.Flags().GetString("executor")
			execute, _ := cmd.Flags().GetBool("execute")
			outputJSON, _ := cmd.Flags().GetBool("json")

			// Validate that either title or body is provided
			if strings.TrimSpace(title) == "" && strings.TrimSpace(body) == "" {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: either title or --body must be provided"))
				os.Exit(1)
			}

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

			// Validate executor if provided
			validExecutors := []string{db.ExecutorClaude, db.ExecutorCodex, db.ExecutorGemini, db.ExecutorPi, db.ExecutorOpenCode, db.ExecutorOpenClaw}
			if taskExecutor != "" {
				validExecutor := false
				for _, e := range validExecutors {
					if e == taskExecutor {
						validExecutor = true
						break
					}
				}
				if !validExecutor {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid executor. Must be one of: "+strings.Join(validExecutors, ", ")))
					os.Exit(1)
				}
			}

			// If project not specified, try to detect from cwd
			if project == "" {
				if cwd, err := os.Getwd(); err == nil {
					if p, err := database.GetProjectByPath(cwd); err == nil && p != nil {
						project = p.Name
					}
				}
			}

			// Generate title from body if title is empty
			if strings.TrimSpace(title) == "" && strings.TrimSpace(body) != "" {
				var apiKey string
				apiKey, _ = database.GetSetting("anthropic_api_key")
				svc := autocomplete.NewService(apiKey)
				if svc.IsAvailable() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					if generatedTitle, genErr := svc.GenerateTitle(ctx, body, project); genErr == nil && generatedTitle != "" {
						title = generatedTitle
						if !outputJSON {
							fmt.Println(dimStyle.Render("Generated title: " + title))
						}
					}
					cancel()
				}
				// Fallback if generation failed
				if strings.TrimSpace(title) == "" {
					firstLine := strings.Split(strings.TrimSpace(body), "\n")[0]
					if len(firstLine) > 50 {
						firstLine = firstLine[:50] + "..."
					}
					title = firstLine
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
				Executor: taskExecutor,
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
					"executor": task.Executor,
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
	createCmd.Flags().String("body", "", "Task body/description (if no title, AI generates from body)")
	createCmd.Flags().StringP("type", "t", "", "Task type: code, writing, thinking (default: code)")
	createCmd.Flags().StringP("project", "p", "", "Project name (auto-detected from cwd if not specified)")
	createCmd.Flags().StringP("executor", "e", "", "Task executor: claude, codex, gemini, pi, opencode, openclaw (default: claude)")
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
							"number":      prInfo.Number,
							"url":         prInfo.URL,
							"state":       string(prInfo.State),
							"check_state": string(prInfo.CheckState),
							"description": prInfo.StatusDescription(),
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
					// Schedule indicator
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

	boardCmd := &cobra.Command{
		Use:   "board",
		Short: "Show the Kanban board in the CLI",
		Long: `Print the same Backlog / Queued / In Progress / Blocked / Done view
that the TUI shows, either as formatted text or JSON for automation.`,
		Run: func(cmd *cobra.Command, args []string) {
			outputJSON, _ := cmd.Flags().GetBool("json")
			limit, _ := cmd.Flags().GetInt("limit")

			if limit <= 0 {
				limit = 5
			}

			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			tasks, err := database.ListTasks(db.ListTasksOptions{IncludeClosed: true, Limit: 500})
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			snapshot := buildBoardSnapshot(tasks, limit)

			if outputJSON {
				data, _ := json.MarshalIndent(snapshot, "", "  ")
				fmt.Println(string(data))
				return
			}

			fmt.Println(boldStyle.Render("Kanban Snapshot"))
			fmt.Println(strings.Repeat("─", 50))
			for _, column := range snapshot.Columns {
				fmt.Printf("%s (%d)\n", column.Label, column.Count)
				if column.Count == 0 {
					fmt.Println("  (empty)")
					fmt.Println()
					continue
				}
				for _, task := range column.Tasks {
					line := fmt.Sprintf("- #%d %s", task.ID, task.Title)
					if task.Project != "" {
						line += fmt.Sprintf(" [%s]", task.Project)
					}
					if task.Type != "" {
						line += fmt.Sprintf(" (%s)", task.Type)
					}
					if task.Pinned {
						line += " 📌"
					}
					if task.AgeHint != "" {
						line += fmt.Sprintf(" • %s", task.AgeHint)
					}
					fmt.Println("  " + line)
				}
				if column.Count > len(column.Tasks) {
					fmt.Printf("  … +%d more\n", column.Count-len(column.Tasks))
				}
				fmt.Println()
			}
		},
	}
	boardCmd.Flags().Bool("json", false, "Output board snapshot as JSON")
	boardCmd.Flags().Int("limit", 5, "Maximum entries to show per column")
	rootCmd.AddCommand(boardCmd)

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
						"number":      prInfo.Number,
						"url":         prInfo.URL,
						"state":       string(prInfo.State),
						"check_state": string(prInfo.CheckState),
						"description": prInfo.StatusDescription(),
						"mergeable":   prInfo.Mergeable,
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

	// Move subcommand - move a task to a different project
	moveCmd := &cobra.Command{
		Use:   "move <task-id> <target-project>",
		Short: "Move a task to a different project",
		Long: `Move a task to a different project.

This properly cleans up the task's worktree and Claude sessions from the old project,
deletes the old task, and creates a new task in the target project.

The new task preserves:
- Title, body, type, and tags
- Status (unless processing/blocked, which resets to backlog)

The new task resets:
- Worktree path, branch name, port
- Claude session ID, daemon session
- Started/completed timestamps

Examples:
  task move 42 myapp
  task move 42 myapp --execute`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}
			targetProject := args[1]

			// Open database
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			// Validate target project exists
			proj, err := database.GetProjectByName(targetProject)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if proj == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Project '%s' not found", targetProject)))
				os.Exit(1)
			}

			// Get task
			task, err := database.GetTask(taskID)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if task == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found", taskID)))
				os.Exit(1)
			}

			// Check if already in target project
			if task.Project == targetProject || task.Project == proj.Name {
				fmt.Println(dimStyle.Render(fmt.Sprintf("Task #%d is already in project '%s'", taskID, targetProject)))
				return
			}

			oldProject := task.Project
			execute, _ := cmd.Flags().GetBool("execute")

			// Confirm unless --force flag is set
			force, _ := cmd.Flags().GetBool("force")
			if !force {
				fmt.Printf("Move task #%d from '%s' to '%s'? [y/N] ", taskID, oldProject, targetProject)
				reader := bufio.NewReader(os.Stdin)
				response, _ := reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response != "y" && response != "yes" {
					fmt.Println("Cancelled")
					return
				}
			}

			// Perform the move
			newTaskID, err := moveTask(database, task, targetProject)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render(fmt.Sprintf("Moved task #%d to project '%s' (new task #%d)", taskID, targetProject, newTaskID)))

			// Queue for execution if requested
			if execute {
				if err := database.UpdateTaskStatus(newTaskID, db.StatusQueued); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error queueing task: "+err.Error()))
					os.Exit(1)
				}
				fmt.Println(successStyle.Render(fmt.Sprintf("Queued task #%d for execution", newTaskID)))
			}
		},
	}
	moveCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	moveCmd.Flags().BoolP("execute", "e", false, "Queue the task for execution after moving")
	rootCmd.AddCommand(moveCmd)

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

	statusCmd := &cobra.Command{
		Use:   "status <task-id> <status>",
		Short: "Set a task's status",
		Long: `Manually update a task's status. Useful for automation/orchestration when
you need to move cards between columns without opening the TUI.

Valid statuses: backlog, queued, processing, blocked, done, archived.`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			status := strings.ToLower(strings.TrimSpace(args[1]))
			if !isValidStatus(status) {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid status. Must be one of: "+strings.Join(validStatuses(), ", ")))
				os.Exit(1)
			}

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

			if err := database.UpdateTaskStatus(taskID, status); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render(fmt.Sprintf("Task #%d moved to %s", taskID, status)))
		},
	}
	rootCmd.AddCommand(statusCmd)

	pinCmd := &cobra.Command{
		Use:   "pin <task-id>",
		Short: "Pin, unpin, or toggle a task",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			unpin, _ := cmd.Flags().GetBool("unpin")
			toggle, _ := cmd.Flags().GetBool("toggle")

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

			var newValue bool
			if toggle {
				newValue = !task.Pinned
			} else if unpin {
				newValue = false
			} else {
				newValue = true
			}

			if err := database.UpdateTaskPinned(taskID, newValue); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			state := "pinned"
			if !newValue {
				state = "unpinned"
			}
			fmt.Println(successStyle.Render(fmt.Sprintf("Task #%d %s", taskID, state)))
		},
	}
	pinCmd.Flags().Bool("unpin", false, "Unpin the task")
	pinCmd.Flags().Bool("toggle", false, "Toggle the current pin state")
	rootCmd.AddCommand(pinCmd)

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

	// Purge Claude config subcommand - remove stale entries from ~/.claude.json
	purgeClaudeConfigCmd := &cobra.Command{
		Use:   "purge-claude-config",
		Short: "Remove stale project entries from ~/.claude.json",
		Long: `Remove entries from ~/.claude.json for project paths that no longer exist.

This cleans up stale entries left behind by deleted worktrees, conductor workspaces,
and other tools that add per-project configuration to the global Claude config.

Examples:
  task purge-claude-config
  task purge-claude-config --dry-run`,
		Run: func(cmd *cobra.Command, args []string) {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			configDirFlag, _ := cmd.Flags().GetString("config-dir")
			resolvedDir := executor.ResolveClaudeConfigDir(configDirFlag)
			configPath := executor.ClaudeConfigFilePath(resolvedDir)

			if dryRun {
				// Show what would be removed
				removed, paths, err := previewStaleClaudeProjectConfigs(configPath)
				if err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
				if removed == 0 {
					fmt.Println(dimStyle.Render("No stale entries found"))
					return
				}
				fmt.Printf("Would remove %d stale entries:\n", removed)
				for _, p := range paths {
					fmt.Printf("  %s\n", dimStyle.Render(p))
				}
				return
			}

			removed, err := executor.PurgeStaleClaudeProjectConfigs(configPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			if removed == 0 {
				fmt.Println(dimStyle.Render("No stale entries found"))
			} else {
				fmt.Println(successStyle.Render(fmt.Sprintf("Removed %d stale entries from %s", removed, configPath)))
			}
		},
	}
	purgeClaudeConfigCmd.Flags().Bool("dry-run", false, "Show what would be removed without making changes")
	purgeClaudeConfigCmd.Flags().String("config-dir", "", "Claude config directory (defaults to $CLAUDE_CONFIG_DIR or ~/.claude)")
	rootCmd.AddCommand(purgeClaudeConfigCmd)

	// Cloud subcommand
	rootCmd.AddCommand(createCloudCommand())

	// Update command - self-update via install script
	upgradeCmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade task to the latest version",
		Long:  "Downloads and installs the latest version of the task CLI from GitHub releases.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(dimStyle.Render("Checking for updates..."))

			// Run the install script via curl
			installCmd := osexec.Command("bash", "-c", "curl -fsSL https://raw.githubusercontent.com/bborn/taskyou/main/scripts/install.sh | bash")
			installCmd.Stdout = os.Stdout
			installCmd.Stderr = os.Stderr
			installCmd.Stdin = os.Stdin

			if err := installCmd.Run(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Update failed: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	rootCmd.AddCommand(upgradeCmd)

	// Settings command
	settingsCmd := &cobra.Command{
		Use:   "settings",
		Short: "View and manage app settings",
		Run: func(cmd *cobra.Command, args []string) {
			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			// Show all settings
			fmt.Println(boldStyle.Render("Settings"))
			fmt.Println()

			// Anthropic API Key
			apiKey, _ := database.GetSetting("anthropic_api_key")
			if apiKey != "" {
				// Mask the key for display
				masked := apiKey[:7] + "..." + apiKey[len(apiKey)-4:]
				fmt.Printf("anthropic_api_key: %s\n", masked)
			} else if os.Getenv("ANTHROPIC_API_KEY") != "" {
				fmt.Printf("anthropic_api_key: %s\n", dimStyle.Render("(using ANTHROPIC_API_KEY env var)"))
			} else {
				fmt.Printf("anthropic_api_key: %s\n", dimStyle.Render("(not set)"))
			}

			// Autocomplete enabled
			autocomplete, _ := database.GetSetting("autocomplete_enabled")
			if autocomplete == "" {
				autocomplete = "true"
			}
			fmt.Printf("autocomplete_enabled: %s\n", autocomplete)

			// Idle suspend timeout
			idleTimeout, _ := database.GetSetting("idle_suspend_timeout")
			if idleTimeout == "" {
				idleTimeout = "6h (default)"
			}
			fmt.Printf("idle_suspend_timeout: %s\n", idleTimeout)

			fmt.Println()
			fmt.Println(dimStyle.Render("Use 'task settings set <key> <value>' to change settings"))
		},
	}

	settingsSetCmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a setting value",
		Long: `Set a configuration setting.

Available settings:
  anthropic_api_key     API key for ghost text autocomplete (uses Anthropic API
                        directly for speed). Get yours at console.anthropic.com
  autocomplete_enabled  Enable/disable ghost text autocomplete (true/false)
  idle_suspend_timeout  How long blocked tasks wait before suspending (e.g. 6h, 30m, 24h)`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			key := args[0]
			value := args[1]

			// Validate known settings
			switch key {
			case "anthropic_api_key":
				if !strings.HasPrefix(value, "sk-ant-") {
					fmt.Println(errorStyle.Render("Invalid API key format. Should start with 'sk-ant-'"))
					return
				}
			case "autocomplete_enabled":
				if value != "true" && value != "false" {
					fmt.Println(errorStyle.Render("Value must be 'true' or 'false'"))
					return
				}
			case "idle_suspend_timeout":
				if _, err := time.ParseDuration(value); err != nil {
					fmt.Println(errorStyle.Render("Invalid duration format. Examples: 6h, 30m, 24h, 1h30m"))
					return
				}
			default:
				fmt.Println(errorStyle.Render("Unknown setting: " + key))
				fmt.Println(dimStyle.Render("Available: anthropic_api_key, autocomplete_enabled, idle_suspend_timeout"))
				return
			}

			dbPath := db.DefaultPath()
			database, err := db.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			if err := database.SetSetting(key, value); err != nil {
				fmt.Println(errorStyle.Render("Failed to save setting: " + err.Error()))
				return
			}
			fmt.Println(successStyle.Render("Setting saved: " + key))
		},
	}

	settingsCmd.AddCommand(settingsSetCmd)
	rootCmd.AddCommand(settingsCmd)

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

	// Set WORKTREE_SESSION_ID env var so child processes use the same session ID
	envCmd := fmt.Sprintf("WORKTREE_SESSION_ID=%s %s", sessionID, cmdStr)

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
	// Use conditional formatting: pane 0 (task detail) always uses bright color, others follow border style
	// This prevents the task detail title from being dimmed when other panes are focused
	osexec.Command("tmux", "set-option", "-t", sessionName, "pane-border-format",
		"#{?#{==:#{pane_index},0},#[fg=#9CA3AF] #{pane_title} , #{pane_title} }").Run()
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
// If dangerousMode is true, sets WORKTREE_DANGEROUS_MODE=1 for the daemon.
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
	cmd.Env = append(os.Environ(), fmt.Sprintf("WORKTREE_SESSION_ID=%s", getSessionID()))
	// Pass dangerous mode flag if enabled
	if dangerousMode {
		cmd.Env = append(cmd.Env, "WORKTREE_DANGEROUS_MODE=1")
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

type boardSnapshot struct {
	Columns []boardColumn `json:"columns"`
}

type boardColumn struct {
	Status string       `json:"status"`
	Label  string       `json:"label"`
	Count  int          `json:"count"`
	Tasks  []boardEntry `json:"tasks"`
}

type boardEntry struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Project string `json:"project"`
	Type    string `json:"type"`
	Pinned  bool   `json:"pinned"`
	AgeHint string `json:"age_hint"`
}

func buildBoardSnapshot(tasks []*db.Task, limit int) boardSnapshot {
	sections := []struct {
		status string
		label  string
	}{
		{db.StatusBacklog, "Backlog"},
		{db.StatusQueued, "Queued"},
		{db.StatusProcessing, "In Progress"},
		{db.StatusBlocked, "Blocked"},
		{db.StatusDone, "Done"},
	}

	grouped := make(map[string][]*db.Task)
	for _, task := range tasks {
		if task.Status == db.StatusArchived {
			continue
		}
		grouped[task.Status] = append(grouped[task.Status], task)
	}

	var snapshot boardSnapshot
	for _, section := range sections {
		columnTasks := grouped[section.status]
		if len(columnTasks) == 0 {
			snapshot.Columns = append(snapshot.Columns, boardColumn{Status: section.status, Label: section.label, Count: 0})
			continue
		}

		sortTasksForBoard(columnTasks)
		column := boardColumn{Status: section.status, Label: section.label, Count: len(columnTasks)}
		for i, task := range columnTasks {
			if i >= limit {
				break
			}
			entry := boardEntry{
				ID:      task.ID,
				Title:   truncate(task.Title, 80),
				Project: task.Project,
				Type:    task.Type,
				Pinned:  task.Pinned,
				AgeHint: boardAgeHint(task),
			}
			column.Tasks = append(column.Tasks, entry)
		}
		snapshot.Columns = append(snapshot.Columns, column)
	}

	return snapshot
}

func sortTasksForBoard(tasks []*db.Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Pinned != tasks[j].Pinned {
			return tasks[i].Pinned
		}
		return boardReferenceTime(tasks[i]).After(boardReferenceTime(tasks[j]))
	})
}

func boardReferenceTime(task *db.Task) time.Time {
	switch task.Status {
	case db.StatusProcessing:
		if task.StartedAt != nil {
			return task.StartedAt.Time
		}
	case db.StatusDone:
		if task.CompletedAt != nil {
			return task.CompletedAt.Time
		}
	case db.StatusBlocked:
		return task.UpdatedAt.Time
	case db.StatusQueued:
		return task.UpdatedAt.Time
	case db.StatusBacklog:
		return task.CreatedAt.Time
	}
	return task.UpdatedAt.Time
}

func boardAgeHint(task *db.Task) string {
	ref := boardReferenceTime(task)
	if ref.IsZero() {
		return ""
	}

	delta := time.Since(ref)
	if delta < 0 {
		delta = -delta
	}

	switch task.Status {
	case db.StatusProcessing:
		return fmt.Sprintf("running %s ago", formatShortDuration(delta))
	case db.StatusBlocked:
		return fmt.Sprintf("blocked %s", formatShortDuration(delta))
	case db.StatusQueued:
		return fmt.Sprintf("queued %s", formatShortDuration(delta))
	case db.StatusBacklog:
		return fmt.Sprintf("created %s", formatShortDuration(delta))
	case db.StatusDone:
		return fmt.Sprintf("done %s", formatShortDuration(delta))
	default:
		return formatShortDuration(delta)
	}
}

func formatShortDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	units := []struct {
		dur   time.Duration
		label string
	}{
		{24 * time.Hour, "d"},
		{time.Hour, "h"},
		{time.Minute, "m"},
		{time.Second, "s"},
	}
	var parts []string
	remainder := d
	for _, u := range units {
		if remainder >= u.dur {
			value := remainder / u.dur
			parts = append(parts, fmt.Sprintf("%d%s", value, u.label))
			remainder %= u.dur
		}
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, " ")
}

var allowedStatuses = []string{
	db.StatusBacklog,
	db.StatusQueued,
	db.StatusProcessing,
	db.StatusBlocked,
	db.StatusDone,
	db.StatusArchived,
}

func validStatuses() []string {
	return allowedStatuses
}

func isValidStatus(status string) bool {
	for _, s := range allowedStatuses {
		if status == s {
			return true
		}
	}
	return false
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
		session.Setenv("WORKTREE_CWD", cwd)
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
	StopReason string `json:"stop_reason,omitempty"` // For Stop hooks
	// Tool use fields (for PreToolUse and PostToolUse hooks)
	ToolName     string          `json:"tool_name,omitempty"`     // Name of the tool being used
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`    // Tool-specific input parameters
	ToolResponse json.RawMessage `json:"tool_response,omitempty"` // Tool result (PostToolUse only)
	ToolUseID    string          `json:"tool_use_id,omitempty"`   // Unique identifier for this tool call
}

// handleClaudeHook processes Claude Code hook callbacks.
// It reads hook data from stdin and updates task status accordingly.
func handleClaudeHook(hookEvent string) error {
	// Get task ID from environment (set by executor when launching Claude)
	// If not set, this Claude session predates the task tracking system - silently succeed
	taskIDStr := os.Getenv("WORKTREE_TASK_ID")
	if taskIDStr == "" {
		return nil
	}
	var taskID int64
	if _, err := fmt.Sscanf(taskIDStr, "%d", &taskID); err != nil {
		return fmt.Errorf("invalid WORKTREE_TASK_ID: %s", taskIDStr)
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
	default:
		// Unknown hook type, ignore
		return nil
	}
}

// logSessionIDOnce logs the Claude session ID for a task, but only once.
// It checks if a session ID log already exists to avoid duplicate entries.
// Also persists the session ID to the task record for reliable resumption.
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

	// Persist session ID to task record for reliable resumption
	database.UpdateTaskClaudeSessionID(taskID, input.SessionID)
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
//   - "end_turn": Claude finished and is waiting for user input → task should be "blocked"
//   - "tool_use": Claude finished with a tool call that's about to execute → task stays "processing"
//     (PreToolUse/PostToolUse hooks handle the actual tool execution state tracking)
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
// This confirms Claude is still actively working after a tool completes and logs tool usage.
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

	// Log the tool use to the task log
	if input.ToolName != "" {
		logMsg := formatToolLogMessage(input)
		database.AppendTaskLog(taskID, "tool", logMsg)
	}

	// After a tool completes, Claude is still working (will process tool results)
	// Ensure task remains in "processing" state
	if task.Status == db.StatusBlocked {
		database.UpdateTaskStatus(taskID, db.StatusProcessing)
	}

	return nil
}

// formatToolLogMessage creates a human-readable log message for tool usage.
// It extracts relevant details from tool_input based on the tool type.
func formatToolLogMessage(input *ClaudeHookInput) string {
	toolName := input.ToolName

	// Try to extract meaningful details from tool_input
	if len(input.ToolInput) > 0 {
		var toolInput map[string]interface{}
		if err := json.Unmarshal(input.ToolInput, &toolInput); err == nil {
			// Extract relevant fields based on tool type
			switch toolName {
			case "Bash":
				if cmd, ok := toolInput["command"].(string); ok {
					// Truncate long commands
					if len(cmd) > 100 {
						cmd = cmd[:100] + "..."
					}
					return fmt.Sprintf("Bash: %s", cmd)
				}
			case "Read":
				if path, ok := toolInput["file_path"].(string); ok {
					return fmt.Sprintf("Read: %s", path)
				}
			case "Write":
				if path, ok := toolInput["file_path"].(string); ok {
					return fmt.Sprintf("Write: %s", path)
				}
			case "Edit":
				if path, ok := toolInput["file_path"].(string); ok {
					return fmt.Sprintf("Edit: %s", path)
				}
			case "Glob":
				if pattern, ok := toolInput["pattern"].(string); ok {
					return fmt.Sprintf("Glob: %s", pattern)
				}
			case "Grep":
				if pattern, ok := toolInput["pattern"].(string); ok {
					return fmt.Sprintf("Grep: %s", pattern)
				}
			case "Task":
				if desc, ok := toolInput["description"].(string); ok {
					return fmt.Sprintf("Task: %s", desc)
				}
			case "WebFetch":
				if url, ok := toolInput["url"].(string); ok {
					return fmt.Sprintf("WebFetch: %s", url)
				}
			case "WebSearch":
				if query, ok := toolInput["query"].(string); ok {
					return fmt.Sprintf("WebSearch: %s", query)
				}
			}
		}
	}

	// Default: just the tool name
	return toolName
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

	// Calculate total memory
	totalMemoryMB := 0
	for _, s := range sessions {
		totalMemoryMB += s.memoryMB
	}

	fmt.Printf("%s\n\n", boldStyle.Render(fmt.Sprintf("Running Claude Sessions (%d total, %dMB memory):", len(sessions), totalMemoryMB)))
	for _, s := range sessions {
		memStr := ""
		if s.memoryMB > 0 {
			memStr = fmt.Sprintf("%dMB", s.memoryMB)
		}
		titleStr := ""
		if s.taskTitle != "" {
			// Truncate title to 40 chars
			title := s.taskTitle
			if len(title) > 40 {
				title = title[:37] + "..."
			}
			titleStr = title
		}
		fmt.Printf("  %s  %s  %s  %s\n",
			successStyle.Render(fmt.Sprintf("task-%d", s.taskID)),
			dimStyle.Render(fmt.Sprintf("%-6s", memStr)),
			dimStyle.Render(fmt.Sprintf("%-40s", titleStr)),
			dimStyle.Render(s.info))
	}
}

type claudeSession struct {
	taskID    int
	taskTitle string
	memoryMB  int // Memory usage in MB
	info      string
}

// getClaudeSessions returns all running task-* windows across all task-daemon-* sessions.
func getClaudeSessions() []claudeSession {
	// First, get all task-daemon-* sessions
	sessionsCmd := osexec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	sessionsOut, err := sessionsCmd.Output()
	if err != nil {
		return nil
	}

	var daemonSessions []string
	for _, session := range strings.Split(strings.TrimSpace(string(sessionsOut)), "\n") {
		if strings.HasPrefix(session, "task-daemon-") {
			daemonSessions = append(daemonSessions, session)
		}
	}

	if len(daemonSessions) == 0 {
		return nil
	}

	// Open database to fetch task titles
	dbPath := db.DefaultPath()
	database, _ := db.Open(dbPath)
	if database != nil {
		defer database.Close()
	}

	// Build map of task ID -> memory usage
	taskMemory := getClaudeMemoryByTaskID()

	var sessions []claudeSession
	seen := make(map[int]bool) // Avoid duplicates if same task appears in multiple sessions

	for _, daemonSession := range daemonSessions {
		cmd := osexec.Command("tmux", "list-windows", "-t", daemonSession, "-F", "#{window_name}:#{window_activity}")
		output, err := cmd.Output()
		if err != nil {
			continue
		}

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

			// Skip if already seen (prefer first occurrence)
			if seen[taskID] {
				continue
			}
			seen[taskID] = true

			info := daemonSession
			if len(parts) >= 2 {
				// Parse activity timestamp
				var activity int64
				fmt.Sscanf(parts[1], "%d", &activity)
				if activity > 0 {
					t := time.Unix(activity, 0)
					info = fmt.Sprintf("%s, last activity %s", daemonSession, t.Format("15:04:05"))
				}
			}

			// Get task title from database
			var taskTitle string
			if database != nil {
				if task, err := database.GetTask(int64(taskID)); err == nil && task != nil {
					taskTitle = task.Title
				}
			}

			// Get memory usage for this task's Claude process
			memoryMB := taskMemory[taskID]

			sessions = append(sessions, claudeSession{
				taskID:    taskID,
				taskTitle: taskTitle,
				memoryMB:  memoryMB,
				info:      info,
			})
		}
	}

	return sessions
}

// getClaudeMemoryByTaskID returns a map of task ID -> memory (MB) for all Claude processes.
// It identifies task IDs by examining each Claude process's working directory.
func getClaudeMemoryByTaskID() map[int]int {
	result := make(map[int]int)

	// Find all Claude processes
	pgrepOut, err := osexec.Command("pgrep", "-f", "claude").Output()
	if err != nil {
		return result
	}

	for _, pidStr := range strings.Split(string(pgrepOut), "\n") {
		pidStr = strings.TrimSpace(pidStr)
		if pidStr == "" {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Get the process's current working directory using lsof
		lsofOut, err := osexec.Command("lsof", "-p", pidStr, "-Fn").Output()
		if err != nil {
			continue
		}

		// Parse lsof output to find cwd
		var cwd string
		lines := strings.Split(string(lsofOut), "\n")
		for i, line := range lines {
			if line == "fcwd" && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "n") {
				cwd = lines[i+1][1:] // Remove 'n' prefix
				break
			}
		}

		if cwd == "" {
			continue
		}

		// Extract task ID from worktree path like ".task-worktrees/467-..."
		if idx := strings.Index(cwd, ".task-worktrees/"); idx >= 0 {
			pathPart := cwd[idx+len(".task-worktrees/"):]
			// Parse the task ID from the beginning of the path
			var taskID int
			if _, err := fmt.Sscanf(pathPart, "%d-", &taskID); err == nil && taskID > 0 {
				memMB := getProcessMemoryMB(pid)
				// If there are multiple Claude processes for the same task, sum them
				result[taskID] += memMB
			}
		}
	}

	return result
}

// getProcessMemoryMB returns the memory usage of a process in MB.
func getProcessMemoryMB(pid int) int {
	// Use ps to get RSS (resident set size) in KB
	out, err := osexec.Command("ps", "-p", strconv.Itoa(pid), "-o", "rss=").Output()
	if err != nil {
		return 0
	}
	rssKB, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return rssKB / 1024 // Convert to MB
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

// recoverStaleTmuxRefs clears stale daemon_session and tmux_window_id references
// from tasks after a crash or daemon restart. This allows tasks to automatically
// reconnect to their Claude sessions when viewed.
func recoverStaleTmuxRefs(dryRun bool) {
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error opening database: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	// Step 1: Find all active daemon sessions
	activeSessions := make(map[string]bool)
	sessionsOut, err := osexec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err == nil {
		for _, session := range strings.Split(strings.TrimSpace(string(sessionsOut)), "\n") {
			if strings.HasPrefix(session, "task-daemon-") {
				activeSessions[session] = true
			}
		}
	}

	if len(activeSessions) == 0 {
		fmt.Println(dimStyle.Render("No active daemon sessions found"))
		return
	}

	fmt.Println(dimStyle.Render(fmt.Sprintf("Active daemon sessions: %d", len(activeSessions))))
	for session := range activeSessions {
		fmt.Println(dimStyle.Render("  " + session))
	}

	// Step 2: Find all valid window IDs across all daemon sessions
	validWindowIDs := make(map[string]bool)
	for session := range activeSessions {
		windowsOut, err := osexec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_id}").Output()
		if err == nil {
			for _, windowID := range strings.Split(strings.TrimSpace(string(windowsOut)), "\n") {
				if windowID != "" {
					validWindowIDs[windowID] = true
				}
			}
		}
	}

	fmt.Println(dimStyle.Render(fmt.Sprintf("Valid window IDs: %d", len(validWindowIDs))))

	// Step 3: Count tasks with stale daemon_session references
	var staleDaemonCount int
	rows, err := database.Query(`
		SELECT COUNT(*) FROM tasks
		WHERE daemon_session IS NOT NULL
		AND daemon_session NOT IN (` + quotedSessionList(activeSessions) + `)
	`)
	if err == nil {
		if rows.Next() {
			rows.Scan(&staleDaemonCount)
		}
		rows.Close()
	}

	// Step 4: Count tasks with stale window IDs
	var staleWindowCount int
	rows, err = database.Query(`
		SELECT COUNT(*) FROM tasks
		WHERE tmux_window_id IS NOT NULL
		AND tmux_window_id NOT IN (` + quotedWindowList(validWindowIDs) + `)
	`)
	if err == nil {
		if rows.Next() {
			rows.Scan(&staleWindowCount)
		}
		rows.Close()
	}

	if staleDaemonCount == 0 && staleWindowCount == 0 {
		fmt.Println(successStyle.Render("No stale references found - database is clean"))
		return
	}

	fmt.Println()
	if staleDaemonCount > 0 {
		fmt.Printf("Found %d tasks with stale daemon_session references\n", staleDaemonCount)
	}
	if staleWindowCount > 0 {
		fmt.Printf("Found %d tasks with stale tmux_window_id references\n", staleWindowCount)
	}

	if dryRun {
		fmt.Println(dimStyle.Render("\nDry run - no changes made. Run without --dry-run to fix."))
		return
	}

	// Step 5: Clear stale references
	fmt.Println()
	if staleDaemonCount > 0 {
		_, err := database.Exec(`
			UPDATE tasks SET daemon_session = NULL
			WHERE daemon_session IS NOT NULL
			AND daemon_session NOT IN (` + quotedSessionList(activeSessions) + `)
		`)
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("Error clearing daemon sessions: "+err.Error()))
		} else {
			fmt.Println(successStyle.Render(fmt.Sprintf("Cleared %d stale daemon_session references", staleDaemonCount)))
		}
	}

	if staleWindowCount > 0 {
		_, err := database.Exec(`
			UPDATE tasks SET tmux_window_id = NULL
			WHERE tmux_window_id IS NOT NULL
			AND tmux_window_id NOT IN (` + quotedWindowList(validWindowIDs) + `)
		`)
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("Error clearing window IDs: "+err.Error()))
		} else {
			fmt.Println(successStyle.Render(fmt.Sprintf("Cleared %d stale tmux_window_id references", staleWindowCount)))
		}
	}

	fmt.Println()
	fmt.Println(dimStyle.Render("Tasks will automatically reconnect to their Claude sessions when viewed."))
}

// quotedSessionList returns a SQL-safe comma-separated list of quoted session names
func quotedSessionList(sessions map[string]bool) string {
	if len(sessions) == 0 {
		return "''"
	}
	var parts []string
	for s := range sessions {
		parts = append(parts, "'"+s+"'")
	}
	return strings.Join(parts, ",")
}

// quotedWindowList returns a SQL-safe comma-separated list of quoted window IDs
func quotedWindowList(windows map[string]bool) string {
	if len(windows) == 0 {
		return "''"
	}
	var parts []string
	for w := range windows {
		parts = append(parts, "'"+w+"'")
	}
	return strings.Join(parts, ",")
}

// cleanupOrphanedClaudes kills Claude processes that aren't tied to any active task windows
// or belong to done tasks older than 2 hours.
func cleanupOrphanedClaudes(force bool) {
	// Step 1: Get all task windows across ALL task-daemon-* sessions
	activeTaskIDs := make(map[int]bool)

	// List all tmux sessions
	sessionsOut, err := osexec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error listing tmux sessions: "+err.Error()))
		return
	}

	for _, session := range strings.Split(string(sessionsOut), "\n") {
		session = strings.TrimSpace(session)
		if !strings.HasPrefix(session, "task-daemon-") {
			continue
		}

		// List windows in this daemon session
		windowsOut, err := osexec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_name}").Output()
		if err != nil {
			continue
		}

		for _, window := range strings.Split(string(windowsOut), "\n") {
			window = strings.TrimSpace(window)
			if !strings.HasPrefix(window, "task-") {
				continue
			}
			var taskID int
			if _, err := fmt.Sscanf(window, "task-%d", &taskID); err == nil {
				activeTaskIDs[taskID] = true
			}
		}
	}

	// Step 1b: Get task IDs of done tasks older than 2 hours - these should be killed
	doneOldTaskIDs := make(map[int]bool)
	dbPath := db.DefaultPath()
	database, err := db.Open(dbPath)
	if err == nil {
		defer database.Close()
		twoHoursAgo := time.Now().Add(-2 * time.Hour)
		// Get done tasks completed more than 2 hours ago
		tasks, err := database.ListTasks(db.ListTasksOptions{
			Status: db.StatusDone,
		})
		if err == nil {
			for _, task := range tasks {
				if task.CompletedAt != nil && task.CompletedAt.Before(twoHoursAgo) {
					doneOldTaskIDs[int(task.ID)] = true
				}
			}
		}
	}

	// Step 2: Get all Claude processes running in tmux
	pgrepOut, err := osexec.Command("pgrep", "-f", "claude.*TERM_PROGRAM=tmux").Output()
	if err != nil {
		// No Claude processes found
		fmt.Println(successStyle.Render("No orphaned Claude processes found"))
		return
	}

	var orphanedPIDs []int
	var oldDonePIDs []int
	for _, pidStr := range strings.Split(string(pgrepOut), "\n") {
		pidStr = strings.TrimSpace(pidStr)
		if pidStr == "" {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Check if this PID's environment has WORKTREE_TASK_ID
		// and if that task ID is in our active set
		envOut, err := osexec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if err != nil {
			orphanedPIDs = append(orphanedPIDs, pid)
			continue
		}

		env := string(envOut)
		isOrphaned := true
		var extractedTaskID int

		// Try to extract WORKTREE_TASK_ID from the command line
		if idx := strings.Index(env, "WORKTREE_TASK_ID="); idx >= 0 {
			if _, err := fmt.Sscanf(env[idx:], "WORKTREE_TASK_ID=%d", &extractedTaskID); err == nil {
				if activeTaskIDs[extractedTaskID] {
					isOrphaned = false
				}
			}
		}

		// Check if this is from an old done task
		if !isOrphaned && extractedTaskID > 0 && doneOldTaskIDs[extractedTaskID] {
			oldDonePIDs = append(oldDonePIDs, pid)
			continue
		}

		if isOrphaned {
			orphanedPIDs = append(orphanedPIDs, pid)
		}
	}

	totalToKill := len(orphanedPIDs) + len(oldDonePIDs)
	if totalToKill == 0 {
		fmt.Println(successStyle.Render("No orphaned Claude processes found"))
		return
	}

	// Step 3: Kill orphaned processes
	if len(orphanedPIDs) > 0 {
		fmt.Printf("%s\n\n", boldStyle.Render(fmt.Sprintf("Found %d orphaned Claude processes:", len(orphanedPIDs))))
	}
	if len(oldDonePIDs) > 0 {
		fmt.Printf("%s\n\n", boldStyle.Render(fmt.Sprintf("Found %d Claude processes from done tasks (>2h old):", len(oldDonePIDs))))
	}

	signal := syscall.SIGTERM
	signalName := "SIGTERM"
	if force {
		signal = syscall.SIGKILL
		signalName = "SIGKILL"
	}

	killed := 0
	allPIDs := append(orphanedPIDs, oldDonePIDs...)
	for _, pid := range allPIDs {
		proc, err := os.FindProcess(pid)
		if err != nil {
			fmt.Printf("  %s PID %d: %s\n", errorStyle.Render("✗"), pid, err.Error())
			continue
		}

		if err := proc.Signal(signal); err != nil {
			fmt.Printf("  %s PID %d: %s\n", errorStyle.Render("✗"), pid, err.Error())
			continue
		}

		fmt.Printf("  %s PID %d (%s)\n", successStyle.Render("✓ Killed"), pid, signalName)
		killed++
	}

	fmt.Printf("\n%s\n", dimStyle.Render(fmt.Sprintf("Killed %d/%d processes", killed, totalToKill)))
}

// moveTask moves a task to a different project by cleaning up old resources,
// deleting the old task, and creating a new task in the target project.
// Returns the new task ID.
func moveTask(database *db.DB, oldTask *db.Task, targetProject string) (int64, error) {
	cfg := config.New(database)
	exec := executor.New(database, cfg)

	// Step 1: Clean up old task's resources

	// Kill Claude session if running (ignore errors - session may not exist)
	killClaudeSession(int(oldTask.ID))

	// Clean up worktree and Claude sessions if they exist
	if oldTask.WorktreePath != "" {
		projectConfigDir := ""
		if oldTask.Project != "" {
			if project, err := database.GetProjectByName(oldTask.Project); err == nil && project != nil {
				projectConfigDir = project.ClaudeConfigDir
			}
		}
		// Clean up Claude session files first (before worktree is removed)
		if err := executor.CleanupClaudeSessions(oldTask.WorktreePath, projectConfigDir); err != nil {
			fmt.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf("Warning: could not remove Claude sessions: %v", err)))
		}

		// Clean up worktree
		if err := exec.CleanupWorktree(oldTask); err != nil {
			fmt.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf("Warning: could not remove worktree: %v", err)))
		}
	}

	// Step 2: Delete the old task from database
	if err := database.DeleteTask(oldTask.ID); err != nil {
		return 0, fmt.Errorf("delete old task: %w", err)
	}

	// Step 3: Create new task in target project
	// Reset execution-related fields but preserve content
	newTask := &db.Task{
		Title:    oldTask.Title,
		Body:     oldTask.Body,
		Type:     oldTask.Type,
		Tags:     oldTask.Tags,
		Project:  targetProject,
		Executor: oldTask.Executor,
		Pinned:   oldTask.Pinned,
		// Reset execution state
		WorktreePath:    "",
		BranchName:      "",
		Port:            0,
		ClaudeSessionID: "",
		DaemonSession:   "",
		StartedAt:       nil,
		CompletedAt:     nil,
		// Keep status unless it was processing/blocked
		Status: oldTask.Status,
	}

	// Reset status if task was in progress (work is lost)
	if newTask.Status == db.StatusProcessing || newTask.Status == db.StatusBlocked {
		newTask.Status = db.StatusBacklog
	}

	if err := database.CreateTask(newTask); err != nil {
		return 0, fmt.Errorf("create new task: %w", err)
	}

	// CreateTask doesn't insert all fields (tags, pinned, etc.), so update them
	if err := database.UpdateTask(newTask); err != nil {
		return 0, fmt.Errorf("update new task: %w", err)
	}

	// Notify about the changes
	exec.NotifyTaskChange("deleted", oldTask)
	exec.NotifyTaskChange("created", newTask)

	return newTask.ID, nil
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

	// Clean up worktree and Claude sessions if they exist
	if task.WorktreePath != "" {
		projectConfigDir := ""
		if task.Project != "" {
			if project, err := database.GetProjectByName(task.Project); err == nil && project != nil {
				projectConfigDir = project.ClaudeConfigDir
			}
		}
		// Clean up Claude session files first (before worktree is removed)
		if err := executor.CleanupClaudeSessions(task.WorktreePath, projectConfigDir); err != nil {
			fmt.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf("Warning: could not remove Claude sessions: %v", err)))
		}

		// Clean up worktree
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

// previewStaleClaudeProjectConfigs returns the count and paths of stale entries
// that would be removed from claude.json without actually removing them.
func previewStaleClaudeProjectConfigs(configPath string) (int, []string, error) {
	if configPath == "" {
		configPath = executor.ClaudeConfigFilePath(executor.DefaultClaudeConfigDir())
	}

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil // No config file
		}
		return 0, nil, fmt.Errorf("read claude config: %w", err)
	}

	// Parse as generic JSON
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return 0, nil, fmt.Errorf("parse claude config: %w", err)
	}

	// Get projects map
	projectsRaw, ok := config["projects"]
	if !ok {
		return 0, nil, nil // No projects configured
	}
	projects, ok := projectsRaw.(map[string]interface{})
	if !ok {
		return 0, nil, nil // Invalid projects format
	}

	// Find stale entries
	var stalePaths []string
	for path := range projects {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			stalePaths = append(stalePaths, path)
		}
	}

	// Sort for consistent output
	sort.Strings(stalePaths)

	return len(stalePaths), stalePaths, nil
}
