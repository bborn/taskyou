// task is the local CLI for managing tasks.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/autocomplete"
	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/events"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/github"
	"github.com/bborn/workflow/internal/hooks"
	"github.com/bborn/workflow/internal/mcp"
	"github.com/bborn/workflow/internal/pipeline"
	"github.com/bborn/workflow/internal/ui"
	"github.com/bborn/workflow/internal/web"
)

var (
	version = "dev"

	// Styles for CLI output
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	boldStyle    = lipgloss.NewStyle().Bold(true)
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
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

// taskEmitter holds the process-wide events emitter so short-lived CLI
// commands can flush pending hooks via waitForEventHooks before exit.
var taskEmitter *events.Emitter

// openTaskDB opens the task database and registers the events emitter so any
// caller of UpdateTaskStatus (CLI commands, TUI, Claude hooks, MCP) fires
// task.blocked/task.completed lifecycle hooks. Otherwise only the daemon
// process emits these events and external watchers miss most transitions.
func openTaskDB(path string) (*db.DB, error) {
	database, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	if taskEmitter == nil {
		taskEmitter = events.New(hooks.DefaultHooksDir())
	}
	database.SetEventEmitter(taskEmitter)
	return database, nil
}

// waitForEventHooks blocks until any in-flight hook scripts have completed.
// CLI commands that mutate task state must defer this before exit, otherwise
// the Go process terminates before the hook goroutine runs its subprocess.
func waitForEventHooks() {
	if taskEmitter != nil {
		taskEmitter.Wait()
	}
}

func main() {
	// Strip any inherited CLAUDE_CONFIG_DIR so config-dir resolution is driven only
	// by per-project config (DB) with a fixed ~/.claude default — consistent across
	// the daemon, TUI, MCP server, and CLI no matter which shell launched each. An
	// inherited value (e.g. a daemon started from inside a ~/.claude-ik Claude
	// session) otherwise makes processes disagree about where a task's session lives,
	// which silently destroys in-progress conversations on resume. Must run before
	// any config-dir resolution or Claude spawn. See executor.NormalizeClaudeConfigEnv.
	executor.NormalizeClaudeConfigEnv()

	var dangerous bool

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

			debugStatePath, _ := cmd.Flags().GetString("debug-state-file")
			cpuProfilePath, _ := cmd.Flags().GetString("cpuprofile")
			memProfilePath, _ := cmd.Flags().GetString("memprofile")

			// Run locally
			if err := runLocal(dangerous, debugStatePath, cpuProfilePath, memProfilePath); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}

	rootCmd.SetVersionTemplate(`{{.Version}}
`)

	rootCmd.PersistentFlags().BoolVar(&dangerous, "dangerous", false, "Run Claude with --dangerously-skip-permissions (for sandboxed environments)")
	rootCmd.PersistentFlags().String("debug-state-file", "", "Path to write debug state JSON on update")
	rootCmd.PersistentFlags().String("cpuprofile", "", "Write a CPU profile here while the TUI runs (analyze with: go tool pprof)")
	rootCmd.PersistentFlags().String("memprofile", "", "Write a heap profile here when the TUI exits")

	// Version deprecation warning for CLI subcommands.
	// Skip for root (TUI has its own check), upgrade, daemon, mcp-server, and claude-hook.
	skipVersionCheck := map[string]bool{
		"ty":          true, // root command (TUI)
		"upgrade":     true,
		"daemon":      true,
		"mcp-server":  true,
		"claude-hook": true,
	}
	rootCmd.PersistentPostRun = func(cmd *cobra.Command, args []string) {
		// Flush any pending event hook goroutines kicked off by the command.
		// Without this, short-lived CLI commands exit before `task.completed`
		// and similar hook scripts can append to notifications.jsonl.
		waitForEventHooks()
		if skipVersionCheck[cmd.Name()] {
			return
		}
		if release := github.CLIVersionCheck(version); release != nil {
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, warnStyle.Render(
				fmt.Sprintf("Update available: %s → %s  (run: ty upgrade)", version, release.Version),
			))
		}
	}

	// Debug subcommand
	debugCmd := &cobra.Command{
		Use:   "debug",
		Short: "Debugging tools",
	}
	rootCmd.AddCommand(debugCmd)

	// Debug state subcommand
	debugStateCmd := &cobra.Command{
		Use:   "state",
		Short: "Dump application state (Model) as JSON",
		Long: `Dump the application state (Model) as structured JSON.
		
This is useful for debugging and for AI agents to verify UI logic.

Examples:
  ty debug state
  ty debug state --keys "Down,Down,Enter"  # Simulate key presses
  ty debug state --keys "n,test task,Enter" # Simulate creating a task`,
		Run: func(cmd *cobra.Command, args []string) {
			keys, _ := cmd.Flags().GetString("keys")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			cwd, _ := os.Getwd()
			exec := executor.New(database, config.New(database))
			model := ui.NewAppModel(database, exec, cwd, version)

			// Load tasks synchronously to ensure model is populated
			tasks, err := database.ListTasks(db.ListTasksOptions{
				IncludeClosed: true,
				Limit:         1000,
			})
			if err == nil {
				model.SetTasks(tasks)

				// Also update window size to something reasonable
				model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			}

			// Simulate key presses if provided
			if keys != "" {
				keyEvents := parseKeyEvents(keys)
				for _, msg := range keyEvents {
					model.Update(msg)
				}
			}

			// Generate and print state
			state := model.GenerateDebugState()
			data, _ := json.MarshalIndent(state, "", "  ")
			fmt.Println(string(data))
		},
	}
	debugStateCmd.Flags().String("keys", "", "Comma-separated list of keys to simulate (e.g., 'Down,Enter,n')")
	debugCmd.AddCommand(debugStateCmd)

	// Daemon subcommand - runs executor in background
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the task executor daemon",
		Long:  "Runs the background executor that processes queued tasks.",
		Run: func(cmd *cobra.Command, args []string) {
			// Set dangerous mode env var if flag is enabled
			// This is checked by executors when spawning Claude sessions
			if dangerous {
				os.Setenv("WORKTREE_DANGEROUS_MODE", "1")
			}
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

			// Use --dangerous flag (persistent from root cmd), falling back to env var
			restartDangerous := dangerous || os.Getenv("WORKTREE_DANGEROUS_MODE") == "1"
			if err := ensureDaemonRunning(restartDangerous); err != nil {
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
			modeFile := pidFile + ".mode"
			if pid, err := readPidFile(pidFile); err == nil && processExists(pid) {
				mode := "safe"
				if m, err := os.ReadFile(modeFile); err == nil {
					mode = string(m)
				}
				fmt.Println(successStyle.Render(fmt.Sprintf("Daemon running (pid %d, %s mode)", pid, mode)))
			} else {
				fmt.Println(dimStyle.Render("Daemon not running"))
			}
		},
	}
	daemonCmd.AddCommand(daemonStatusCmd)

	rootCmd.AddCommand(daemonCmd)

	// Restart subcommand - restart daemon and TUI (preserves agent sessions by default)
	var hardRestart bool
	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon and TUI (preserves agent sessions)",
		Long: `Restarts the daemon and TUI while preserving running agent sessions.
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
				// Soft restart: only kill the task-ui session, preserve task-daemon sessions with agent windows
				fmt.Println(dimStyle.Render("Preserving agent sessions..."))
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

			syscall.Exec(executable, []string{executable}, os.Environ())
		},
	}
	restartCmd.Flags().BoolVar(&hardRestart, "hard", false, "Kill all tmux sessions (full reset)")
	rootCmd.AddCommand(restartCmd)

	// Recover subcommand - fix stale references after crash
	recoverCmd := &cobra.Command{
		Use:   "recover",
		Short: "Fix stale tmux references after a crash",
		Long: `Clears stale daemon_session and tmux_window_id references from tasks.

Use this after your computer crashes or the daemon dies unexpectedly.
Tasks will automatically reconnect to their agent sessions when viewed.`,
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
		Args:  cobra.NoArgs, // takes no positional args; reject them instead of silently ignoring (e.g. `ty logs 4013`)
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

	// Worktree write-guard subcommand - the executor-agnostic transport for the
	// worktree write-guard used by the codex/gemini/opencode pre-tool hooks. Reads a
	// normalized pre-tool payload on stdin, evaluates EvaluateWorktreeWriteGuard, and
	// renders the decision in the format the given executor expects (internal use).
	worktreeGuardCmd := &cobra.Command{
		Use:    "worktree-guard",
		Short:  "Evaluate the worktree write-guard for a pre-tool hook",
		Hidden: true, // Internal use only - invoked by codex/gemini/opencode hooks
		Run: func(cmd *cobra.Command, args []string) {
			format, _ := cmd.Flags().GetString("format")
			// Never block the agent on our own failure: handle errors by allowing.
			if err := handleWorktreeGuardHook(format); err != nil {
				os.Exit(0)
			}
		},
	}
	worktreeGuardCmd.Flags().String("format", "", "Output format: codex | gemini | opencode")
	rootCmd.AddCommand(worktreeGuardCmd)

	// MCP server subcommand - runs the workflow MCP server for a task (internal use)
	mcpServerCmd := &cobra.Command{
		Use:    "mcp-server",
		Short:  "Run the workflow MCP server for a task",
		Hidden: true, // Internal use only - invoked by Claude Code via .mcp.json
		Run: func(cmd *cobra.Command, args []string) {
			taskID, _ := cmd.Flags().GetInt64("task-id")
			if taskID == 0 {
				// Also check WORKTREE_TASK_ID environment variable
				if taskIDStr := os.Getenv("WORKTREE_TASK_ID"); taskIDStr != "" {
					fmt.Sscanf(taskIDStr, "%d", &taskID)
				}
			}
			if taskID == 0 {
				fmt.Fprintln(os.Stderr, "task-id is required (via --task-id flag or WORKTREE_TASK_ID env)")
				os.Exit(1)
			}
			if err := runMCPServer(taskID); err != nil {
				fmt.Fprintln(os.Stderr, "MCP server error:", err)
				os.Exit(1)
			}
		},
	}
	mcpServerCmd.Flags().Int64("task-id", 0, "Task ID for the MCP server")
	rootCmd.AddCommand(mcpServerCmd)

	// Sessions subcommand - manage running agent sessions (supports all executors)
	sessionsCmd := &cobra.Command{
		Use:   "sessions",
		Short: "Manage running agent tmux sessions",
		Run: func(cmd *cobra.Command, args []string) {
			listSessions()
		},
	}

	sessionsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List running agent sessions",
		Run: func(cmd *cobra.Command, args []string) {
			listSessions()
		},
	}
	sessionsCmd.AddCommand(sessionsListCmd)

	sessionsCleanupCmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Kill orphaned agent processes not tied to active task windows",
		Run: func(cmd *cobra.Command, args []string) {
			force, _ := cmd.Flags().GetBool("force")
			cleanupOrphanedSessions(force)
		},
	}
	sessionsCleanupCmd.Flags().BoolP("force", "f", false, "Use SIGKILL instead of SIGTERM to force kill processes")
	sessionsCmd.AddCommand(sessionsCleanupCmd)

	sessionsSuspendCmd := &cobra.Command{
		Use:   "suspend [task-id...]",
		Short: "Kill agent processes for blocked tasks while preserving resume capability",
		Long: `Suspend blocked task sessions by killing their agent processes and tmux windows.
The session ID is preserved in the database so tasks can be resumed later via 'ty retry'.

By default, suspends all blocked tasks that have running sessions.
Optionally specify one or more task IDs to suspend specific tasks.

