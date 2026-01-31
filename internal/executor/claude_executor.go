package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
)

// ClaudeExecutor implements TaskExecutor for Claude Code CLI.
// This wraps the existing Claude execution logic in executor.go.
type ClaudeExecutor struct {
	executor *Executor
	logger   *log.Logger
}

// NewClaudeExecutor creates a new Claude executor.
func NewClaudeExecutor(e *Executor) *ClaudeExecutor {
	return &ClaudeExecutor{
		executor: e,
		logger:   e.logger,
	}
}

// Name returns the executor name.
func (c *ClaudeExecutor) Name() string {
	return db.ExecutorClaude
}

// IsAvailable checks if the claude CLI is installed.
func (c *ClaudeExecutor) IsAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// Execute runs a task using Claude Code CLI.
func (c *ClaudeExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	result := c.executor.runClaude(ctx, task, workDir, prompt)
	return ExecResult(result)
}

// Resume resumes a previous Claude session with feedback.
func (c *ClaudeExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	result := c.executor.runClaudeResume(ctx, task, workDir, prompt, feedback)
	return ExecResult(result)
}

// GetProcessID returns the PID of the Claude process for a task.
func (c *ClaudeExecutor) GetProcessID(taskID int64) int {
	return c.executor.getClaudePID(taskID)
}

// Kill terminates the Claude process for a task.
func (c *ClaudeExecutor) Kill(taskID int64) bool {
	return c.executor.KillClaudeProcess(taskID)
}

// Suspend pauses the Claude process for a task.
func (c *ClaudeExecutor) Suspend(taskID int64) bool {
	return c.executor.SuspendTask(taskID)
}

// IsSuspended checks if a task's Claude process is suspended.
func (c *ClaudeExecutor) IsSuspended(taskID int64) bool {
	return c.executor.IsSuspended(taskID)
}

// ResumeProcess resumes a suspended Claude process.
func (c *ClaudeExecutor) ResumeProcess(taskID int64) bool {
	return c.executor.ResumeTask(taskID)
}

// BuildCommand returns the shell command to start an interactive Claude session.
func (c *ClaudeExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	// Build dangerous mode flag
	dangerousFlag := ""
	if task.DangerousMode || os.Getenv("WORKTREE_DANGEROUS_MODE") == "1" {
		dangerousFlag = "--dangerously-skip-permissions "
	}

	// Get session ID for environment
	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Build command - resume if we have a session ID, otherwise start fresh
	if sessionID != "" {
		return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q claude %s--chrome --resume %s`,
			task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag, sessionID)
	}

	// Start fresh - if prompt is provided, write to temp file and pass it
	if prompt != "" {
		// Create temp file for prompt (avoids shell quoting issues)
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			c.logger.Error("BuildCommand: failed to create temp file", "error", err)
			return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q claude %s--chrome`,
				task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag)
		}
		promptFile.WriteString(prompt)
		promptFile.Close()

		return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q claude %s--chrome "$(cat %q)"; rm -f %q`,
			task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag, promptFile.Name(), promptFile.Name())
	}

	return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q claude %s--chrome`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag)
}

// ---- Session and Dangerous Mode Support ----

// SupportsSessionResume returns true - Claude supports session resume via --resume.
func (c *ClaudeExecutor) SupportsSessionResume() bool {
	return true
}

// SupportsDangerousMode returns true - Claude supports --dangerously-skip-permissions.
func (c *ClaudeExecutor) SupportsDangerousMode() bool {
	return true
}

// FindSessionID discovers the most recent Claude session ID for the given workDir.
func (c *ClaudeExecutor) FindSessionID(workDir string) string {
	return FindClaudeSessionID(workDir)
}

// ResumeDangerous kills the current Claude process and restarts with --dangerously-skip-permissions.
func (c *ClaudeExecutor) ResumeDangerous(task *db.Task, workDir string) bool {
	return c.executor.resumeClaudeDangerous(task, workDir)
}

// ResumeSafe kills the current Claude process and restarts without --dangerously-skip-permissions.
func (c *ClaudeExecutor) ResumeSafe(task *db.Task, workDir string) bool {
	return c.executor.resumeClaudeSafe(task, workDir)
}
