package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
)

// CodexExecutor implements TaskExecutor for OpenAI's Codex CLI.
type CodexExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewCodexExecutor creates a new Codex executor.
func NewCodexExecutor(e *Executor) *CodexExecutor {
	return &CodexExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (c *CodexExecutor) Name() string {
	return db.ExecutorCodex
}

// IsAvailable checks if the codex CLI is installed.
func (c *CodexExecutor) IsAvailable() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

// Execute runs a task using Codex CLI.
func (c *CodexExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return c.runCodex(ctx, task, workDir, prompt, "", false)
}

// Resume resumes a previous Codex session with feedback.
func (c *CodexExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return c.runCodex(ctx, task, workDir, prompt, feedback, true)
}

// runCodex executes the Codex CLI with the given parameters.
func (c *CodexExecutor) runCodex(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	// Check if codex CLI is available
	if !c.IsAvailable() {
		c.executor.logLine(task.ID, "error", "codex CLI is not installed - please install it from https://github.com/openai/codex")
		return ExecResult{Message: "codex CLI is not installed"}
	}

	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		c.executor.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return ExecResult{Message: "tmux is not installed"}
	}

	// Ensure task-daemon session exists
	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		c.logger.Error("could not create task-daemon session", "error", err)
		c.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Kill ALL existing windows with this name (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	// Create a temp file for the prompt
	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		c.logger.Error("could not create temp file", "error", err)
		c.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}

	// Build the full prompt
	fullPrompt := prompt
	if isResume && feedback != "" {
		fullPrompt = prompt + "\n\n## User Feedback\n\n" + feedback
	}
	promptFile.WriteString(fullPrompt)
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	// Build environment variables
	sessionID := os.Getenv("WORKTREE_SESSION_ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Build the codex command
	// Codex CLI uses different flags than Claude:
	// - --full-auto for autonomous mode (similar to Claude's default)
	// - prompts are passed via stdin or as arguments
	var script string
	dangerousFlag := ""
	if os.Getenv("WORKTREE_DANGEROUS_MODE") == "1" {
		dangerousFlag = "--full-auto "
	}

	// Note: Codex doesn't have built-in session resume like Claude,
	// so we always start fresh but include full context
	script = fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q codex %s"$(cat %q)"`,
		task.ID, sessionID, task.Port, task.WorktreePath, dangerousFlag, promptFile.Name())

	// Create new window in task-daemon session
	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		c.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		c.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	// Update windowTarget if session changed during retry
	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	// Give tmux a moment to start
	time.Sleep(200 * time.Millisecond)

	// Save which daemon session owns this task's window
	if err := c.executor.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		c.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}

	// Capture and store the window ID for reliable targeting
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := c.executor.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			c.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	// Ensure shell pane exists alongside Codex pane
	c.executor.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath)

	// Configure tmux window
	c.executor.configureTmuxWindow(windowTarget)

	// Poll for output and completion
	result := c.executor.pollTmuxSession(ctx, task.ID, windowTarget)

	return ExecResult(result)
}

// GetProcessID returns the PID of the Codex process for a task.
func (c *CodexExecutor) GetProcessID(taskID int64) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	windowName := TmuxWindowName(taskID)

	// Search all tmux sessions for a window with this task's name
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{session_name}:#{window_name}:#{pane_index} #{pane_pid}").Output()
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		target := parts[0]
		pidStr := parts[1]

		if !strings.Contains(target, windowName) {
			continue
		}

		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Check if this is a codex process or has codex as child
		cmdOut, _ := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
		if strings.Contains(string(cmdOut), "codex") {
			return pid
		}

		// Check for codex child process
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "codex").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
			if err == nil {
				return childPid
			}
		}
	}

	return 0
}

// Kill terminates the Codex process for a task.
func (c *CodexExecutor) Kill(taskID int64) bool {
	pid := c.GetProcessID(taskID)
	if pid == 0 {
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		c.logger.Debug("Failed to find Codex process", "pid", pid, "error", err)
		return false
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		c.logger.Debug("Failed to terminate Codex process", "pid", pid, "error", err)
		return false
	}

	c.logger.Info("Terminated Codex process", "task", taskID, "pid", pid)

	// Clean up suspended task tracking
	delete(c.suspendedTasks, taskID)

	return true
}

// Suspend pauses the Codex process for a task.
func (c *CodexExecutor) Suspend(taskID int64) bool {
	pid := c.GetProcessID(taskID)
	if pid == 0 {
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		c.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}

	if err := proc.Signal(syscall.SIGTSTP); err != nil {
		c.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}

	c.suspendedTasks[taskID] = time.Now()

	c.logger.Info("Suspended Codex process", "task", taskID, "pid", pid)
	c.executor.logLine(taskID, "system", "Codex suspended (idle timeout)")
	return true
}

// IsSuspended checks if a task's Codex process is suspended.
func (c *CodexExecutor) IsSuspended(taskID int64) bool {
	_, suspended := c.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a suspended Codex process.
func (c *CodexExecutor) ResumeProcess(taskID int64) bool {
	if !c.IsSuspended(taskID) {
		return false
	}

	pid := c.GetProcessID(taskID)
	if pid == 0 {
		delete(c.suspendedTasks, taskID)
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		delete(c.suspendedTasks, taskID)
		return false
	}

	if err := proc.Signal(syscall.SIGCONT); err != nil {
		c.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}

	delete(c.suspendedTasks, taskID)

	c.logger.Info("Resumed Codex process", "task", taskID, "pid", pid)
	c.executor.logLine(taskID, "system", "Codex resumed")
	return true
}