Examples:
  ty sessions suspend           # Suspend all blocked tasks with running sessions
  ty sessions suspend 42 43     # Suspend specific tasks
  ty sessions suspend --all     # Suspend all tasks with running sessions (not just blocked)`,
		Run: func(cmd *cobra.Command, args []string) {
			all, _ := cmd.Flags().GetBool("all")
			var taskIDs []int
			for _, arg := range args {
				var id int
				if _, err := fmt.Sscanf(arg, "%d", &id); err == nil {
					taskIDs = append(taskIDs, id)
				} else {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+arg))
					os.Exit(1)
				}
			}
			suspendSessions(taskIDs, all)
		},
	}
	sessionsSuspendCmd.Flags().Bool("all", false, "Suspend all tasks with running sessions, not just blocked ones")
	sessionsCmd.AddCommand(sessionsSuspendCmd)

	rootCmd.AddCommand(sessionsCmd)

	// Alias: claudes -> sessions (for backwards compatibility)
	claudesCmd := &cobra.Command{
		Use:    "claudes",
		Short:  "Alias for 'sessions' (deprecated, use 'sessions' instead)",
		Hidden: true, // Hide from help but still works
		Run: func(cmd *cobra.Command, args []string) {
			listSessions()
		},
	}
	rootCmd.AddCommand(claudesCmd)

	// Delete subcommand - delete a task, kill its agent session, and remove worktree
	deleteCmd := &cobra.Command{
		Use:               "delete <task-id>",
		Short:             "Delete a task, kill its agent session, and remove its worktree",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeTaskIDs,
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			// Get task info for confirmation
			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
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
  task create "Urgent bug" --tags "bug,urgent" --pinned  # Tagged and pinned task
  task create --body "The login button is broken on mobile devices" # AI generates title
  task create "QA: PR #2526" --branch fix/ui-overflow --project myapp  # Checkout existing branch`,
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var title string
			if len(args) > 0 {
				title = args[0]
			}
			body, _ := cmd.Flags().GetString("body")
			body = unescapeNewlines(body) // Convert literal \n to actual newlines
			taskType, _ := cmd.Flags().GetString("type")
			project, _ := cmd.Flags().GetString("project")
			taskExecutor, _ := cmd.Flags().GetString("executor")
			effortLevel, _ := cmd.Flags().GetString("effort")
			modelOverride, _ := cmd.Flags().GetString("model")
			execute, _ := cmd.Flags().GetBool("execute")
			createDangerous, _ := cmd.Flags().GetBool("dangerous")
			permissionModeFlag, _ := cmd.Flags().GetString("permission-mode")
			tags, _ := cmd.Flags().GetString("tags")
			pinned, _ := cmd.Flags().GetBool("pinned")
			remoteControl, _ := cmd.Flags().GetBool("remote-control")
			branch, _ := cmd.Flags().GetString("branch")
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
			database, err := openTaskDB(dbPath)
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

			// Validate effort level if provided (empty = use Claude's global default)
			effortLevel = strings.ToLower(strings.TrimSpace(effortLevel))
			if !db.IsValidEffortLevel(effortLevel) {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid effort. Must be one of: "+strings.Join(db.EffortLevels(), ", ")))
				os.Exit(1)
			}

			// Normalize model override (empty = use Claude's global default). Any
			// non-empty value is accepted; the Claude CLI validates the model name.
			modelOverride = strings.TrimSpace(modelOverride)

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

			// Resolve permission mode: an explicit --permission-mode wins, then the
			// legacy --dangerous flag; empty inherits the project default in CreateTask.
			permMode := db.NormalizePermissionMode(permissionModeFlag)
			if permMode == "" && createDangerous {
				permMode = db.PermissionModeDangerous
			}

			// Create the task
			task := &db.Task{
				Title:          title,
				Body:           body,
				Status:         status,
				Type:           taskType,
				Project:        project,
				Executor:       taskExecutor,
				EffortLevel:    effortLevel,
				Model:          modelOverride,
				Tags:           tags,
				Pinned:         pinned,
				SourceBranch:   branch,
				PermissionMode: permMode,
				RemoteControl:  remoteControl,
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
				if task.SourceBranch != "" {
					output["source_branch"] = task.SourceBranch
				}
				if task.EffortLevel != "" {
					output["effort_level"] = task.EffortLevel
				}
				if task.Model != "" {
					output["model"] = task.Model
				}
				jsonBytes, _ := json.Marshal(output)
				fmt.Println(string(jsonBytes))
			} else {
				msg := fmt.Sprintf("Created task #%d: %s", task.ID, task.Title)
				if branch != "" {
					msg += fmt.Sprintf(" (branch: %s)", branch)
				}
				if execute {
					if createDangerous {
						msg += " (queued for execution in dangerous mode)"
					} else {
						msg += " (queued for execution)"
					}
				}
				fmt.Println(successStyle.Render(msg))
			}
		},
	}
	createCmd.Flags().String("body", "", "Task body/description (if no title, AI generates from body)")
	createCmd.Flags().StringP("type", "t", "", "Task type: code, writing, thinking (default: code)")
	createCmd.Flags().StringP("project", "p", "", "Project name (auto-detected from cwd if not specified)")
	createCmd.Flags().StringP("executor", "e", "", "Task executor: claude, codex, gemini, pi, opencode, openclaw (default: claude)")
	createCmd.Flags().String("effort", "", "Per-task Claude effort override: low, medium, high, xhigh, max (default: Claude's global default)")
	createCmd.Flags().String("model", "", "Per-task Claude model override: opus, sonnet, haiku, or a full model name (default: Claude's global default)")
	createCmd.Flags().BoolP("execute", "x", false, "Queue task for immediate execution")
	createCmd.Flags().Bool("dangerous", false, "Execute in dangerous mode (alias for --permission-mode dangerous)")
	createCmd.Flags().String("permission-mode", "", "Permission mode: default (prompt), accept-edits (auto-accept file edits), auto (Claude Code auto mode: auto-approve safe actions, block risky ones), dangerous (skip all). Defaults to the project's setting")
	createCmd.Flags().String("tags", "", "Task tags (comma-separated)")
	createCmd.Flags().Bool("pinned", false, "Pin the task to the top of its column")
	createCmd.Flags().Bool("remote-control", false, "Launch Claude with --remote-control (interactive, remote-drivable session)")
	createCmd.Flags().StringP("branch", "b", "", "Existing branch to checkout for worktree (e.g., fix/ui-overflow)")
	createCmd.Flags().Bool("json", false, "Output in JSON format")
	createCmd.RegisterFlagCompletionFunc("project", completeFlagProjects)
	createCmd.RegisterFlagCompletionFunc("type", completeFlagTypes)
	createCmd.RegisterFlagCompletionFunc("executor", completeFlagExecutors)
	createCmd.RegisterFlagCompletionFunc("effort", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return db.EffortLevels(), cobra.ShellCompDirectiveNoFileComp
	})
	createCmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return db.ModelOptions(), cobra.ShellCompDirectiveNoFileComp
	})
	rootCmd.AddCommand(createCmd)

	// Pipeline subcommand - create a multi-phase pipeline task
	pipelineCmd := &cobra.Command{
		Use:   "pipeline [goal]",
		Short: "Create a multi-model plan → code → review pipeline for a goal",
		Long: `Create a workflow: one goal broken into a small DAG of step tasks, each routed
to its own executor and model, that hand work forward on a single shared branch
and advance automatically — sequential where steps depend on each other, parallel
where they don't.

The default 'plan-code-review' workflow runs five steps on one branch:
  Plan     (Claude / Opus)   — writes PLAN.md, no code
  Code     (Claude / Sonnet) — implements the plan
  Review A (Claude / Opus)   \
  Review B (Claude / Sonnet)  } two independent reviewers, in parallel
  Collect  (Claude / Sonnet) — merges reviews, applies fixes, opens the PR

Each step's executor and model are configurable per project and persisted, so you
set them once (e.g. "Review B runs on codex here") and every workflow in that
project uses your choices. See 'task pipeline config'.

Each step commits and pushes the shared branch, so the workflow needs a project
that uses git worktrees and has a remote to push to.

Examples:
  task pipeline "Add rate limiting to the API" --project myapp
  task pipeline "Refactor the auth module" -p myapp --permission-mode dangerous
  task pipeline config -p myapp --set "Review B=codex"  # configure steps
  task pipeline --list  # show available pipeline definitions`,
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			listDefs, _ := cmd.Flags().GetBool("list")
			if listDefs {
				for _, d := range pipeline.Definitions() {
					stepLabels := make([]string, 0, len(d.Steps))
					for _, s := range d.Steps {
						label := s.Name + " (" + s.Executor
						if s.Model != "" {
							label += "/" + s.Model
						}
						label += ")"
						if len(s.Deps) > 0 {
							label += " ← " + strings.Join(s.Deps, "+")
						}
						stepLabels = append(stepLabels, label)
					}
					fmt.Printf("%s\n  %s\n  steps: %s\n", successStyle.Render(d.Name), d.Description, strings.Join(stepLabels, " · "))
				}
				return
			}

			var goal string
			if len(args) > 0 {
				goal = args[0]
			}
			body, _ := cmd.Flags().GetString("body")
			body = unescapeNewlines(body)
			if strings.TrimSpace(goal) == "" {
				goal = body
			}
			if strings.TrimSpace(goal) == "" {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: a goal (positional argument or --body) is required"))
				os.Exit(1)
			}

			project, _ := cmd.Flags().GetString("project")
			definition, _ := cmd.Flags().GetString("definition")
			permissionModeFlag, _ := cmd.Flags().GetString("permission-mode")
			createDangerous, _ := cmd.Flags().GetBool("dangerous")
			noExecute, _ := cmd.Flags().GetBool("no-execute")
			outputJSON, _ := cmd.Flags().GetBool("json")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			// Detect project from cwd if not specified.
			if project == "" {
				if cwd, err := os.Getwd(); err == nil {
					if p, err := database.GetProjectByPath(cwd); err == nil && p != nil {
						project = p.Name
					}
				}
			}
			if project == "" {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: could not determine project; pass --project"))
				os.Exit(1)
			}

			permMode := db.NormalizePermissionMode(permissionModeFlag)
			if permMode == "" && createDangerous {
				permMode = db.PermissionModeDangerous
			}

			result, err := pipeline.Create(database, pipeline.Options{
				Goal:           goal,
				Project:        project,
				Definition:     definition,
				PermissionMode: permMode,
				Execute:        !noExecute,
			})
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			if outputJSON {
				steps := make([]map[string]interface{}, 0, len(result.Tasks))
				for i, t := range result.Tasks {
					steps = append(steps, map[string]interface{}{
						"step":     result.Definition.Steps[i].Name,
						"deps":     result.Definition.Steps[i].Deps,
						"id":       t.ID,
						"executor": t.Executor,
						"model":    t.Model,
						"status":   t.Status,
					})
				}
				out := map[string]interface{}{
					"definition": result.Definition.Name,
					"branch":     result.Branch,
					"project":    project,
					"steps":      steps,
				}
				jsonBytes, _ := json.Marshal(out)
				fmt.Println(string(jsonBytes))
				return
			}

			fmt.Println(successStyle.Render(fmt.Sprintf("Created %s workflow on branch %s", result.Definition.Name, result.Branch)))
			for i, t := range result.Tasks {
				s := result.Definition.Steps[i]
				model := s.Model
				if model == "" {
					model = "default"
				}
				dep := ""
				if len(s.Deps) > 0 {
					dep = "  ← " + strings.Join(s.Deps, "+")
				}
				fmt.Printf("  #%d  %-9s %s/%s  (%s)%s\n", t.ID, s.Name, t.Executor, model, t.Status, dep)
			}
			if noExecute {
				fmt.Println(dimStyle.Render("Staged but not started — queue the root step to run it."))
			} else {
				fmt.Println(dimStyle.Render("Running — steps advance automatically; parallel reviewers run at once."))
			}
		},
	}
	pipelineCmd.Flags().String("body", "", "Goal text (alternative to the positional argument)")
	pipelineCmd.Flags().StringP("project", "p", "", "Project name (auto-detected from cwd if not specified)")
	pipelineCmd.Flags().StringP("definition", "d", pipeline.DefaultDefinition, "Pipeline definition to use")
	pipelineCmd.Flags().String("permission-mode", "", "Permission mode for every phase: default, accept-edits, auto, dangerous (defaults to the project's setting)")
	pipelineCmd.Flags().Bool("dangerous", false, "Run every phase in dangerous mode (alias for --permission-mode dangerous)")
	pipelineCmd.Flags().Bool("no-execute", false, "Stage the workflow without queuing the root step")
	pipelineCmd.Flags().Bool("list", false, "List available workflow definitions and exit")
	pipelineCmd.Flags().Bool("json", false, "Output in JSON format")
	pipelineCmd.RegisterFlagCompletionFunc("project", completeFlagProjects)
	pipelineCmd.RegisterFlagCompletionFunc("definition", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return pipeline.DefinitionNames(), cobra.ShellCompDirectiveNoFileComp
	})

	// Pipeline config subcommand - view/set per-project step executors & models
	pipelineConfigCmd := &cobra.Command{
		Use:   "config",
		Short: "View or set the per-project executor/model for each workflow step",
		Long: `Configure which executor and model each workflow step runs on, per project.
Set it once for a project and every workflow there defaults to your choices.

With no --set/--reset flags, prints the project's current configuration.

Examples:
  task pipeline config -p myapp                            # show current config
  task pipeline config -p myapp --set "Review B=codex"     # cross-executor review
  task pipeline config -p myapp --set "Plan=claude/opus" --set "Code=claude/sonnet"
  task pipeline config -p myapp --reset                    # revert to built-in defaults`,
		Run: func(cmd *cobra.Command, args []string) {
			project, _ := cmd.Flags().GetString("project")
			definition, _ := cmd.Flags().GetString("definition")
			sets, _ := cmd.Flags().GetStringArray("set")
			reset, _ := cmd.Flags().GetBool("reset")
			outputJSON, _ := cmd.Flags().GetBool("json")

			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			if project == "" {
				if cwd, err := os.Getwd(); err == nil {
					if p, err := database.GetProjectByPath(cwd); err == nil && p != nil {
						project = p.Name
					}
				}
			}
			if project == "" {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: could not determine project; pass --project"))
				os.Exit(1)
			}

			def, ok := pipeline.Get(definition)
			if !ok {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: unknown workflow definition "+definition))
				os.Exit(1)
			}

			if reset {
				if err := pipeline.ClearConfig(database, project, def.Name); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
				fmt.Println(successStyle.Render(fmt.Sprintf("Reset %s workflow config for %s to defaults", def.Name, project)))
			}

			if len(sets) > 0 {
				// Start from the current effective config so partial --set edits merge.
				cfg := pipeline.EffectiveConfig(database, project, def)
				byName := map[string]int{}
				for i, c := range cfg {
					byName[strings.ToLower(c.Name)] = i
				}
				validExecutors := map[string]bool{
					db.ExecutorClaude: true, db.ExecutorCodex: true, db.ExecutorGemini: true,
					db.ExecutorPi: true, db.ExecutorOpenCode: true, db.ExecutorOpenClaw: true,
				}
				for _, s := range sets {
					name, exec, model, perr := parseStepSet(s)
					if perr != nil {
						fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+perr.Error()))
						os.Exit(1)
					}
					idx, found := byName[strings.ToLower(name)]
					if !found {
						fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error: no step %q in %s (steps: %s)", name, def.Name, stepNames(def))))
						os.Exit(1)
					}
					if !validExecutors[exec] {
						fmt.Fprintln(os.Stderr, errorStyle.Render("Error: invalid executor "+exec))
						os.Exit(1)
					}
					cfg[idx].Executor = exec
					cfg[idx].Model = model
				}
				if err := pipeline.SaveConfig(database, project, def.Name, cfg); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
			}

			// Print the resulting effective config.
			cfg := pipeline.EffectiveConfig(database, project, def)
			configured := pipeline.IsConfigured(database, project, def.Name)
			if outputJSON {
				out := map[string]interface{}{
					"project":    project,
					"definition": def.Name,
					"configured": configured,
					"steps":      cfg,
				}
				jsonBytes, _ := json.Marshal(out)
				fmt.Println(string(jsonBytes))
				return
			}
			origin := "defaults"
			if configured {
				origin = "configured"
			}
			fmt.Println(successStyle.Render(fmt.Sprintf("%s workflow for %s (%s)", def.Name, project, origin)))
			for _, c := range cfg {
				model := c.Model
				if model == "" {
					model = "default"
				}
				fmt.Printf("  %-9s %s/%s\n", c.Name, c.Executor, model)
			}
			fmt.Println(dimStyle.Render("Set with: task pipeline config -p " + project + " --set \"Review B=codex\""))
		},
	}
	pipelineConfigCmd.Flags().StringP("project", "p", "", "Project name (auto-detected from cwd if not specified)")
	pipelineConfigCmd.Flags().StringP("definition", "d", pipeline.DefaultDefinition, "Workflow definition to configure")
	pipelineConfigCmd.Flags().StringArray("set", nil, "Set a step executor/model, e.g. \"Review B=codex\" or \"Plan=claude/opus\" (repeatable)")
	pipelineConfigCmd.Flags().Bool("reset", false, "Clear saved config and revert to built-in defaults")
	pipelineConfigCmd.Flags().Bool("json", false, "Output in JSON format")
	pipelineConfigCmd.RegisterFlagCompletionFunc("project", completeFlagProjects)
	pipelineCmd.AddCommand(pipelineConfigCmd)

	rootCmd.AddCommand(pipelineCmd)

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
			tag, _ := cmd.Flags().GetString("tag")
			all, _ := cmd.Flags().GetBool("all")
			limit, _ := cmd.Flags().GetInt("limit")
			outputJSON, _ := cmd.Flags().GetBool("json")
			showPR, _ := cmd.Flags().GetBool("pr")

			// Open database
			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			opts := db.ListTasksOptions{
				Status:        status,
				Project:       project,
				Type:          taskType,
				Tag:           tag,
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
	listCmd.Flags().String("tag", "", "Filter by tag (exact match, e.g. gm:cortex)")
	listCmd.Flags().BoolP("all", "a", false, "Include completed tasks")
	listCmd.Flags().IntP("limit", "n", 50, "Maximum number of tasks to return")
	listCmd.Flags().Bool("json", false, "Output in JSON format")
	listCmd.Flags().Bool("pr", false, "Show PR/CI status (requires network)")
	listCmd.RegisterFlagCompletionFunc("status", completeFlagStatuses)
	listCmd.RegisterFlagCompletionFunc("project", completeFlagProjects)
	listCmd.RegisterFlagCompletionFunc("type", completeFlagTypes)
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
			database, err := openTaskDB(dbPath)
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

			snapshot := web.BuildBoardSnapshot(tasks, limit)

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

	// Tail subcommand - live updating task view grouped by project and status
	tailCmd := &cobra.Command{
		Use:   "tail",
		Short: "Live view of tasks organized by project and status",
		Long: `Show a continuously updating view of all active tasks,
grouped by project and then by status. Refreshes automatically.
Press Ctrl+C to stop.`,
		Run: func(cmd *cobra.Command, args []string) {
			interval, _ := cmd.Flags().GetDuration("interval")
			showDone, _ := cmd.Flags().GetBool("done")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			p := tea.NewProgram(
				tailModel{
					db:       database,
					interval: interval,
					showDone: showDone,
				},
				tea.WithAltScreen(),
			)
			if _, err := p.Run(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	tailCmd.Flags().Duration("interval", 2*time.Second, "Refresh interval (e.g. 1s, 500ms)")
	tailCmd.Flags().Bool("done", false, "Include completed tasks")
	rootCmd.AddCommand(tailCmd)

	// Show subcommand - show task details
	showCmd := &cobra.Command{
		Use:               "show <task-id>",
		Short:             "Show task details",
		ValidArgsFunction: completeTaskIDs,
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
			database, err := openTaskDB(dbPath)
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
					"id":             task.ID,
					"title":          task.Title,
					"body":           task.Body,
					"status":         task.Status,
					"type":           task.Type,
					"project":        task.Project,
					"executor":       task.Executor,
					"worktree":       task.WorktreePath,
					"branch":         task.BranchName,
					"claude_pane_id": task.ClaudePaneID,
					"shell_pane_id":  task.ShellPaneID,
					"summary":        task.Summary,
					"created_at":     task.CreatedAt.Time.Format(time.RFC3339),
					"updated_at":     task.UpdatedAt.Time.Format(time.RFC3339),
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

				// Summary (always shown if present)
				if task.Summary != "" {
					fmt.Println()
					fmt.Println(boldStyle.Render("Summary:"))
					fmt.Println(task.Summary)
				}

				// If blocked, show the last question
				if task.Status == db.StatusBlocked {
					logs, _ := database.GetTaskLogs(taskID, 50)
					for _, l := range logs {
						if l.LineType == "question" {
							fmt.Println()
							fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Bold(true).Render("Waiting on:"))
							fmt.Println(l.Content)
							break
						}
					}
				}

				// Logs
				if showLogs {
					logs, _ := database.GetTaskLogs(taskID, 100)
					if len(logs) > 0 {
						fmt.Println()
						fmt.Println(boldStyle.Render("Recent Logs:"))
						for _, l := range logs {
							ts := dimStyle.Render(l.CreatedAt.Time.Format("15:04:05"))
							prefix := ""
							switch l.LineType {
							case "system":
								prefix = dimStyle.Render("[system] ")
							case "error":
								prefix = errorStyle.Render("[error] ")
							case "tool":
								prefix = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B5CF6")).Render("[tool] ")
							case "question":
								prefix = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Render("[question] ")
							case "output":
								prefix = dimStyle.Render("[output] ")
							case "text":
								prefix = dimStyle.Render("[text] ")
							}
							fmt.Printf("%s %s%s\n", ts, prefix, truncate(l.Content, 200))
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
		Use:               "update <task-id>",
		Short:             "Update a task",
		ValidArgsFunction: completeTaskIDs,
		Long: `Update task fields.

Examples:
  task update 42 --title "New title"
  task update 42 --body "Updated description"
  task update 42 --executor codex        # Switch to Codex executor
  task update 42 --tags "bug,urgent"     # Set tags
  task update 42 --pinned                # Pin the task`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			title, _ := cmd.Flags().GetString("title")
			body, _ := cmd.Flags().GetString("body")
			body = unescapeNewlines(body) // Convert literal \n to actual newlines
			taskType, _ := cmd.Flags().GetString("type")
			project, _ := cmd.Flags().GetString("project")
			taskExecutor, _ := cmd.Flags().GetString("executor")
			tags, _ := cmd.Flags().GetString("tags")
			pinned, _ := cmd.Flags().GetBool("pinned")

			// Open database
			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
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

			// Validate executor if provided
			if taskExecutor != "" {
				validExecutors := []string{db.ExecutorClaude, db.ExecutorCodex, db.ExecutorGemini, db.ExecutorPi, db.ExecutorOpenCode, db.ExecutorOpenClaw}
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
			if taskExecutor != "" {
				task.Executor = taskExecutor
			}
			if cmd.Flags().Changed("tags") {
				task.Tags = tags
			}
			if cmd.Flags().Changed("pinned") {
				task.Pinned = pinned
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
	updateCmd.Flags().StringP("executor", "e", "", "Update task executor: claude, codex, gemini, pi, opencode, openclaw")
	updateCmd.Flags().String("tags", "", "Update task tags (comma-separated)")
	updateCmd.Flags().Bool("pinned", false, "Pin or unpin the task")
	updateCmd.RegisterFlagCompletionFunc("project", completeFlagProjects)
	updateCmd.RegisterFlagCompletionFunc("type", completeFlagTypes)
	updateCmd.RegisterFlagCompletionFunc("executor", completeFlagExecutors)
	rootCmd.AddCommand(updateCmd)

	// Move subcommand - move a task to a different project
	moveCmd := &cobra.Command{
		Use:               "move <task-id> <target-project>",
		Short:             "Move a task to a different project",
		ValidArgsFunction: completeTaskIDsThenProject,
		Long: `Move a task to a different project.

This properly cleans up the task's worktree and agent sessions from the old project,
deletes the old task, and creates a new task in the target project.

The new task preserves:
- Title, body, type, and tags
- Status (unless processing/blocked, which resets to backlog)

The new task resets:
- Worktree path, branch name, port
- Agent session ID, daemon session
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
			database, err := openTaskDB(dbPath)
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
			moveDangerous, _ := cmd.Flags().GetBool("dangerous")

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
				if moveDangerous {
					if err := database.UpdateTaskDangerousMode(newTaskID, true); err != nil {
						fmt.Fprintln(os.Stderr, errorStyle.Render("Error setting dangerous mode: "+err.Error()))
						os.Exit(1)
					}
				}
				if err := database.UpdateTaskStatus(newTaskID, db.StatusQueued); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error queueing task: "+err.Error()))
					os.Exit(1)
				}
				msg := fmt.Sprintf("Queued task #%d for execution", newTaskID)
				if moveDangerous {
					msg += " (dangerous mode)"
				}
				fmt.Println(successStyle.Render(msg))
			}
		},
	}
	moveCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	moveCmd.Flags().BoolP("execute", "e", false, "Queue the task for execution after moving")
	moveCmd.Flags().Bool("dangerous", false, "Execute in dangerous mode (requires --execute)")
	rootCmd.AddCommand(moveCmd)

	// Execute subcommand - queue a task for execution
	executeCmd := &cobra.Command{
		Use:               "execute <task-id>",
		Aliases:           []string{"queue"},
		Short:             "Queue a task for execution",
		ValidArgsFunction: completeTaskIDs,
		Long: `Queue a task to be executed by the daemon.

Examples:
  task execute 42
  task queue 42
  task run 42                   # "run" with a task ID also queues it
  task execute 42 --dangerous   # Execute in dangerous mode`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			executeDangerous, _ := cmd.Flags().GetBool("dangerous")
			executePermMode, _ := cmd.Flags().GetString("permission-mode")
			executePermMode = db.NormalizePermissionMode(executePermMode)
			if executePermMode == "" && executeDangerous {
				executePermMode = db.PermissionModeDangerous
			}

			// Open database
			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
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

			// Override the task's permission mode if explicitly requested.
			if executePermMode != "" {
				if err := database.UpdateTaskPermissionMode(taskID, executePermMode); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error setting permission mode: "+err.Error()))
					os.Exit(1)
				}
				task.PermissionMode = executePermMode
			}

			if err := database.UpdateTaskStatus(taskID, db.StatusQueued); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			msg := fmt.Sprintf("Queued task #%d: %s", taskID, task.Title)
			switch task.EffectivePermissionMode() {
			case db.PermissionModeDangerous:
				msg += " (dangerous mode)"
			case db.PermissionModeAuto:
				msg += " (auto mode)"
			case db.PermissionModeAcceptEdits:
				msg += " (accept-edits mode)"
			}
			fmt.Println(successStyle.Render(msg))
		},
	}
	executeCmd.Flags().Bool("dangerous", false, "Execute in dangerous mode (alias for --permission-mode dangerous)")
	executeCmd.Flags().String("permission-mode", "", "Override permission mode: default (prompt), accept-edits (auto-accept file edits), auto (Claude Code auto mode), dangerous (skip all)")
	rootCmd.AddCommand(executeCmd)

	// Routines: named unattended agent runs (ty run <name>, ty routines ...).
	// `ty run <task-id>` keeps the old execute-alias behavior via dispatch.
	rootCmd.AddCommand(newRunCmd(executeCmd))
	rootCmd.AddCommand(newRoutinesCmd())

	statusCmd := &cobra.Command{
		Use:               "status <task-id> <status>",
		Short:             "Set a task's status",
		ValidArgsFunction: completeTaskIDsThenStatus,
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
			database, err := openTaskDB(dbPath)
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
		Use:               "pin <task-id>",
		Short:             "Pin, unpin, or toggle a task",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeTaskIDs,
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			unpin, _ := cmd.Flags().GetBool("unpin")
			toggle, _ := cmd.Flags().GetBool("toggle")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
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
		Use:               "close <task-id>",
		ValidArgsFunction: completeTaskIDs,
		Aliases:           []string{"done", "complete"},
		Short:             "Mark a task as done",
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
			database, err := openTaskDB(dbPath)
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

			// Note: We intentionally do NOT kill the agent session when closing a task.
			// The tmux window is kept around so users can review the agent's work.
			// Use 'task sessions cleanup' or 'task delete <id>' to clean up windows.

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
		Use:               "retry <task-id>",
		Short:             "Retry a blocked or failed task",
		ValidArgsFunction: completeTaskIDs,
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
			database, err := openTaskDB(dbPath)
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

	// Input subcommand - send input directly to a running task's executor
	inputCmd := &cobra.Command{
		Use:               "input <task-id> [message]",
		Short:             "Send input to a task's executor",
		ValidArgsFunction: completeTaskIDs,
		Long: `Send input directly to a running task's executor via tmux.

This allows you to interact with a blocked or running task without going through
the retry mechanism. The input is sent directly to the executor's tmux pane.

By default the message is submitted (Enter is pressed after the text), so the
agent actually receives it. Pass --no-submit to only drop the text in the input
field without submitting it.

If no message is provided, reads from stdin (useful for piping).
Use --enter to just send Enter (for confirming TUI prompts).
Use --key to send special keys like "Up", "Down", "Tab", "Escape".

Examples:
  task input 42 "yes"                # Type "yes" and submit
  task input 42 "Try a different approach"
  task input 42 "draft text" --no-submit   # Fill the field, don't submit
  task input 42 --enter              # Just press Enter
  task input 42 --key Down --enter   # Press Down then Enter
  echo "continue" | task input 42`,
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			justEnter, _ := cmd.Flags().GetBool("enter")
			specialKey, _ := cmd.Flags().GetString("key")
			noSubmit, _ := cmd.Flags().GetBool("no-submit")

			var message string
			if len(args) > 1 {
				message = strings.Join(args[1:], " ")
			} else if !justEnter && specialKey == "" {
				// Read from stdin only if not using --enter or --key
				scanner := bufio.NewScanner(os.Stdin)
				if scanner.Scan() {
					message = scanner.Text()
				}
				if err := scanner.Err(); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error reading stdin: "+err.Error()))
					os.Exit(1)
				}
			}

			if message == "" && !justEnter && specialKey == "" {
				fmt.Fprintln(os.Stderr, errorStyle.Render("No input provided (use --enter to send just Enter, or --key for special keys)"))
				os.Exit(1)
			}

			// Open database
			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
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

			// Get the pane ID
			paneID := task.ClaudePaneID
			if paneID == "" {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d has no executor pane (not running?)", taskID)))
				os.Exit(1)
			}

			// Build and send tmux send-keys commands
			// If --key specified, send that first
			if specialKey != "" {
				keyCmd := osexec.Command("tmux", "send-keys", "-t", paneID, specialKey)
				if err := keyCmd.Run(); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error sending key to pane %s: %v", paneID, err)))
					os.Exit(1)
				}
			}

			// Send the message text (literally, so a message that looks like a tmux
			// key name such as "Enter" or "Up" isn't interpreted as a keypress).
			if message != "" {
				sendCmd := osexec.Command("tmux", "send-keys", "-t", paneID, "-l", message)
				if err := sendCmd.Run(); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error sending input to pane %s (task may have finished): %v", paneID, err)))
					os.Exit(1)
				}
			}

			// Submit by pressing Enter unless --no-submit was passed. Enter is sent
			// as a SEPARATE keypress after the text: agentic TUIs (Claude Code, etc.)
			// use bracketed-paste / input debouncing, so an Enter bundled into the
			// same send-keys call as the text gets absorbed as a newline instead of
			// submitting. The brief pause lets the TUI register the text first.
			submit := shouldSubmitInput(message, justEnter, noSubmit)
			if submit {
				if message != "" {
					time.Sleep(100 * time.Millisecond)
				}
				sendCmd := osexec.Command("tmux", "send-keys", "-t", paneID, "Enter")
				if err := sendCmd.Run(); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error sending Enter to pane %s (task may have finished): %v", paneID, err)))
					os.Exit(1)
				}
			}

			if message != "" && !submit {
				fmt.Println(successStyle.Render(fmt.Sprintf("Sent input to task #%d (not submitted)", taskID)))
			} else {
				fmt.Println(successStyle.Render(fmt.Sprintf("Sent input to task #%d", taskID)))
			}
		},
	}
	inputCmd.Flags().Bool("enter", false, "Just send Enter key (for confirming prompts)")
	inputCmd.Flags().String("key", "", "Send a special key (e.g., Up, Down, Tab, Escape)")
	inputCmd.Flags().Bool("no-submit", false, "Type the text but don't press Enter (leave it in the input field)")
	rootCmd.AddCommand(inputCmd)

	// Pi Wrapper subcommand - internal use for RPC mode
	rootCmd.AddCommand(piWrapperCmd)

	// Output subcommand - capture recent output from executor pane
	outputCmd := &cobra.Command{
		Use:               "output <task-id>",
		Short:             "Capture recent output from a task's executor",
		ValidArgsFunction: completeTaskIDs,
		Long: `Capture recent output from a running task's executor pane.

This allows you to see what the executor has outputted without attaching to the tmux pane.

Examples:
  task output 42
  task output 42 --lines 100`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			lines, _ := cmd.Flags().GetInt("lines")
			if lines <= 0 {
				lines = 50
			}

			// Open database
			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
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

			// Get the pane ID
			paneID := task.ClaudePaneID
			if paneID == "" {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d has no executor pane (not running?)", taskID)))
				fmt.Fprintln(os.Stderr, dimStyle.Render("Tip: use 'task show' to see what the task accomplished"))
				os.Exit(1)
			}

			// Capture pane content
			captureCmd := osexec.Command("tmux", "capture-pane", "-t", paneID, "-p", "-S", fmt.Sprintf("-%d", lines))
			output, err := captureCmd.Output()
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Executor pane no longer exists for task #%d", taskID)))
				fmt.Fprintln(os.Stderr, dimStyle.Render("Tip: use 'task show' to see what the task accomplished, or 'task show --logs' for full activity"))
				os.Exit(1)
			}

			fmt.Print(string(output))
		},
	}
	outputCmd.Flags().IntP("lines", "n", 50, "Number of lines to capture")
	rootCmd.AddCommand(outputCmd)

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

	// Worktrees cleanup subcommand - remove stale worktrees to reclaim disk space
	worktreesCmd := &cobra.Command{
		Use:   "worktrees",
		Short: "Manage git worktrees",
	}

	worktreesCleanupCmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Archive and remove stale worktrees to reclaim disk space",
		Long: `Archives and removes worktrees for done/archived tasks older than a threshold.

Worktrees are archived first (preserving uncommitted changes in git refs),
then the directory is removed. Archived tasks can be restored with 'unarchive'.

The default max age is 24 hours. Use --max-age to override.
Use --dry-run to preview what would be cleaned up.

Examples:
  task worktrees cleanup
  task worktrees cleanup --dry-run
  task worktrees cleanup --max-age 72h
  task worktrees cleanup --max-age 0  # clean up ALL done/archived worktrees`,
		Run: func(cmd *cobra.Command, args []string) {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			maxAgeStr, _ := cmd.Flags().GetString("max-age")

			maxAge := executor.DefaultWorktreeCleanupMaxAge
			if maxAgeStr != "" {
				if maxAgeStr == "0" {
					maxAge = 0
				} else {
					parsed, err := time.ParseDuration(maxAgeStr)
					if err != nil {
						fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid duration: "+maxAgeStr))
						os.Exit(1)
					}
					maxAge = parsed
				}
			}

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			cfg := config.New(database)
			exec := executor.New(database, cfg)

			tasks, err := exec.CleanupStaleWorktreesManual(maxAge, dryRun)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			if len(tasks) == 0 {
				if maxAge == 0 {
					fmt.Println(dimStyle.Render("No done/archived tasks with worktrees found"))
				} else {
					fmt.Println(dimStyle.Render(fmt.Sprintf("No stale worktrees older than %s found", maxAge)))
				}
				return
			}

			if dryRun {
				fmt.Printf("Would archive and remove %d worktree(s):\n", len(tasks))
				for _, t := range tasks {
					age := time.Since(t.CompletedAt.Time).Round(time.Hour)
					fmt.Printf("  #%-4d %-12s %-30s %s (age: %s)\n",
						t.ID, t.Project, truncate(t.Title, 30), dimStyle.Render(t.WorktreePath), age)
				}
			} else {
				fmt.Println(successStyle.Render(fmt.Sprintf("Archived and removed %d stale worktree(s)", len(tasks))))
				for _, t := range tasks {
					fmt.Printf("  #%-4d %s\n", t.ID, t.Title)
				}
			}
		},
	}
	worktreesCleanupCmd.Flags().Bool("dry-run", false, "Show what would be removed without making changes")
	worktreesCleanupCmd.Flags().String("max-age", "", "Maximum age before cleanup (e.g., 24h, 72h, 0 for all). Default: 24h (1 day)")
	worktreesCmd.AddCommand(worktreesCleanupCmd)
	rootCmd.AddCommand(worktreesCmd)

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

	// Doctor command - diagnose the agent server's GitHub auth health.
	doctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose agent server health (GitHub auth & rate limits)",
		Long: `Checks the local GitHub CLI authentication used by agents and warns about
conditions that cause shared GraphQL bucket exhaustion across agent servers:

  - gh not installed or not logged in (GitHub operations silently fail)
  - an expired/revoked token (401 Bad credentials)
  - authentication as a PERSONAL account, whose 5,000 pt/hr GraphQL limit is
    shared per-user across every server authed as that account
  - low remaining GraphQL headroom

Each agent server should authenticate with its OWN GitHub App installation
token (a bot identity), which gets an independent GraphQL bucket.

Exits non-zero on hard errors (gh missing, logged out, expired token). Pass
--strict to also exit non-zero on warnings (e.g. personal-account auth), so a
fleet sweep like 'for s in ...; do ssh $s ty doctor --strict; done' can flag
servers programmatically.`,
		Run: func(cmd *cobra.Command, args []string) {
			strict, _ := cmd.Flags().GetBool("strict")
			fmt.Println(boldStyle.Render("TaskYou Doctor"))
			fmt.Println(dimStyle.Render("Checking GitHub authentication..."))
			fmt.Println()

			status := github.CheckAuth(context.Background())
			if status.Err != nil {
				fmt.Println(warnStyle.Render("⚠ Could not fully probe gh: " + status.Err.Error()))
				fmt.Println()
			}

			findings := status.Findings()
			hasError := false
			for _, f := range findings {
				var icon, msg string
				switch f.Severity {
				case github.SeverityOK:
					icon = successStyle.Render("✓")
					msg = f.Message
				case github.SeverityWarn:
					icon = warnStyle.Render("⚠")
					msg = warnStyle.Render(f.Message)
				case github.SeverityError:
					icon = errorStyle.Render("✗")
					msg = errorStyle.Render(f.Message)
					hasError = true
				}
				fmt.Printf("%s %s\n", icon, msg)
				if f.Detail != "" {
					fmt.Println(dimStyle.Render("    " + f.Detail))
				}
			}

			fmt.Println()
			if hasError || status.HasProblems() {
				fmt.Println(dimStyle.Render("Tip: provision this server with its own GitHub App installation token,"))
				fmt.Println(dimStyle.Render("mirroring the offerlab-devs[bot] pattern, for an independent rate-limit bucket."))
			} else {
				fmt.Println(successStyle.Render("All checks passed."))
			}

			if hasError || (strict && status.HasProblems()) {
				os.Exit(1)
			}
		},
	}
	doctorCmd.Flags().Bool("strict", false, "Exit non-zero on warnings too (e.g. personal-account auth), for fleet health sweeps")
	rootCmd.AddCommand(doctorCmd)

	// Settings command
	settingsCmd := &cobra.Command{
		Use:   "settings",
		Short: "View and manage app settings",
		Run: func(cmd *cobra.Command, args []string) {
			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
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
		Use:               "set <key> <value>",
		Short:             "Set a setting value",
		ValidArgsFunction: completeSettingKeys,
		Long: `Set a configuration setting.

Available settings:
  anthropic_api_key     API key for ghost text autocomplete (uses Anthropic API
                        directly for speed). Get yours at console.anthropic.com
  autocomplete_enabled  Enable/disable ghost text autocomplete (true/false)
  idle_suspend_timeout  How long blocked tasks wait before suspending (e.g. 6h, 30m, 24h)
  http_api_port         Port the daemon-hosted HTTP API listens on (default 8080)
  http_api_disabled     Stop the daemon from hosting the HTTP API (true/false)`,
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
			case config.SettingHTTPAPIPort:
				if p, err := strconv.Atoi(value); err != nil || p < 1 || p > 65535 {
					fmt.Println(errorStyle.Render("Value must be a port number between 1 and 65535"))
					return
				}
			case config.SettingHTTPAPIDisabled:
				if value != "true" && value != "false" {
					fmt.Println(errorStyle.Render("Value must be 'true' or 'false'"))
					return
				}
			default:
				fmt.Println(errorStyle.Render("Unknown setting: " + key))
				fmt.Println(dimStyle.Render("Available: anthropic_api_key, autocomplete_enabled, idle_suspend_timeout, http_api_port, http_api_disabled"))
				return
			}

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
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

	// Events command - manage event hooks
	eventsCmd := &cobra.Command{
		Use:   "events",
		Short: "Manage event hooks",
		Long: `Manage task event hooks for automation.

Events are emitted when tasks change state (created, started, completed, failed).
Script hooks in ~/.config/task/hooks/ are executed automatically.

Examples:
  ty events list                      # Show recent events`,
	}

	// events list - show recent events from event log
	eventsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List recent events from the event log",
		Run: func(cmd *cobra.Command, args []string) {
			limit, _ := cmd.Flags().GetInt("limit")
			eventType, _ := cmd.Flags().GetString("type")
			taskID, _ := cmd.Flags().GetInt64("task")
			outputJSON, _ := cmd.Flags().GetBool("json")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			// Build query
			query := "SELECT id, event_type, task_id, message, metadata, created_at FROM event_log WHERE 1=1"
			var queryArgs []interface{}

			if eventType != "" {
				query += " AND event_type = ?"
				queryArgs = append(queryArgs, eventType)
			}
			if taskID > 0 {
				query += " AND task_id = ?"
				queryArgs = append(queryArgs, taskID)
			}

			query += " ORDER BY created_at DESC LIMIT ?"
			queryArgs = append(queryArgs, limit)

			rows, err := database.Query(query, queryArgs...)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer rows.Close()

			type EventRecord struct {
				ID        int64     `json:"id"`
				Type      string    `json:"event_type"`
				TaskID    int64     `json:"task_id"`
				Message   string    `json:"message"`
				Metadata  string    `json:"metadata"`
				CreatedAt time.Time `json:"created_at"`
			}

			var events []EventRecord
			for rows.Next() {
				var e EventRecord
				var createdAt db.LocalTime
				if err := rows.Scan(&e.ID, &e.Type, &e.TaskID, &e.Message, &e.Metadata, &createdAt); err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
					os.Exit(1)
				}
				e.CreatedAt = createdAt.Time
				events = append(events, e)
			}

			if outputJSON {
				data, _ := json.MarshalIndent(events, "", "  ")
				fmt.Println(string(data))
				return
			}

			if len(events) == 0 {
				fmt.Println(dimStyle.Render("No events found"))
				return
			}

			fmt.Println(boldStyle.Render(fmt.Sprintf("Recent Events (%d)", len(events))))
			fmt.Println(strings.Repeat("─", 80))
			for _, e := range events {
				timestamp := e.CreatedAt.Format("2006-01-02 15:04:05")
				fmt.Printf("%s  %s  Task #%d  %s\n",
					dimStyle.Render(timestamp),
					boldStyle.Render(e.Type),
					e.TaskID,
					e.Message)
				if e.Metadata != "{}" && e.Metadata != "" {
					fmt.Println(dimStyle.Render("  Metadata: " + e.Metadata))
				}
			}
		},
	}
	eventsListCmd.Flags().Int("limit", 50, "Maximum number of events to show")
	eventsListCmd.Flags().String("type", "", "Filter by event type (e.g., task.created)")
	eventsListCmd.Flags().Int64("task", 0, "Filter by task ID")
	eventsListCmd.Flags().Bool("json", false, "Output in JSON format")
	eventsCmd.AddCommand(eventsListCmd)

	rootCmd.AddCommand(eventsCmd)

	// Projects subcommand - manage projects
	projectsCmd := &cobra.Command{
		Use:   "projects",
		Short: "Manage projects",
		Long: `List, create, update, and delete projects.

Examples:
  ty projects                    # List all projects
  ty projects list               # List all projects
  ty projects show myapp         # Show project details
  ty projects create myapp       # Create new project
  ty projects update myapp       # Update project settings
  ty projects delete myapp       # Delete a project`,
		Run: func(cmd *cobra.Command, args []string) {
			// Default to list when no subcommand provided
			listProjectsCLI(cmd)
		},
	}
	projectsCmd.Flags().Bool("json", false, "Output in JSON format")

	// Projects list subcommand
	projectsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all projects",
		Run: func(cmd *cobra.Command, args []string) {
			listProjectsCLI(cmd)
		},
	}
	projectsListCmd.Flags().Bool("json", false, "Output in JSON format")
	projectsCmd.AddCommand(projectsListCmd)

	// Projects show subcommand
	projectsShowCmd := &cobra.Command{
		Use:               "show <name>",
		Short:             "Show project details",
		ValidArgsFunction: completeProjectNames,
		Long: `Show detailed information about a project including its instructions.

Examples:
  ty projects show myapp
  ty projects show myapp --json`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			outputJSON, _ := cmd.Flags().GetBool("json")
			showProjectCLI(args[0], outputJSON)
		},
	}
	projectsShowCmd.Flags().Bool("json", false, "Output in JSON format")
	projectsCmd.AddCommand(projectsShowCmd)

	// Projects create subcommand
	projectsCreateCmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new project",
		Long: `Create a new project with the specified name.

Examples:
  ty projects create myapp --path ~/Projects/myapp
  ty projects create myapp --path ~/Projects/myapp --instructions "Use TypeScript"
  ty projects create myapp --path ~/Projects/myapp --color "#61AFEF"`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			path, _ := cmd.Flags().GetString("path")
			instructions, _ := cmd.Flags().GetString("instructions")
			color, _ := cmd.Flags().GetString("color")
			aliases, _ := cmd.Flags().GetString("aliases")
			claudeConfigDir, _ := cmd.Flags().GetString("claude-config-dir")
			permissionMode, _ := cmd.Flags().GetString("permission-mode")
			noGit, _ := cmd.Flags().GetBool("no-git")
			outputJSON, _ := cmd.Flags().GetBool("json")

			createProjectCLI(args[0], path, instructions, color, aliases, claudeConfigDir, permissionMode, noGit, outputJSON)
		},
	}
	projectsCreateCmd.Flags().StringP("path", "p", "", "Project directory path (required)")
	projectsCreateCmd.Flags().StringP("instructions", "i", "", "Project-specific AI instructions")
	projectsCreateCmd.Flags().StringP("color", "c", "", "Hex color for display (e.g., #61AFEF)")
	projectsCreateCmd.Flags().StringP("aliases", "a", "", "Comma-separated aliases for lookup")
	projectsCreateCmd.Flags().String("claude-config-dir", "", "Override CLAUDE_CONFIG_DIR for this project")
	projectsCreateCmd.Flags().String("permission-mode", "", "Default permission mode for tasks: default (prompt), accept-edits (auto-accept file edits), auto (Claude Code auto mode), dangerous (skip all)")
	projectsCreateCmd.Flags().Bool("no-git", false, "Disable git worktrees (for non-git projects)")
	projectsCreateCmd.Flags().Bool("json", false, "Output in JSON format")
	projectsCreateCmd.MarkFlagRequired("path")
	projectsCmd.AddCommand(projectsCreateCmd)

	// Projects update subcommand
	projectsUpdateCmd := &cobra.Command{
		Use:               "update <name>",
		Short:             "Update project settings",
		ValidArgsFunction: completeProjectNames,
		Long: `Update settings for an existing project.

Examples:
  ty projects update myapp --instructions "Use TypeScript and React"
  ty projects update myapp --color "#10B981"
  ty projects update myapp --name newname
  ty projects update myapp --path ~/Projects/newpath
  ty projects update myapp --context "Project context summary..."`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name, _ := cmd.Flags().GetString("name")
			path, _ := cmd.Flags().GetString("path")
			instructions, _ := cmd.Flags().GetString("instructions")
			color, _ := cmd.Flags().GetString("color")
			aliases, _ := cmd.Flags().GetString("aliases")
			claudeConfigDir, _ := cmd.Flags().GetString("claude-config-dir")
			projectContext, _ := cmd.Flags().GetString("context")
			permissionMode, _ := cmd.Flags().GetString("permission-mode")
			outputJSON, _ := cmd.Flags().GetBool("json")
			noGit, _ := cmd.Flags().GetBool("no-git")
			git, _ := cmd.Flags().GetBool("git")

			if noGit && git {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: --no-git and --git are mutually exclusive"))
				os.Exit(1)
			}

			// Determine git mode: nil means not specified
			var useWorktrees *bool
			if noGit {
				v := false
				useWorktrees = &v
			} else if git {
				v := true
				useWorktrees = &v
			}

			updateProjectCLI(args[0], name, path, instructions, color, aliases, claudeConfigDir, projectContext, permissionMode, useWorktrees, outputJSON)
		},
	}
	projectsUpdateCmd.Flags().StringP("name", "n", "", "New project name")
	projectsUpdateCmd.Flags().StringP("path", "p", "", "Project directory path")
	projectsUpdateCmd.Flags().StringP("instructions", "i", "", "Project-specific AI instructions")
	projectsUpdateCmd.Flags().StringP("color", "c", "", "Hex color for display (e.g., #61AFEF)")
	projectsUpdateCmd.Flags().StringP("aliases", "a", "", "Comma-separated aliases for lookup")
	projectsUpdateCmd.Flags().String("claude-config-dir", "", "Override CLAUDE_CONFIG_DIR for this project")
	projectsUpdateCmd.Flags().String("context", "", "Cached project context summary")
	projectsUpdateCmd.Flags().String("permission-mode", "", "Default permission mode for tasks: default (prompt), accept-edits (auto-accept file edits), auto (Claude Code auto mode), dangerous (skip all)")
	projectsUpdateCmd.Flags().Bool("no-git", false, "Disable git worktrees (for non-git projects)")
	projectsUpdateCmd.Flags().Bool("git", false, "Enable git worktrees (default)")
	projectsUpdateCmd.Flags().Bool("json", false, "Output in JSON format")
	projectsCmd.AddCommand(projectsUpdateCmd)

	// Projects delete subcommand
	projectsDeleteCmd := &cobra.Command{
		Use:               "delete <name>",
		Short:             "Delete a project",
		ValidArgsFunction: completeProjectNames,
		Long: `Delete a project. The 'personal' project cannot be deleted.

Note: This only removes the project from the task system. It does not
delete any files or directories on disk.

Examples:
  ty projects delete myapp
  ty projects delete myapp --force  # Skip confirmation`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			force, _ := cmd.Flags().GetBool("force")
			deleteProjectCLI(args[0], force)
		},
	}
	projectsDeleteCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	projectsCmd.AddCommand(projectsDeleteCmd)

	rootCmd.AddCommand(projectsCmd)

	// Block command - create a dependency between two tasks
	blockCmd := &cobra.Command{
		Use:               "block <blocked-task-id> --by <blocker-task-id>",
		ValidArgsFunction: completeTaskIDs,
		Short:             "Block a task until another task completes",
		Long: `Create a dependency where a task is blocked until another task completes.

Example: ty block 5 --by 3
This makes task #5 blocked by task #3. Task #5 cannot proceed until #3 is done.

Use --auto-queue to automatically move the blocked task to 'queued' when unblocked.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var blockedID int64
			if _, err := fmt.Sscanf(args[0], "%d", &blockedID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			blockerID, _ := cmd.Flags().GetInt64("by")
			if blockerID == 0 {
				fmt.Fprintln(os.Stderr, errorStyle.Render("--by flag is required"))
				os.Exit(1)
			}

			autoQueue, _ := cmd.Flags().GetBool("auto-queue")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			// Verify both tasks exist
			blocker, err := database.GetTask(blockerID)
			if err != nil || blocker == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Blocker task #%d not found", blockerID)))
				os.Exit(1)
			}

			blocked, err := database.GetTask(blockedID)
			if err != nil || blocked == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Blocked task #%d not found", blockedID)))
				os.Exit(1)
			}

			if err := database.AddDependency(blockerID, blockedID, autoQueue); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			autoQueueStr := ""
			if autoQueue {
				autoQueueStr = " (will auto-queue when unblocked)"
			}
			fmt.Println(successStyle.Render(fmt.Sprintf("Task #%d is now blocked by #%d%s", blockedID, blockerID, autoQueueStr)))
		},
	}
	blockCmd.Flags().Int64("by", 0, "ID of the blocker task (required)")
	blockCmd.Flags().Bool("auto-queue", false, "Auto-queue blocked task when unblocked")
	blockCmd.MarkFlagRequired("by")
	rootCmd.AddCommand(blockCmd)

	// Unblock command - remove a dependency
	unblockCmd := &cobra.Command{
		Use:               "unblock <blocked-task-id> --from <blocker-task-id>",
		ValidArgsFunction: completeTaskIDs,
		Short:             "Remove a blocking dependency",
		Long: `Remove a dependency so a task is no longer blocked by another.

Example: ty unblock 5 --from 3
This removes the dependency where task #5 was blocked by task #3.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var blockedID int64
			if _, err := fmt.Sscanf(args[0], "%d", &blockedID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			blockerID, _ := cmd.Flags().GetInt64("from")
			if blockerID == 0 {
				fmt.Fprintln(os.Stderr, errorStyle.Render("--from flag is required"))
				os.Exit(1)
			}

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			if err := database.RemoveDependency(blockerID, blockedID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render(fmt.Sprintf("Task #%d is no longer blocked by #%d", blockedID, blockerID)))
		},
	}
	unblockCmd.Flags().Int64("from", 0, "ID of the blocker task to remove (required)")
	unblockCmd.MarkFlagRequired("from")
	rootCmd.AddCommand(unblockCmd)

	// Deps command - show dependencies for a task
	depsCmd := &cobra.Command{
		Use:               "deps <task-id>",
		ValidArgsFunction: completeTaskIDs,
		Short:             "Show dependencies for a task",
		Long: `Display all dependencies for a task, showing:
- Tasks that block this task (must complete before this task)
- Tasks that this task blocks (waiting on this task)`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var taskID int64
			if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Invalid task ID: "+args[0]))
				os.Exit(1)
			}

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			task, err := database.GetTask(taskID)
			if err != nil || task == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Task #%d not found", taskID)))
				os.Exit(1)
			}

			blockers, blockedBy, err := database.GetAllDependencies(taskID)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			fmt.Printf("%s #%d: %s\n\n", boldStyle.Render("Task"), taskID, task.Title)

			if len(blockers) == 0 && len(blockedBy) == 0 {
				fmt.Println(dimStyle.Render("No dependencies"))
				return
			}

			if len(blockers) > 0 {
				fmt.Println(boldStyle.Render("Blocked by:"))
				for _, b := range blockers {
					statusIcon := ""
					if b.Status == db.StatusDone || b.Status == db.StatusArchived {
						statusIcon = successStyle.Render(" [done]")
					} else {
						statusIcon = dimStyle.Render(fmt.Sprintf(" [%s]", b.Status))
					}
					fmt.Printf("  #%d: %s%s\n", b.ID, b.Title, statusIcon)
				}
				fmt.Println()
			}

			if len(blockedBy) > 0 {
				fmt.Println(boldStyle.Render("Blocks:"))
				for _, b := range blockedBy {
					fmt.Printf("  #%d: %s %s\n", b.ID, b.Title, dimStyle.Render(fmt.Sprintf("[%s]", b.Status)))
				}
			}
		},
	}
	rootCmd.AddCommand(depsCmd)

	// Types command - manage task types
	typesCmd := &cobra.Command{
		Use:   "types",
		Short: "Manage task types",
		Long: `Manage task types used for organizing and customizing task behavior.

Task types define prompt templates and instructions for different kinds of tasks.
Built-in types (code, writing, thinking) can be edited but not deleted.

Examples:
  ty types                    # List all task types
  ty types show code          # Show details of the 'code' type
  ty types create             # Create a new task type
  ty types edit research      # Edit the 'research' type
  ty types delete research    # Delete a custom type`,
		Run: func(cmd *cobra.Command, args []string) {
			outputJSON, _ := cmd.Flags().GetBool("json")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			taskTypes, err := database.ListTaskTypes()
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}

			if outputJSON {
				type typeOutput struct {
					ID           int64  `json:"id"`
					Name         string `json:"name"`
					Label        string `json:"label"`
					Instructions string `json:"instructions"`
					SortOrder    int    `json:"sort_order"`
					IsBuiltin    bool   `json:"is_builtin"`
				}
				output := make([]typeOutput, 0, len(taskTypes))
				for _, t := range taskTypes {
					output = append(output, typeOutput{
						ID:           t.ID,
						Name:         t.Name,
						Label:        t.Label,
						Instructions: t.Instructions,
						SortOrder:    t.SortOrder,
						IsBuiltin:    t.IsBuiltin,
					})
				}
				data, _ := json.MarshalIndent(output, "", "  ")
				fmt.Println(string(data))
				return
			}

			fmt.Println(boldStyle.Render("Task Types"))
			fmt.Println()

			if len(taskTypes) == 0 {
				fmt.Println(dimStyle.Render("No task types configured"))
				return
			}

			for _, t := range taskTypes {
				builtinTag := ""
				if t.IsBuiltin {
					builtinTag = dimStyle.Render(" [builtin]")
				}
				fmt.Printf("  %s%s\n", boldStyle.Render(t.Name), builtinTag)
				fmt.Printf("    Label: %s\n", t.Label)
				// Show truncated instructions
				instr := t.Instructions
				if len(instr) > 80 {
					instr = instr[:77] + "..."
				}
				instr = strings.ReplaceAll(instr, "\n", " ")
				fmt.Printf("    Instructions: %s\n", dimStyle.Render(instr))
				fmt.Println()
			}

			fmt.Println(dimStyle.Render("Use 'ty types show <name>' to see full instructions"))
		},
	}
	typesCmd.Flags().Bool("json", false, "Output in JSON format")

	// Types list subcommand (alias for default behavior)
	typesListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all task types",
		Run:   typesCmd.Run,
	}
	typesListCmd.Flags().Bool("json", false, "Output in JSON format")
	typesCmd.AddCommand(typesListCmd)

	// Types show subcommand
	typesShowCmd := &cobra.Command{
		Use:               "show <name>",
		ValidArgsFunction: completeTypeNames,
		Short:             "Show details of a task type",
		Args:              cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			outputJSON, _ := cmd.Flags().GetBool("json")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			taskType, err := database.GetTaskTypeByName(name)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if taskType == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Task type not found: "+name))
				os.Exit(1)
			}

			if outputJSON {
				type typeOutput struct {
					ID           int64  `json:"id"`
					Name         string `json:"name"`
					Label        string `json:"label"`
					Instructions string `json:"instructions"`
					SortOrder    int    `json:"sort_order"`
					IsBuiltin    bool   `json:"is_builtin"`
				}
				output := typeOutput{
					ID:           taskType.ID,
					Name:         taskType.Name,
					Label:        taskType.Label,
					Instructions: taskType.Instructions,
					SortOrder:    taskType.SortOrder,
					IsBuiltin:    taskType.IsBuiltin,
				}
				data, _ := json.MarshalIndent(output, "", "  ")
				fmt.Println(string(data))
				return
			}

			builtinTag := ""
			if taskType.IsBuiltin {
				builtinTag = dimStyle.Render(" [builtin]")
			}
			fmt.Printf("%s%s\n", boldStyle.Render(taskType.Name), builtinTag)
			fmt.Printf("Label: %s\n", taskType.Label)
			fmt.Printf("Sort Order: %d\n", taskType.SortOrder)
			fmt.Println()
			fmt.Println(boldStyle.Render("Instructions:"))
			fmt.Println(taskType.Instructions)
		},
	}
	typesShowCmd.Flags().Bool("json", false, "Output in JSON format")
	typesCmd.AddCommand(typesShowCmd)

	// Types create subcommand
	typesCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new task type",
		Long: `Create a new task type with custom instructions.

The instructions field supports template variables:
  {{project}}              - Project name
  {{title}}                - Task title
  {{body}}                 - Task body/description
  {{branch}}               - Git branch name
  {{tags}}                 - Task tags
  {{pr_url}}               - Pull request URL (if set)
  {{pr_number}}            - Pull request number (if set)
  {{task_id}}              - Task ID
  {{task_metadata}}        - Full task metadata section
  {{project_instructions}} - Project-specific instructions
  {{attachments}}          - File attachments content
  {{history}}              - Conversation history

Examples:
  ty types create --name research --label "Research" --instructions "Research the topic: {{title}}"
  ty types create --name review --label "Code Review" --instructions-file review.txt`,
		Run: func(cmd *cobra.Command, args []string) {
			name, _ := cmd.Flags().GetString("name")
			label, _ := cmd.Flags().GetString("label")
			instructions, _ := cmd.Flags().GetString("instructions")
			instructionsFile, _ := cmd.Flags().GetString("instructions-file")
			sortOrder, _ := cmd.Flags().GetInt("sort-order")

			// Validate name is provided
			if name == "" {
				fmt.Fprintln(os.Stderr, errorStyle.Render("--name is required"))
				os.Exit(1)
			}

			// Validate name format (lowercase, no spaces)
			if strings.ToLower(name) != name || strings.Contains(name, " ") {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Name must be lowercase with no spaces"))
				os.Exit(1)
			}

			// Use name as label if not provided
			if label == "" {
				// Capitalize first letter for label
				label = strings.ToUpper(name[:1]) + name[1:]
			}

			// Read instructions from file if provided
			if instructionsFile != "" {
				data, err := os.ReadFile(instructionsFile)
				if err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error reading instructions file: "+err.Error()))
					os.Exit(1)
				}
				instructions = string(data)
			}

			if instructions == "" {
				fmt.Fprintln(os.Stderr, errorStyle.Render("--instructions or --instructions-file is required"))
				os.Exit(1)
			}

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			// Check if name already exists
			existing, _ := database.GetTaskTypeByName(name)
			if existing != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Task type already exists: "+name))
				os.Exit(1)
			}

			taskType := &db.TaskType{
				Name:         name,
				Label:        label,
				Instructions: instructions,
				SortOrder:    sortOrder,
				IsBuiltin:    false,
			}

			if err := database.CreateTaskType(taskType); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error creating task type: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render("Created task type: " + name))
		},
	}
	typesCreateCmd.Flags().String("name", "", "Type name (lowercase, no spaces) - required")
	typesCreateCmd.Flags().String("label", "", "Display label (defaults to capitalized name)")
	typesCreateCmd.Flags().String("instructions", "", "Prompt instructions template")
	typesCreateCmd.Flags().String("instructions-file", "", "Read instructions from file")
	typesCreateCmd.Flags().Int("sort-order", 100, "Sort order for display (lower = first)")
	typesCmd.AddCommand(typesCreateCmd)

	// Types edit subcommand
	typesEditCmd := &cobra.Command{
		Use:               "edit <name>",
		ValidArgsFunction: completeTypeNames,
		Short:             "Edit an existing task type",
		Long: `Edit an existing task type. Built-in types can be edited but not deleted.

All flags are optional - only specified values will be updated.

Examples:
  ty types edit code --label "Development"
  ty types edit research --instructions "New instructions here"
  ty types edit review --instructions-file updated_review.txt`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			newName, _ := cmd.Flags().GetString("name")
			label, _ := cmd.Flags().GetString("label")
			instructions, _ := cmd.Flags().GetString("instructions")
			instructionsFile, _ := cmd.Flags().GetString("instructions-file")
			sortOrder, _ := cmd.Flags().GetInt("sort-order")
			sortOrderSet := cmd.Flags().Changed("sort-order")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			taskType, err := database.GetTaskTypeByName(name)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if taskType == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Task type not found: "+name))
				os.Exit(1)
			}

			// Read instructions from file if provided
			if instructionsFile != "" {
				data, err := os.ReadFile(instructionsFile)
				if err != nil {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Error reading instructions file: "+err.Error()))
					os.Exit(1)
				}
				instructions = string(data)
			}

			// Update fields if provided
			updated := false
			if newName != "" {
				if strings.ToLower(newName) != newName || strings.Contains(newName, " ") {
					fmt.Fprintln(os.Stderr, errorStyle.Render("Name must be lowercase with no spaces"))
					os.Exit(1)
				}
				// Check if new name conflicts with existing type
				if newName != name {
					existing, _ := database.GetTaskTypeByName(newName)
					if existing != nil {
						fmt.Fprintln(os.Stderr, errorStyle.Render("Task type already exists: "+newName))
						os.Exit(1)
					}
				}
				taskType.Name = newName
				updated = true
			}
			if label != "" {
				taskType.Label = label
				updated = true
			}
			if instructions != "" {
				taskType.Instructions = instructions
				updated = true
			}
			if sortOrderSet {
				taskType.SortOrder = sortOrder
				updated = true
			}

			if !updated {
				fmt.Fprintln(os.Stderr, errorStyle.Render("No updates specified. Use --name, --label, --instructions, --instructions-file, or --sort-order"))
				os.Exit(1)
			}

			if err := database.UpdateTaskType(taskType); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error updating task type: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render("Updated task type: " + taskType.Name))
		},
	}
	typesEditCmd.Flags().String("name", "", "New type name (lowercase, no spaces)")
	typesEditCmd.Flags().String("label", "", "New display label")
	typesEditCmd.Flags().String("instructions", "", "New prompt instructions template")
	typesEditCmd.Flags().String("instructions-file", "", "Read new instructions from file")
	typesEditCmd.Flags().Int("sort-order", 0, "New sort order for display")
	typesCmd.AddCommand(typesEditCmd)

	// Types delete subcommand
	typesDeleteCmd := &cobra.Command{
		Use:               "delete <name>",
		ValidArgsFunction: completeTypeNames,
		Short:             "Delete a custom task type",
		Long: `Delete a custom task type. Built-in types (code, writing, thinking) cannot be deleted.

Examples:
  ty types delete research
  ty types delete review --force`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]
			force, _ := cmd.Flags().GetBool("force")

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			taskType, err := database.GetTaskTypeByName(name)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
				os.Exit(1)
			}
			if taskType == nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Task type not found: "+name))
				os.Exit(1)
			}

			if taskType.IsBuiltin {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Cannot delete built-in task type: "+name))
				os.Exit(1)
			}

			// Confirmation prompt unless --force
			if !force {
				fmt.Printf("Delete task type '%s'? This cannot be undone. [y/N] ", name)
				reader := bufio.NewReader(os.Stdin)
				response, _ := reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response != "y" && response != "yes" {
					fmt.Println("Cancelled")
					return
				}
			}

			if err := database.DeleteTaskType(taskType.ID); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error deleting task type: "+err.Error()))
				os.Exit(1)
			}

			fmt.Println(successStyle.Render("Deleted task type: " + name))
		},
	}
	typesDeleteCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	typesCmd.AddCommand(typesDeleteCmd)

	rootCmd.AddCommand(typesCmd)

	// Serve subcommand - HTTP API server
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start an HTTP API server",
		Long: `Start a lightweight HTTP API server exposing the same operations as the CLI.
External frontends (like ty-web) can build on top of this API.
The server shares the same SQLite database the daemon writes to (WAL mode).`,
		Run: func(cmd *cobra.Command, args []string) {
			port, _ := cmd.Flags().GetInt("port")
			addr := fmt.Sprintf(":%d", port)

			dbPath := db.DefaultPath()
			database, err := openTaskDB(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Error opening database: "+err.Error()))
				os.Exit(1)
			}
			defer database.Close()

			runner := &execCommandRunner{}
			// Give the API access to executor metadata and interactive session
			// bootstrap (used by GUI clients to start/attach executor terminals).
			exec := executor.New(database, config.New(database))
			srv := web.New(web.Config{
				Addr:      addr,
				DB:        database,
				CmdRunner: runner,
				Sessions:  exec,
			})

			// Handle signals for graceful shutdown
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

			go func() {
				<-sigCh
				fmt.Println("\nShutting down web server...")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				srv.Shutdown(ctx)
			}()

			if err := srv.Start(); err != nil {
				fmt.Fprintln(os.Stderr, errorStyle.Render("Server error: "+err.Error()))
				os.Exit(1)
			}
		},
	}
	serveCmd.Flags().Int("port", 8080, "Port to listen on")
	rootCmd.AddCommand(serveCmd)

	// Bulk operations
	rootCmd.AddCommand(newBulkCmd())

	// Completion command for shell tab completion
	rootCmd.AddCommand(newCompletionCmd(rootCmd))

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
}

