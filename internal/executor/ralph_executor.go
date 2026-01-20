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

// RalphExecutor implements TaskExecutor for the Amp CLI (used by Ralph).
// Ralph is an autonomous AI agent loop that orchestrates Amp.
type RalphExecutor struct {
	executor       *Executor
	logger         *log.Logger
	suspendedTasks map[int64]time.Time
}

// NewRalphExecutor creates a new Ralph executor.
func NewRalphExecutor(e *Executor) *RalphExecutor {
	return &RalphExecutor{
		executor:       e,
		logger:         e.logger,
		suspendedTasks: make(map[int64]time.Time),
	}
}

// Name returns the executor name.
func (r *RalphExecutor) Name() string {
	return db.ExecutorRalph
}

// IsAvailable checks if the amp CLI is installed.
func (r *RalphExecutor) IsAvailable() bool {
	_, err := exec.LookPath("amp")
	return err == nil
}

// Execute runs a task using Amp CLI.
func (r *RalphExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return r.runAmp(ctx, task, workDir, prompt, "", false)
}

// Resume resumes a previous session with feedback.
func (r *RalphExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return r.runAmp(ctx, task, workDir, prompt, feedback, true)
}

// runAmp executes the Amp CLI with the given parameters.
func (r *RalphExecutor) runAmp(ctx context.Context, task *db.Task, workDir, prompt, feedback string, isResume bool) ExecResult {
	// Check if amp CLI is available
	if !r.IsAvailable() {
		r.executor.logLine(task.ID, "error", "amp CLI is not installed - please install it from https://github.com/anthropics/amp")
		return ExecResult{Message: "amp CLI is not installed"}
	}

	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		r.executor.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return ExecResult{Message: "tmux is not installed"}
	}

	// Ensure task-daemon session exists
	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		r.logger.Error("could not create task-daemon session", "error", err)
		r.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return ExecResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Kill ALL existing windows with this name (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	// Create a temp file for the prompt
	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		r.logger.Error("could not create temp file", "error", err)
		r.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
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

	// Build the amp command
	// Amp CLI uses --dangerously-allow-all for autonomous mode (similar to Claude's dangerous mode)
	// Ralph's default behavior is to run with this flag enabled
	var script string
	dangerousFlag := "--dangerously-allow-all"
	if os.Getenv("WORKTREE_DANGEROUS_MODE") != "1" {
		// If dangerous mode is NOT enabled, don't use the flag
		dangerousFlag = ""
	}

	// Note: Amp doesn't have built-in session resume like Claude,
	// so we always start fresh but include full context
	// The prompt is piped to amp via stdin (like ralph.sh does)
	if dangerousFlag != "" {
		script = fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q cat %q | amp %s`,
			task.ID, sessionID, task.Port, task.WorktreePath, promptFile.Name(), dangerousFlag)
	} else {
		script = fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q cat %q | amp`,
			task.ID, sessionID, task.Port, task.WorktreePath, promptFile.Name())
	}

	// Create new window in task-daemon session
	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		r.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		r.executor.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
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
	if err := r.executor.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		r.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}

	// Capture and store the window ID for reliable targeting
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := r.executor.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			r.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	// Ensure shell pane exists alongside Amp pane
	r.executor.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath)

	// Configure tmux window
	r.executor.configureTmuxWindow(windowTarget)

	// Poll for output and completion
	result := r.executor.pollTmuxSession(ctx, task.ID, windowTarget)

	return ExecResult(result)
}

// GetProcessID returns the PID of the Amp process for a task.
func (r *RalphExecutor) GetProcessID(taskID int64) int {
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

		// Check if this is an amp process or has amp as child
		cmdOut, _ := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
		if strings.Contains(string(cmdOut), "amp") {
			return pid
		}

		// Check for amp child process
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "amp").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
			if err == nil {
				return childPid
			}
		}
	}

	return 0
}

// Kill terminates the Amp process for a task.
func (r *RalphExecutor) Kill(taskID int64) bool {
	pid := r.GetProcessID(taskID)
	if pid == 0 {
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		r.logger.Debug("Failed to find Amp process", "pid", pid, "error", err)
		return false
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		r.logger.Debug("Failed to terminate Amp process", "pid", pid, "error", err)
		return false
	}

	r.logger.Info("Terminated Amp process", "task", taskID, "pid", pid)

	// Clean up suspended task tracking
	delete(r.suspendedTasks, taskID)

	return true
}

// Suspend pauses the Amp process for a task.
func (r *RalphExecutor) Suspend(taskID int64) bool {
	pid := r.GetProcessID(taskID)
	if pid == 0 {
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		r.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}

	if err := proc.Signal(syscall.SIGTSTP); err != nil {
		r.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}

	r.suspendedTasks[taskID] = time.Now()

	r.logger.Info("Suspended Amp process", "task", taskID, "pid", pid)
	r.executor.logLine(taskID, "system", "Amp suspended (idle timeout)")
	return true
}

// IsSuspended checks if a task's Amp process is suspended.
func (r *RalphExecutor) IsSuspended(taskID int64) bool {
	_, suspended := r.suspendedTasks[taskID]
	return suspended
}

// ResumeProcess resumes a suspended Amp process.
func (r *RalphExecutor) ResumeProcess(taskID int64) bool {
	if !r.IsSuspended(taskID) {
		return false
	}

	pid := r.GetProcessID(taskID)
	if pid == 0 {
		delete(r.suspendedTasks, taskID)
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		delete(r.suspendedTasks, taskID)
		return false
	}

	if err := proc.Signal(syscall.SIGCONT); err != nil {
		r.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}

	delete(r.suspendedTasks, taskID)

	r.logger.Info("Resumed Amp process", "task", taskID, "pid", pid)
	r.executor.logLine(taskID, "system", "Amp resumed")
	return true
}
