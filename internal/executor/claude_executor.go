package executor

import (
	"context"
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
	return ExecResult{
		Success:     result.Success,
		NeedsInput:  result.NeedsInput,
		Interrupted: result.Interrupted,
		Message:     result.Message,
	}
}

// Resume resumes a previous Claude session with feedback.
func (c *ClaudeExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	result := c.executor.runClaudeResume(ctx, task, workDir, prompt, feedback)
	return ExecResult{
		Success:     result.Success,
		NeedsInput:  result.NeedsInput,
		Interrupted: result.Interrupted,
		Message:     result.Message,
	}
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