// tailModel is a Bubble Tea model for the live tail view.
// Uses alternate screen buffer to avoid scrollback pollution.
type tailModel struct {
	db       *db.DB
	interval time.Duration
	showDone bool
	width    int
	height   int
}

type tailTickMsg time.Time

func (m tailModel) Init() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg {
		return tailTickMsg(t)
	})
}

func (m tailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tailTickMsg:
		return m, tea.Tick(m.interval, func(t time.Time) tea.Msg {
			return tailTickMsg(t)
		})
	}
	return m, nil
}

func (m tailModel) View() string {
	var b strings.Builder

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

	projectStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#A78BFA"))
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E5E7EB"))
	logStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")).Italic(true)

	statusOrder := []string{
		db.StatusProcessing,
		db.StatusBlocked,
		db.StatusQueued,
		db.StatusBacklog,
	}
	if m.showDone {
		statusOrder = append(statusOrder, db.StatusDone)
	}

	statusLabels := map[string]string{
		db.StatusProcessing: "In Progress",
		db.StatusBlocked:    "Blocked",
		db.StatusQueued:     "Queued",
		db.StatusBacklog:    "Backlog",
		db.StatusDone:       "Done",
	}

	opts := db.ListTasksOptions{
		IncludeClosed: m.showDone,
		Limit:         500,
	}
	tasks, err := m.db.ListTasks(opts)
	if err != nil {
		return errorStyle.Render("Error: " + err.Error())
	}

	var taskIDs []int64
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}
	latestLogs, _ := m.db.GetLatestLogPerTask(taskIDs)
	if latestLogs == nil {
		latestLogs = make(map[int64]*db.TaskLog)
	}

	type projectGroup struct {
		name     string
		statuses map[string][]*db.Task
	}
	projectMap := make(map[string]*projectGroup)
	var projectOrder []string

	for _, t := range tasks {
		if t.Status == db.StatusArchived {
			continue
		}
		proj := t.Project
		if proj == "" {
			proj = "(no project)"
		}
		pg, exists := projectMap[proj]
		if !exists {
			pg = &projectGroup{name: proj, statuses: make(map[string][]*db.Task)}
			projectMap[proj] = pg
			projectOrder = append(projectOrder, proj)
		}
		pg.statuses[t.Status] = append(pg.statuses[t.Status], t)
	}

	sort.Strings(projectOrder)

	// Header
	separatorWidth := 60
	if m.width > 0 && m.width < separatorWidth {
		separatorWidth = m.width
	}
	b.WriteString(headerStyle.Render("TaskYou — Live Tail"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render(time.Now().Format("15:04:05")))
	b.WriteByte('\n')
	b.WriteString(dimStyle.Render(strings.Repeat("─", separatorWidth)))
	b.WriteByte('\n')

	if len(tasks) == 0 {
		b.WriteString(dimStyle.Render("  No tasks found"))
		b.WriteByte('\n')
	}

	maxTitleWidth := 70
	if m.width > 20 {
		maxTitleWidth = m.width - 20
	}

	for i, proj := range projectOrder {
		pg := projectMap[proj]
		total := 0
		for _, tl := range pg.statuses {
			total += len(tl)
		}
		b.WriteByte('\n')
		fmt.Fprintf(&b, "%s %s\n", projectStyle.Render(proj), dimStyle.Render(fmt.Sprintf("(%d)", total)))

		for _, status := range statusOrder {
			statusTasks := pg.statuses[status]
			if len(statusTasks) == 0 {
				continue
			}
			label := statusLabels[status]
			fmt.Fprintf(&b, "  %s\n", statusStyle(status).Render(fmt.Sprintf("▸ %s (%d)", label, len(statusTasks))))
			for _, t := range statusTasks {
				title := truncate(t.Title, maxTitleWidth)
				age := boardAgeHint(t)
				line := fmt.Sprintf("    #%-4d %s", t.ID, title)
				if age != "" {
					line += "  " + dimStyle.Render(age)
				}
				b.WriteString(line)
				b.WriteByte('\n')

				if log, ok := latestLogs[t.ID]; ok && log.Content != "" {
					content := strings.TrimSpace(log.Content)
					if idx := strings.IndexByte(content, '\n'); idx != -1 {
						content = content[:idx]
					}
					content = truncate(content, 80)
					fmt.Fprintf(&b, "           %s\n", logStyle.Render(content))
				}
			}
		}

		if i < len(projectOrder)-1 {
			b.WriteByte('\n')
		}
	}

	b.WriteByte('\n')
	b.WriteString(dimStyle.Render("q/esc to quit • refreshing every " + m.interval.String()))

	result := b.String()
	if m.height > 0 {
		lines := strings.Split(result, "\n")
		if len(lines) > m.height {
			lines = lines[:m.height-1]
			lines = append(lines, dimStyle.Render("… (resize terminal to see all tasks)"))
			result = strings.Join(lines, "\n")
		}
	}

	return result
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
		// Reset window styling that may have been left over from a previous detail view
		// (joinTmuxPanes sets window-style to dim inactive panes, but if the session
		// wasn't cleanly shut down, the dimming persists on re-attach)
		osexec.Command("tmux", "set-option", "-t", sessionName, "window-style", "default").Run()
		osexec.Command("tmux", "set-option", "-t", sessionName, "window-active-style", "default").Run()

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

	// Override global tmux window-style to prevent dimming from other tools (e.g. dmux)
	osexec.Command("tmux", "set-option", "-t", sessionName, "window-style", "default").Run()
	osexec.Command("tmux", "set-option", "-t", sessionName, "window-active-style", "default").Run()

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

// setupProfiling starts CPU and/or heap profiling when paths are provided and
// returns a stop function that flushes the profiles. The returned func is
// idempotent (only the first call has an effect), so it can be both called
// explicitly after the TUI exits — important because the tmux teardown can
// SIGKILL this process and skip deferred flushes — and deferred as a safety net
// for early-return error paths. Profiling failures only warn; they never abort
// the TUI.
func setupProfiling(cpuPath, memPath string) func() {
	var cpuFile *os.File
	if cpuPath != "" {
		f, err := os.Create(cpuPath)
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, dimStyle.Render("Warning: could not create cpu profile: "+err.Error()))
		default:
			if err := pprof.StartCPUProfile(f); err != nil {
				fmt.Fprintln(os.Stderr, dimStyle.Render("Warning: could not start cpu profile: "+err.Error()))
				_ = f.Close()
			} else {
				cpuFile = f
				fmt.Fprintln(os.Stderr, dimStyle.Render("CPU profiling to: "+cpuPath))
			}
		}
	}

	stopped := false
	return func() {
		if stopped {
			return
		}
		stopped = true

		if cpuFile != nil {
			pprof.StopCPUProfile()
			_ = cpuFile.Close()
		}
		if memPath != "" {
			f, err := os.Create(memPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, dimStyle.Render("Warning: could not create mem profile: "+err.Error()))
				return
			}
			defer f.Close()
			runtime.GC() // get up-to-date allocation statistics
			_ = pprof.WriteHeapProfile(f)
		}
	}
}

// runLocal runs the TUI locally with a local SQLite database.
func runLocal(dangerousMode bool, debugStatePath, cpuProfilePath, memProfilePath string) error {
	// Optional performance profiling. The CPU profile captures the whole
	// interactive session (including every render); the heap profile is written
	// on exit. Analyze with `go tool pprof <binary> <profile>`.
	stopProfiling := setupProfiling(cpuProfilePath, memProfilePath)
	defer stopProfiling() // safety net for early-return error paths below

	// Multiple interactive TUIs may now run at once. The old process-wide lock is
	// gone; contention is handled at a finer grain instead — a TUI only refuses to
	// borrow an *individual* executor pane when another instance already holds it
	// (see acquireExecutorLock in internal/ui). Two boards can coexist; only the
	// same live executor can't be joined twice.

	// Ensure daemon is running
	if err := ensureDaemonRunning(dangerousMode); err != nil {
		fmt.Fprintln(os.Stderr, dimStyle.Render("Warning: could not start daemon: "+err.Error()))
	}

	// Open database
	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
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
	model := ui.NewAppModel(database, exec, cwd, version)
	if debugStatePath != "" {
		model.SetDebugStatePath(debugStatePath)
	}
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}

	// Flush profiles now, before the tmux cleanup below may kill our own session
	// (which would SIGKILL this process and skip the deferred flush).
	stopProfiling()

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
// If the daemon is already running (even with a different mode), it is left as-is.
func ensureDaemonRunning(dangerousMode bool) error {
	pidFile := getPidFilePath()
	modeFile := pidFile + ".mode"

	// Check if daemon is already running
	if pid, err := readPidFile(pidFile); err == nil {
		if processExists(pid) {
			return nil // Already running, leave it alone regardless of mode
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

	// Write mode file so we know what mode the daemon is running in
	modeFile := pidFile + ".mode"
	modeStr := "safe"
	if os.Getenv("WORKTREE_DANGEROUS_MODE") == "1" {
		modeStr = "dangerous"
	}
	os.WriteFile(modeFile, []byte(modeStr), 0644)
	defer os.Remove(modeFile)

	// Setup logger
	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		Prefix:          "task-daemon",
	})

	// Open database
	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
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

	// Host the HTTP API in-process so external clients (ty-web, the ty-chrome
	// extension) can reach it whenever the daemon is up — no separate `ty serve`
	// needed. Failures here (e.g. port already bound) are logged but never bring
	// down the executor; the daemon's job is running tasks first and foremost.
	httpSrv := startDaemonHTTPAPI(database, exec, logger)

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Wait for signal
	sig := <-sigCh
	logger.Info("Received signal, shutting down", "signal", sig)
	if httpSrv != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		httpSrv.Shutdown(shutdownCtx)
		shutdownCancel()
	}
	exec.Stop()

	return nil
}

// startDaemonHTTPAPI launches the HTTP API server in a background goroutine,
// reusing the daemon's already-running executor for session bootstrap. It
// returns the server so the caller can shut it down gracefully, or nil if the
// API is disabled. A bind failure is logged and tolerated — it must never stop
// the daemon from executing tasks.
func startDaemonHTTPAPI(database *db.DB, exec *executor.Executor, logger *log.Logger) *web.Server {
	if disabled, _ := database.GetSetting(config.SettingHTTPAPIDisabled); disabled == "true" {
		logger.Info("HTTP API disabled via setting", "key", config.SettingHTTPAPIDisabled)
		return nil
	}

	port := config.DefaultHTTPAPIPort
	if v, _ := database.GetSetting(config.SettingHTTPAPIPort); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			port = p
		} else {
			logger.Warn("Invalid HTTP API port setting, using default", "value", v, "default", port)
		}
	}

	srv := web.New(web.Config{
		Addr:      fmt.Sprintf(":%d", port),
		DB:        database,
		CmdRunner: &execCommandRunner{},
		Sessions:  exec,
	})

	go func() {
		// Belt-and-suspenders: a panic outside an HTTP handler (net/http already
		// recovers per-request panics) must not take down the executor.
		defer func() {
			if r := recover(); r != nil {
				logger.Error("HTTP API goroutine panicked; executor continues", "panic", r)
			}
		}()
		if err := srv.Start(); err != nil {
			// Most commonly "address already in use" (e.g. a stale `ty serve`).
			// The executor keeps running regardless.
			logger.Warn("HTTP API not started", "error", err)
		}
	}()

	logger.Info("HTTP API listening", "port", port)
	return srv
}

// ClaudeHookInput is the JSON structure Claude sends to hooks via stdin.
type ClaudeHookInput struct {
	SessionID        string `json:"session_id"`
	TranscriptPath   string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	PermissionMode   string `json:"permission_mode,omitempty"` // e.g. "default", "acceptEdits", "bypassPermissions"
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type,omitempty"` // For Notification hooks
	Message          string `json:"message,omitempty"`           // General message field
	StopReason       string `json:"stop_reason,omitempty"`       // For Stop hooks
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
	database, err := openTaskDB(dbPath)
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
				if input.Message != "" {
					msg = "Waiting for permission: " + input.Message
				}
				// The Notification hook doesn't include tool_name/tool_input, but the
				// PreToolUse hook (which fires just before) stashes the detail as a
				// "pending_tool" log entry. Retrieve and append to the permission message.
				if detail := latestPendingToolDetail(database, taskID); detail != "" {
					msg += "\n" + detail
				}
			}
			database.AppendTaskLog(taskID, "system", msg)
		}
	}
	return nil
}

// runMCPServer runs the workflow MCP server for a specific task.
// This is invoked by Claude Code via the .mcp.json configuration.
func runMCPServer(taskID int64) error {
	// Open database
	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Create and run MCP server
	server := mcp.NewServer(database, taskID)
	return server.Run()
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

	// When the executor is about to use a tool, the task should be "processing"
	// This handles the case where:
	// 1. Task was blocked (waiting for input) and user responded
	// 2. Task was in any other state but executor is now actively working
	if task.Status == db.StatusBlocked {
		database.UpdateTaskStatus(taskID, db.StatusProcessing)
		database.AppendTaskLog(taskID, "system", "Agent resumed working")
	}

	// Store tool detail so the Notification(permission_prompt) handler can show it.
	// The Notification hook doesn't include tool_name/tool_input, but PreToolUse
	// fires right before it, so we stash the detail here for retrieval.
	if detail := formatPermissionDetail(input); detail != "" {
		database.AppendTaskLog(taskID, "pending_tool", detail)
	}

	// Worktree write-guard: block/ask on writes that would escape the isolated
	// worktree. Inert for non-worktree ("shared dir") tasks. Emitting the decision
	// to stdout is what gives it teeth; an "ask" surfaces as a permission prompt,
	// which the Notification(permission_prompt) hook turns into a blocked task on
	// the board, so unattended runs route to needs-input rather than silently
	// writing to the wrong tree.
	emitWorktreeGuardDecision(database, taskID, task, input)

	return nil
}

// emitWorktreeGuardDecision evaluates the worktree write-guard and, when a tool
// call would write outside the worktree, prints a PreToolUse permission decision
// (ask/deny) to stdout per Claude Code's hookSpecificOutput contract. It prints
// nothing when the write is allowed, leaving Claude's normal permission flow intact.
func emitWorktreeGuardDecision(database *db.DB, taskID int64, task *db.Task, input *ClaudeHookInput) {
	if task == nil {
		return
	}
	var allow []string
	if projectDir := executor.ManagedWorktreeProjectDir(task.WorktreePath); projectDir != "" {
		allow = executor.WorktreeAllowExternalWrites(projectDir)
	}
	decision := executor.EvaluateWorktreeWriteGuard(task.WorktreePath, allow, executor.WorktreeGuardInput{
		ToolName:       input.ToolName,
		ToolInput:      input.ToolInput,
		Cwd:            input.Cwd,
		PermissionMode: input.PermissionMode,
	})
	if decision == nil {
		return
	}

	if decision.Decision == "deny" {
		database.AppendTaskLog(taskID, "system", "Worktree guard denied an out-of-worktree write")
	}

	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       decision.Decision,
			"permissionDecisionReason": decision.Reason,
		},
	}
	if data, err := json.Marshal(out); err == nil {
		fmt.Println(string(data))
	}
}

// worktreeGuardHookInput is the normalized pre-tool payload the worktree-guard
// subcommand reads on stdin. Codex (PreToolUse) and Gemini (BeforeTool) emit this
// snake_case shape natively; the OpenCode plugin assembles it before piping it in.
type worktreeGuardHookInput struct {
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	Cwd            string          `json:"cwd"`
	PermissionMode string          `json:"permission_mode"`
}

// handleWorktreeGuardHook is the executor-agnostic transport for the worktree
// write-guard, shared by the codex/gemini/opencode pre-tool hooks. It reads the
// worktree root from WORKTREE_PATH (set by every executor when launching the CLI),
// evaluates the single shared policy (EvaluateWorktreeWriteGuard), and renders the
// decision in the requested executor's wire format. It fails open on any error so a
// guard malfunction never wedges the agent.
func handleWorktreeGuardHook(format string) error {
	worktreePath := strings.TrimSpace(os.Getenv("WORKTREE_PATH"))
	if worktreePath == "" {
		return nil // no worktree context — nothing to guard
	}

	var in worktreeGuardHookInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return nil // unparseable payload — allow rather than block
	}

	var allow []string
	if projectDir := executor.ManagedWorktreeProjectDir(worktreePath); projectDir != "" {
		allow = executor.WorktreeAllowExternalWrites(projectDir)
	}

	decision := executor.EvaluateWorktreeWriteGuard(worktreePath, allow, executor.WorktreeGuardInput{
		ToolName:       in.ToolName,
		ToolInput:      in.ToolInput,
		Cwd:            in.Cwd,
		PermissionMode: in.PermissionMode,
	})

	renderWorktreeGuardDecision(format, decision)
	return nil
}

// renderWorktreeGuardDecision writes a guard decision in the wire format the given
// executor's hook expects. None of codex/gemini/opencode support an interactive
// "ask" in their pre-tool hook, so an "ask" is downgraded to a hard deny to fail
// safe (the escape hatch is worktree.allow_external_writes in .taskyou.yml). When
// the guard allows the call, nothing is emitted (and exit stays 0) so the CLI's own
// approval flow proceeds unchanged.
func renderWorktreeGuardDecision(format string, decision *executor.WorktreeGuardDecision) {
	if decision == nil {
		return // allowed
	}

	switch format {
	case "codex":
		// Codex PreToolUse contract: hookSpecificOutput.permissionDecision ∈ {allow,deny}.
		emitJSONLine(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":            "PreToolUse",
				"permissionDecision":       "deny",
				"permissionDecisionReason": decision.Reason,
			},
		})
	case "gemini":
		// Gemini BeforeTool contract: {decision: allow|deny, reason, systemMessage}.
		emitJSONLine(map[string]any{
			"decision":      "deny",
			"reason":        decision.Reason,
			"systemMessage": "Worktree write-guard blocked an out-of-worktree write",
		})
	case "opencode":
		// The OpenCode plugin reads the reason from stdout and treats exit code 1 as
		// a denial (which it surfaces by throwing, aborting the tool call).
		fmt.Println(decision.Reason)
		os.Exit(1)
	}
}

// emitJSONLine marshals v and prints it on a single line to stdout for a hook.
func emitJSONLine(v any) {
	if data, err := json.Marshal(v); err == nil {
		fmt.Println(string(data))
	}
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

	// Re-fetch task status — the MCP server may have changed it during
	// tool execution (e.g. taskyou_needs_input sets blocked).
	task, err = database.GetTask(taskID)
	if err != nil || task == nil {
		return err
	}

	// After a tool completes, Claude is still working (will process tool results).
	// Ensure task remains in "processing" state — unless an MCP tool
	// intentionally set it to blocked for user input.
	if task.Status == db.StatusBlocked {
		// Check if the blocked state is from a question prompt (MCP needs_input).
		// If so, respect it. Otherwise, resume to processing.
		hasQuestion := false
		if logs, err := database.GetTaskLogs(taskID, 3); err == nil {
			for _, l := range logs {
				if l.LineType == "question" {
					hasQuestion = true
					break
				}
			}
		}
		if !hasQuestion {
			database.UpdateTaskStatus(taskID, db.StatusProcessing)
			database.AppendTaskLog(taskID, "system", "Agent resumed working")
		}
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

// shouldSubmitInput decides whether `task input` should press Enter after the
// text. By default a message is submitted so the agent actually receives it;
// --no-submit leaves the text in the input field without submitting. The
// --enter flag (justEnter) always submits, since pressing Enter is its purpose.
func shouldSubmitInput(message string, justEnter, noSubmit bool) bool {
	if justEnter {
		return true
	}
	return message != "" && !noSubmit
}

// formatPermissionDetail extracts a short detail string from the tool input
// for display in permission approval dialogs. Returns "" if no detail available.
func formatPermissionDetail(input *ClaudeHookInput) string {
	if input.ToolName == "" || len(input.ToolInput) == 0 {
		return ""
	}

	var toolInput map[string]interface{}
	if err := json.Unmarshal(input.ToolInput, &toolInput); err != nil {
		return ""
	}

	var detail string
	switch input.ToolName {
	case "Bash":
		if cmd, ok := toolInput["command"].(string); ok {
			detail = cmd
		}
	case "Read":
		if path, ok := toolInput["file_path"].(string); ok {
			detail = path
		}
	case "Write":
		if path, ok := toolInput["file_path"].(string); ok {
			detail = path
		}
	case "Edit":
		if path, ok := toolInput["file_path"].(string); ok {
			detail = path
		}
	case "Glob":
		if pattern, ok := toolInput["pattern"].(string); ok {
			detail = pattern
		}
	case "Grep":
		if pattern, ok := toolInput["pattern"].(string); ok {
			detail = pattern
		}
	case "Task":
		if desc, ok := toolInput["description"].(string); ok {
			detail = desc
		}
	case "WebFetch":
		if url, ok := toolInput["url"].(string); ok {
			detail = url
		}
	case "WebSearch":
		if query, ok := toolInput["query"].(string); ok {
			detail = query
		}
	}

	if detail == "" {
		return ""
	}

	// Truncate to keep the approval bar compact
	if len(detail) > 200 {
		detail = detail[:200] + "..."
	}

	return detail
}

// latestPendingToolDetail retrieves the most recent "pending_tool" log entry
// for a task. This is written by handlePreToolUseHook and consumed here by the
// notification handler to show what tool/command is requesting permission.
func latestPendingToolDetail(database *db.DB, taskID int64) string {
	logs, err := database.GetTaskLogs(taskID, 5)
	if err != nil {
		return ""
	}
	// Logs are in DESC order (most recent first).
	for _, l := range logs {
		if l.LineType == "pending_tool" {
			return l.Content
		}
	}
	return ""
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

	// Exit if orphaned. If our parent goes away, the terminal that launched us
	// is gone and nobody is reading this output -- but only SIGINT/SIGTERM are
	// caught, and an orphan is reparented without either. Left alone a detached
	// `ty logs` polls forever (exactly how one instance ran for ~a month burning
	// CPU). Remember the starting parent PID and bail the moment it changes
	// (reparented to init/launchd == parent died).
	startPpid := os.Getppid()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	// Re-globbing the whole projects tree (hundreds of dirs) is comparatively
	// expensive, so only do it every couple seconds to catch new sessions. The
	// fast tick just streams growth of files we already know about.
	const reglobEvery = 10 // 10 * 200ms ~= 2s
	tick := 0

	for {
		select {
		case <-sigCh:
			fmt.Println()
			return nil
		case <-ticker.C:
			if os.Getppid() != startPpid {
				// Parent died; don't linger as an orphan.
				return nil
			}

			tick++
			if tick%reglobEvery == 0 {
				// Re-glob to catch new files.
				files, _ = filepath.Glob(pattern)
			}

			for _, f := range files {
				// Skip internal agent files (Claude Code sub-agents)
				if strings.HasPrefix(filepath.Base(f), "agent-") {
					continue
				}
				// Cheap change-detection: a bare Stat is one syscall. Skip files
				// that haven't grown so we don't Open+Seek+Scan thousands of
				// unchanged session logs five times a second (the original hot
				// path -- ~3.5k files re-opened at 5Hz = tens of thousands of
				// syscalls/sec, continuously).
				pos, seen := positions[f]
				if info, err := os.Stat(f); err == nil && seen && info.Size() == pos {
					continue
				}
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

// unescapeNewlines converts literal "\n" sequences to actual newline characters
// in CLI input. This allows users to enter multi-line text from the command line.
func unescapeNewlines(s string) string {
	return strings.ReplaceAll(s, "\\n", "\n")
}

// parseStepSet parses a "Step=executor[/model]" workflow-config assignment.
func parseStepSet(s string) (name, executor, model string, err error) {
	parts := strings.SplitN(s, "=", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", "", fmt.Errorf("invalid --set %q (want \"Step=executor[/model]\")", s)
	}
	name = strings.TrimSpace(parts[0])
	em := strings.SplitN(strings.TrimSpace(parts[1]), "/", 2)
	executor = strings.ToLower(strings.TrimSpace(em[0]))
	if len(em) == 2 {
		model = strings.TrimSpace(em[1])
	}
	return name, executor, model, nil
}

// stepNames returns a definition's step names joined for error messages.
func stepNames(def pipeline.Definition) string {
	names := make([]string, len(def.Steps))
	for i, s := range def.Steps {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
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

// execCommandRunner implements web.CommandRunner using os/exec.
type execCommandRunner struct{}

func (r *execCommandRunner) Run(name string, args ...string) error {
	return osexec.Command(name, args...).Run()
}

func (r *execCommandRunner) Output(name string, args ...string) ([]byte, error) {
	return osexec.Command(name, args...).Output()
}

// listSessions lists all running agent task windows in task-daemon.
func listSessions() {
	sessions := getSessions()
	if len(sessions) == 0 {
		fmt.Println(dimStyle.Render("No agent sessions running"))
		return
	}

	// Calculate total memory
	totalMemoryMB := 0
	for _, s := range sessions {
		totalMemoryMB += s.memoryMB
	}

	fmt.Printf("%s\n\n", boldStyle.Render(fmt.Sprintf("Running Agent Sessions (%d total, %dMB memory):", len(sessions), totalMemoryMB)))
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
		executorStr := s.executor
		if executorStr == "" {
			executorStr = "claude" // default
		}
		fmt.Printf("  %s  %-8s  %s  %s  %s\n",
			successStyle.Render(fmt.Sprintf("task-%d", s.taskID)),
			dimStyle.Render(executorStr),
			dimStyle.Render(fmt.Sprintf("%-6s", memStr)),
			dimStyle.Render(fmt.Sprintf("%-36s", titleStr)),
			dimStyle.Render(s.info))
	}
}

type agentSession struct {
	taskID    int
	taskTitle string
	executor  string // Executor name (claude, codex, gemini, etc.)
	memoryMB  int    // Memory usage in MB
	info      string
}

// getSessions returns all running task-* windows across all task-daemon-* sessions.
func getSessions() []agentSession {
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

	// Open database to fetch task titles and executor info
	dbPath := db.DefaultPath()
	database, _ := db.Open(dbPath)
	if database != nil {
		defer database.Close()
	}

	// Build map of task ID -> memory usage (supports all executors)
	taskMemory := getAgentMemoryByTaskID()

	var sessions []agentSession
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

			// Get task title and executor from database
			var taskTitle string
			var taskExecutor string
			if database != nil {
				if task, err := database.GetTask(int64(taskID)); err == nil && task != nil {
					taskTitle = task.Title
					taskExecutor = task.Executor
				}
			}

			// Get memory usage for this task's agent process
			memoryMB := taskMemory[taskID]

			sessions = append(sessions, agentSession{
				taskID:    taskID,
				taskTitle: taskTitle,
				executor:  taskExecutor,
				memoryMB:  memoryMB,
				info:      info,
			})
		}
	}

	return sessions
}

// getAgentMemoryByTaskID returns a map of task ID -> memory (MB) for all agent processes.
// It identifies task IDs by examining each agent process's working directory.
// Supports all executors: claude, codex, gemini, openclaw, opencode, pi.
func getAgentMemoryByTaskID() map[int]int {
	result := make(map[int]int)

	// Find processes for all supported executors
	executorNames := []string{"claude", "codex", "gemini", "openclaw", "opencode", "pi"}

	for _, executorName := range executorNames {
		pgrepOut, err := osexec.Command("pgrep", "-f", executorName).Output()
		if err != nil {
			continue // No processes found for this executor
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
					// If there are multiple agent processes for the same task, sum them
					result[taskID] += memMB
				}
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

// killSession kills a specific task's tmux window in task-daemon.
func killSession(taskID int) error {
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

// killSessionAcrossDaemons kills a task's tmux window across all task-daemon-* sessions.
// Returns true if a window was found and killed.
func killSessionAcrossDaemons(taskID int) bool {
	sessionsOut, err := osexec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return false
	}

	windowName := fmt.Sprintf("task-%d", taskID)
	killed := false

	for _, session := range strings.Split(strings.TrimSpace(string(sessionsOut)), "\n") {
		if !strings.HasPrefix(session, "task-daemon-") {
			continue
		}
		windowTarget := fmt.Sprintf("%s:%s", session, windowName)
		if err := osexec.Command("tmux", "list-panes", "-t", windowTarget).Run(); err != nil {
			continue // Window doesn't exist in this session
		}
		if err := osexec.Command("tmux", "kill-window", "-t", windowTarget).Run(); err == nil {
			killed = true
		}
	}
	return killed
}

// suspendSessions kills agent processes for tasks while preserving their session IDs
// so they can be resumed later. If taskIDs is empty, suspends all blocked tasks with
// running sessions. If all is true, suspends all tasks (not just blocked).
func suspendSessions(taskIDs []int, all bool) {
	// Get running sessions
	sessions := getSessions()
	if len(sessions) == 0 {
		fmt.Println(dimStyle.Render("No agent sessions running"))
		return
	}

	// Build a set of running task IDs for quick lookup
	runningSessions := make(map[int]agentSession)
	for _, s := range sessions {
		runningSessions[s.taskID] = s
	}

	// Open database for status checks and tmux ID cleanup
	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error opening database: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	// Determine which tasks to suspend
	var toSuspend []agentSession
	if len(taskIDs) > 0 {
		// Suspend specific tasks
		for _, id := range taskIDs {
			s, running := runningSessions[id]
			if !running {
				fmt.Fprintf(os.Stderr, "%s\n", dimStyle.Render(fmt.Sprintf("task-%d: no running session, skipping", id)))
				continue
			}
			toSuspend = append(toSuspend, s)
		}
	} else {
		// Suspend based on status
		for _, s := range sessions {
			if all {
				toSuspend = append(toSuspend, s)
				continue
			}
			// Default: only blocked tasks
			task, err := database.GetTask(int64(s.taskID))
			if err != nil || task == nil {
				continue
			}
			if task.Status == db.StatusBlocked {
				toSuspend = append(toSuspend, s)
			}
		}
	}

	if len(toSuspend) == 0 {
		if len(taskIDs) > 0 {
			fmt.Println(dimStyle.Render("No matching running sessions found"))
		} else if all {
			fmt.Println(dimStyle.Render("No running sessions to suspend"))
		} else {
			fmt.Println(dimStyle.Render("No blocked tasks with running sessions to suspend"))
		}
		return
	}

	// Suspend each task
	fmt.Printf("%s\n\n", boldStyle.Render(fmt.Sprintf("Suspending %d session(s):", len(toSuspend))))

	totalFreedMB := 0
	suspended := 0
	for _, s := range toSuspend {
		// Kill the tmux window (kills the agent process)
		killed := killSessionAcrossDaemons(s.taskID)

		// Clear tmux references in DB (window/pane IDs are now stale)
		// but preserve claude_session_id for resume capability
		database.ClearTaskTmuxIDs(int64(s.taskID))

		// Also clear daemon_session since the window is gone
		database.Exec(`UPDATE tasks SET daemon_session = '' WHERE id = ?`, int64(s.taskID))

		title := s.taskTitle
		if len(title) > 40 {
			title = title[:37] + "..."
		}

		memStr := ""
		if s.memoryMB > 0 {
			memStr = fmt.Sprintf(" (%dMB freed)", s.memoryMB)
			totalFreedMB += s.memoryMB
		}

		statusIcon := "✓"
		if !killed {
			statusIcon = "~"
		}

		fmt.Printf("  %s  %s  %s%s\n",
			successStyle.Render(statusIcon),
			successStyle.Render(fmt.Sprintf("task-%d", s.taskID)),
			dimStyle.Render(title),
			dimStyle.Render(memStr))
		suspended++
	}

	fmt.Println()
	if totalFreedMB > 0 {
		fmt.Println(successStyle.Render(fmt.Sprintf("Suspended %d session(s), ~%dMB freed", suspended, totalFreedMB)))
	} else {
		fmt.Println(successStyle.Render(fmt.Sprintf("Suspended %d session(s)", suspended)))
	}
	fmt.Println(dimStyle.Render("Session IDs preserved — use 'ty retry <task-id>' to resume"))
}

// recoverStaleTmuxRefs clears stale daemon_session and tmux_window_id references
// from tasks after a crash or daemon restart. This allows tasks to automatically
// reconnect to their agent sessions when viewed.
func recoverStaleTmuxRefs(dryRun bool) {
	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
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
	fmt.Println(dimStyle.Render("Tasks will automatically reconnect to their agent sessions when viewed."))
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

// cleanupOrphanedSessions kills tmux windows whose task ID is no longer in the
// database (deleted-task orphans), and windows for tasks completed more than
// two hours ago. Killing the window terminates the agent process running in
// its pane via SIGHUP propagation.
//
// Tmux windows are the source of truth here. Earlier versions tried to find
// orphaned agent processes via `pgrep -f <name>.*TERM_PROGRAM=tmux`, but
// TERM_PROGRAM is an env var (not on the command line on macOS), so pgrep
// returned nothing and cleanup quietly did nothing — leaving orphans alive
// indefinitely. Working at the window level avoids depending on env vars
// being visible to ps / pgrep.
func cleanupOrphanedSessions(force bool) {
	type windowRef struct {
		session string
		window  string
		taskID  int
	}

	sessionsOut, err := osexec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error listing tmux sessions: "+err.Error()))
		return
	}

	var allWindows []windowRef
	for _, session := range strings.Split(string(sessionsOut), "\n") {
		session = strings.TrimSpace(session)
		if !strings.HasPrefix(session, "task-daemon-") {
			continue
		}
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
			if _, err := fmt.Sscanf(window, "task-%d", &taskID); err != nil {
				continue
			}
			allWindows = append(allWindows, windowRef{session: session, window: window, taskID: taskID})
		}
	}

	existingTaskIDs := make(map[int]bool)
	oldDoneTaskIDs := make(map[int]bool)
	dbPath := db.DefaultPath()
	if database, err := openTaskDB(dbPath); err == nil {
		defer database.Close()
		twoHoursAgo := time.Now().Add(-2 * time.Hour)
		// IncludeClosed: done/archived tasks are excluded by default, but we need
		// them here so windows for done tasks can be classified correctly. Limit:
		// 0 maps to a default of 100 — set a large explicit limit so a busy queue
		// isn't truncated and active tasks misclassified as deleted.
		if tasks, err := database.ListTasks(db.ListTasksOptions{IncludeClosed: true, Limit: 100000}); err == nil {
			for _, task := range tasks {
				existingTaskIDs[int(task.ID)] = true
				if task.Status == db.StatusDone && task.CompletedAt != nil && task.CompletedAt.Before(twoHoursAgo) {
					oldDoneTaskIDs[int(task.ID)] = true
				}
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf("Warning: could not open database (%v); falling back to tmux-only orphan check", err)))
	}

	var deletedWindows, oldDoneWindows []windowRef
	for _, w := range allWindows {
		if !existingTaskIDs[w.taskID] {
			deletedWindows = append(deletedWindows, w)
			continue
		}
		if oldDoneTaskIDs[w.taskID] {
			oldDoneWindows = append(oldDoneWindows, w)
		}
	}

	totalToKill := len(deletedWindows) + len(oldDoneWindows)
	if totalToKill == 0 {
		fmt.Println(successStyle.Render("No orphaned agent windows found"))
		return
	}

	if len(deletedWindows) > 0 {
		fmt.Printf("%s\n", boldStyle.Render(fmt.Sprintf("Found %d windows for deleted tasks:", len(deletedWindows))))
	}
	if len(oldDoneWindows) > 0 {
		fmt.Printf("%s\n", boldStyle.Render(fmt.Sprintf("Found %d windows for done tasks (>2h old):", len(oldDoneWindows))))
	}

	killed := 0
	for _, w := range append(deletedWindows, oldDoneWindows...) {
		target := w.session + ":" + w.window
		if err := killWindow(target, force); err != nil {
			fmt.Printf("  %s %s: %s\n", errorStyle.Render("✗"), target, err.Error())
			continue
		}
		fmt.Printf("  %s %s\n", successStyle.Render("✓ Killed"), target)
		killed++
	}

	fmt.Printf("\n%s\n", dimStyle.Render(fmt.Sprintf("Killed %d/%d windows", killed, totalToKill)))
}

// killWindow terminates a tmux window. With force=true, it also sends SIGKILL
// to every PID in the window's panes, which is needed for stuck processes
// that don't respond to the SIGHUP that tmux kill-window normally delivers.
func killWindow(target string, force bool) error {
	if force {
		// Best-effort: collect pane PIDs first so we can SIGKILL them after
		// killing the window. tmux kill-window itself can't escalate signals.
		out, _ := osexec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_pid}").Output()
		if err := osexec.Command("tmux", "kill-window", "-t", target).Run(); err != nil {
			return err
		}
		for _, pidStr := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			pid, perr := strconv.Atoi(strings.TrimSpace(pidStr))
			if perr != nil || pid == 0 {
				continue
			}
			if proc, ferr := os.FindProcess(pid); ferr == nil {
				_ = proc.Signal(syscall.SIGKILL)
			}
		}
		return nil
	}
	return osexec.Command("tmux", "kill-window", "-t", target).Run()
}

// moveTask moves a task to a different project by cleaning up old resources,
// deleting the old task, and creating a new task in the target project.
// Returns the new task ID.
func moveTask(database *db.DB, oldTask *db.Task, targetProject string) (int64, error) {
	cfg := config.New(database)
	exec := executor.New(database, cfg)

	// Step 1: Clean up old task's resources

	// Kill agent session if running. Use the across-daemons variant because the
	// CLI invocation's session ID rarely matches the daemon that originally
	// spawned the window — the scoped killSession would silently miss it and
	// leak the agent process. See sessions_test.go for repro.
	killSessionAcrossDaemons(int(oldTask.ID))

	// Clean up worktree and agent sessions if they exist
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

	// Notify about the changes
	exec.NotifyTaskChange("deleted", oldTask)
	exec.NotifyTaskChange("created", newTask)

	return newTask.ID, nil
}

// deleteTask deletes a task, its agent session, and its worktree.
func deleteTask(taskID int64) error {
	// Open database
	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
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

	// Kill agent session if running. Use the across-daemons variant: the CLI's
	// session ID rarely matches the daemon that originally spawned the window
	// (UI launches with WORKTREE_SESSION_ID set to its own PID; the daemon
	// process has its own PID), so a scoped killSession would silently miss
	// the window and leak the agent. See sessions_test.go for repro.
	killSessionAcrossDaemons(int(taskID))

	// Clean up worktree and agent sessions if they exist
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

// listProjectsCLI lists all projects in the database.
func listProjectsCLI(cmd *cobra.Command) {
	outputJSON, _ := cmd.Flags().GetBool("json")

	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	projects, err := database.ListProjects()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}

	if outputJSON {
		var output []map[string]interface{}
		for _, p := range projects {
			taskCount, _ := database.CountTasksByProject(p.Name)
			item := map[string]interface{}{
				"id":         p.ID,
				"name":       p.Name,
				"path":       p.Path,
				"color":      p.Color,
				"task_count": taskCount,
				"created_at": p.CreatedAt.Time.Format(time.RFC3339),
			}
			if p.Aliases != "" {
				item["aliases"] = p.Aliases
			}
			if p.Instructions != "" {
				item["has_instructions"] = true
			}
			if p.ClaudeConfigDir != "" {
				item["claude_config_dir"] = p.ClaudeConfigDir
			}
			output = append(output, item)
		}
		jsonBytes, _ := json.Marshal(output)
		fmt.Println(string(jsonBytes))
		return
	}

	if len(projects) == 0 {
		fmt.Println(dimStyle.Render("No projects found"))
		return
	}

	fmt.Println(boldStyle.Render("Projects"))
	fmt.Println(strings.Repeat("─", 60))

	for _, p := range projects {
		taskCount, _ := database.CountTasksByProject(p.Name)

		// Color indicator
		colorIndicator := ""
		if p.Color != "" {
			colorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(p.Color))
			colorIndicator = colorStyle.Render("●") + " "
		}

		// Name with task count
		nameStr := boldStyle.Render(p.Name)
		countStr := dimStyle.Render(fmt.Sprintf("(%d tasks)", taskCount))

		fmt.Printf("%s%s %s\n", colorIndicator, nameStr, countStr)
		fmt.Printf("  %s\n", dimStyle.Render(p.Path))

		if p.Aliases != "" {
			fmt.Printf("  %s %s\n", dimStyle.Render("Aliases:"), p.Aliases)
		}
	}
}

// showProjectCLI shows detailed information about a single project.
func showProjectCLI(name string, outputJSON bool) {
	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	project, err := database.GetProjectByName(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if project == nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Project '%s' not found", name)))
		os.Exit(1)
	}

	taskCount, _ := database.CountTasksByProject(project.Name)
	projectContext, _ := database.GetProjectContext(project.Name)

	if outputJSON {
		output := map[string]interface{}{
			"id":                      project.ID,
			"name":                    project.Name,
			"path":                    project.Path,
			"color":                   project.Color,
			"aliases":                 project.Aliases,
			"use_worktrees":           project.UseWorktrees,
			"default_permission_mode": project.EffectiveDefaultPermissionMode(),
			"task_count":              taskCount,
			"created_at":              project.CreatedAt.Time.Format(time.RFC3339),
		}
		if project.Instructions != "" {
			output["instructions"] = project.Instructions
		}
		if project.ClaudeConfigDir != "" {
			output["claude_config_dir"] = project.ClaudeConfigDir
		}
		if projectContext != "" {
			output["context"] = projectContext
		}
		if len(project.Actions) > 0 {
			output["actions"] = project.Actions
		}
		jsonBytes, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(jsonBytes))
		return
	}

	// Color indicator
	colorIndicator := ""
	if project.Color != "" {
		colorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(project.Color))
		colorIndicator = colorStyle.Render("●") + " "
	}

	fmt.Println(boldStyle.Render(fmt.Sprintf("%s%s", colorIndicator, project.Name)))
	fmt.Println(strings.Repeat("─", 60))

	fmt.Printf("%s %s\n", dimStyle.Render("Path:"), project.Path)
	fmt.Printf("%s %d\n", dimStyle.Render("Tasks:"), taskCount)

	if project.Aliases != "" {
		fmt.Printf("%s %s\n", dimStyle.Render("Aliases:"), project.Aliases)
	}
	if project.Color != "" {
		fmt.Printf("%s %s\n", dimStyle.Render("Color:"), project.Color)
	}
	if project.ClaudeConfigDir != "" {
		fmt.Printf("%s %s\n", dimStyle.Render("Claude Config:"), project.ClaudeConfigDir)
	}
	if !project.UseWorktrees {
		fmt.Printf("%s %s\n", dimStyle.Render("Git Worktrees:"), "disabled (non-git project)")
	}
	fmt.Printf("%s %s\n", dimStyle.Render("Permission Mode:"), project.EffectiveDefaultPermissionMode())

	if project.Instructions != "" {
		fmt.Println()
		fmt.Println(boldStyle.Render("Instructions:"))
		fmt.Println(project.Instructions)
	}

	if len(project.Actions) > 0 {
		fmt.Println()
		fmt.Println(boldStyle.Render("Actions:"))
		for _, action := range project.Actions {
			fmt.Printf("  %s: %s\n", dimStyle.Render(action.Trigger), action.Instructions)
		}
	}

	if projectContext != "" {
		fmt.Println()
		fmt.Println(boldStyle.Render("Context:"))
		// Truncate context if too long for display
		contextDisplay := projectContext
		if len(contextDisplay) > 500 {
			contextDisplay = contextDisplay[:500] + "...\n" + dimStyle.Render(fmt.Sprintf("(truncated, %d chars total)", len(projectContext)))
		}
		fmt.Println(contextDisplay)
	}
}

// createProjectCLI creates a new project.
func createProjectCLI(name, path, instructions, color, aliases, claudeConfigDir, permissionMode string, noGit bool, outputJSON bool) {
	// Validate name
	if strings.TrimSpace(name) == "" {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: project name cannot be empty"))
		os.Exit(1)
	}

	// Expand path
	if path == "" {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: --path is required"))
		os.Exit(1)
	}
	expandedPath := path
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		expandedPath = filepath.Join(home, path[1:])
	}
	absPath, err := filepath.Abs(expandedPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: invalid path: "+err.Error()))
		os.Exit(1)
	}

	// Check if path exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error: path does not exist: %s", absPath)))
		os.Exit(1)
	}

	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	// Check if project already exists
	existing, _ := database.GetProjectByName(name)
	if existing != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error: project '%s' already exists", name)))
		os.Exit(1)
	}

	project := &db.Project{
		Name:                  name,
		Path:                  absPath,
		Instructions:          instructions,
		Color:                 color,
		Aliases:               aliases,
		ClaudeConfigDir:       claudeConfigDir,
		UseWorktrees:          !noGit,
		DefaultPermissionMode: db.NormalizePermissionMode(permissionMode),
	}

	if err := database.CreateProject(project); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}

	if outputJSON {
		output := map[string]interface{}{
			"id":            project.ID,
			"name":          project.Name,
			"path":          project.Path,
			"use_worktrees": project.UseWorktrees,
			"created_at":    project.CreatedAt.Time.Format(time.RFC3339),
		}
		if project.Color != "" {
			output["color"] = project.Color
		}
		if project.Instructions != "" {
			output["instructions"] = project.Instructions
		}
		jsonBytes, _ := json.Marshal(output)
		fmt.Println(string(jsonBytes))
	} else {
		suffix := ""
		if !project.UseWorktrees {
			suffix = " (non-git, worktrees disabled)"
		}
		fmt.Println(successStyle.Render(fmt.Sprintf("Created project '%s' at %s%s", name, absPath, suffix)))
	}
}

// updateProjectCLI updates an existing project.
func updateProjectCLI(currentName, newName, path, instructions, color, aliases, claudeConfigDir, projectContext, permissionMode string, useWorktrees *bool, outputJSON bool) {
	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	project, err := database.GetProjectByName(currentName)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if project == nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Project '%s' not found", currentName)))
		os.Exit(1)
	}

	// Track what changed
	changes := []string{}

	// Update fields if provided
	if newName != "" && newName != project.Name {
		// Check if new name is available
		existing, _ := database.GetProjectByName(newName)
		if existing != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error: project '%s' already exists", newName)))
			os.Exit(1)
		}
		project.Name = newName
		changes = append(changes, "name")
	}

	if path != "" {
		expandedPath := path
		if strings.HasPrefix(path, "~") {
			home, _ := os.UserHomeDir()
			expandedPath = filepath.Join(home, path[1:])
		}
		absPath, err := filepath.Abs(expandedPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("Error: invalid path: "+err.Error()))
			os.Exit(1)
		}
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error: path does not exist: %s", absPath)))
			os.Exit(1)
		}
		project.Path = absPath
		changes = append(changes, "path")
	}

	if instructions != "" {
		project.Instructions = instructions
		changes = append(changes, "instructions")
	}

	if color != "" {
		project.Color = color
		changes = append(changes, "color")
	}

	if aliases != "" {
		project.Aliases = aliases
		changes = append(changes, "aliases")
	}

	if claudeConfigDir != "" {
		project.ClaudeConfigDir = claudeConfigDir
		changes = append(changes, "claude_config_dir")
	}

	if permissionMode != "" {
		normalized := db.NormalizePermissionMode(permissionMode)
		if normalized == "" {
			fmt.Fprintln(os.Stderr, errorStyle.Render("Error: invalid permission mode (use default, accept-edits, auto, or dangerous)"))
			os.Exit(1)
		}
		project.DefaultPermissionMode = normalized
		changes = append(changes, "default permission mode")
	}

	if useWorktrees != nil {
		project.UseWorktrees = *useWorktrees
		if *useWorktrees {
			changes = append(changes, "git worktrees enabled")
		} else {
			changes = append(changes, "git worktrees disabled")
		}
	}

	if len(changes) == 0 && projectContext == "" {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: no changes specified"))
		os.Exit(1)
	}

	if err := database.UpdateProject(project); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}

	// Handle context separately
	if projectContext != "" {
		if err := database.SetProjectContext(project.Name, projectContext); err != nil {
			fmt.Fprintln(os.Stderr, errorStyle.Render("Error updating context: "+err.Error()))
			os.Exit(1)
		}
		changes = append(changes, "context")
	}

	if outputJSON {
		output := map[string]interface{}{
			"id":      project.ID,
			"name":    project.Name,
			"path":    project.Path,
			"updated": changes,
		}
		jsonBytes, _ := json.Marshal(output)
		fmt.Println(string(jsonBytes))
	} else {
		fmt.Println(successStyle.Render(fmt.Sprintf("Updated project '%s': %s", project.Name, strings.Join(changes, ", "))))
	}
}

// deleteProjectCLI deletes a project.
func deleteProjectCLI(name string, force bool) {
	dbPath := db.DefaultPath()
	database, err := openTaskDB(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	defer database.Close()

	project, err := database.GetProjectByName(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if project == nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Project '%s' not found", name)))
		os.Exit(1)
	}

	// The 'personal' project cannot be deleted (enforced in DB layer too)
	if project.Name == "personal" {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: the 'personal' project cannot be deleted"))
		os.Exit(1)
	}

	// Confirm unless --force flag is set
	if !force {
		taskCount, _ := database.CountTasksByProject(project.Name)
		msg := fmt.Sprintf("Delete project '%s'?", name)
		if taskCount > 0 {
			msg = fmt.Sprintf("Delete project '%s' (%d tasks will become unassigned)?", name, taskCount)
		}
		fmt.Printf("%s [y/N] ", msg)
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Cancelled")
			return
		}
	}

	if err := database.DeleteProject(project.ID); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}

	fmt.Println(successStyle.Render(fmt.Sprintf("Deleted project '%s'", name)))
}

// parseKeyEvents parses a comma-separated string of keys into bubbletea KeyMsgs.
func parseKeyEvents(input string) []tea.Msg {
	var msgs []tea.Msg
	// Split by comma, but be careful about text input that might contain commas if we were robust.
	// For now, simple split is fine as per instructions.
	parts := strings.Split(input, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		var msg tea.KeyMsg
		// Check for special keys
		switch strings.ToLower(part) {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc", "escape":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		case "delete":
			msg = tea.KeyMsg{Type: tea.KeyDelete}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "pgup", "pageup":
			msg = tea.KeyMsg{Type: tea.KeyPgUp}
		case "pgdown", "pagedown":
			msg = tea.KeyMsg{Type: tea.KeyPgDown}
		case "home":
			msg = tea.KeyMsg{Type: tea.KeyHome}
		case "end":
			msg = tea.KeyMsg{Type: tea.KeyEnd}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		default:
			// Treat as text input
			// If length is 1, it's a single rune keypress
			// If length > 1, it's a sequence of rune keypresses
			for _, r := range part {
				msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			continue
		}
		msgs = append(msgs, msg)
	}
	return msgs
}
