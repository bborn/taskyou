package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/log"

	"github.com/bborn/workflow/internal/db"
)

// shellSingleQuote wraps s in single quotes for safe interpolation into a shell
// command, escaping any embedded single quote via the standard close-escape-reopen
// idiom:
//
//	'\''
//
// Unlike fmt's %q (which produces Go-string quoting and leaves $, backticks,
// and the like live for the shell), this neutralizes shell metacharacters and
// is safe against command injection.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ClaudeExecutor implements TaskExecutor for Claude Code CLI.
// This wraps the existing Claude execution logic in executor.go.
type ClaudeExecutor struct {
	executor *Executor
	logger   *log.Logger
}

// claudePermissionFlag returns the Claude CLI permission flag (with a trailing
// space) for a task's effective permission mode. The WORKTREE_DANGEROUS_MODE
// environment variable forces dangerous mode for sandboxed environments.
func claudePermissionFlag(task *db.Task) string {
	if os.Getenv("WORKTREE_DANGEROUS_MODE") == "1" {
		return "--dangerously-skip-permissions "
	}
	switch task.EffectivePermissionMode() {
	case db.PermissionModeDangerous:
		return "--dangerously-skip-permissions "
	case db.PermissionModeAuto:
		return "--permission-mode auto "
	case db.PermissionModeAcceptEdits:
		return "--permission-mode acceptEdits "
	default:
		return ""
	}
}

// effortFlag returns the `--effort <level> ` CLI flag (with a trailing space) for a
// per-task effort override, or an empty string when no override is set. An empty
// override means the task uses Claude's global default, leaving the user's global
// effort setting untouched.
func effortFlag(level string) string {
	if level == "" {
		return ""
	}
	return fmt.Sprintf("--effort %s ", level)
}

// rcFlag returns the `--remote-control <name> ` CLI flag (with a trailing
// space) for a task that has Remote Control enabled, or an empty string
// otherwise. The session name is the task title, falling back to `task-<id>`
// when the title is empty. The name is shell-single-quoted because the returned
// flag is concatenated into a script run via `sh -c`, and the title is
// arbitrary user/MCP/daemon-supplied text.
func rcFlag(task *db.Task) string {
	if !task.RemoteControl {
		return ""
	}
	rcName := task.Title
	if rcName == "" {
		rcName = fmt.Sprintf("task-%d", task.ID)
	}
	return fmt.Sprintf("--remote-control %s ", shellSingleQuote(rcName))
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
	// Build permission mode flag (dangerous, auto, accept-edits, or none)
	dangerousFlag := claudePermissionFlag(task)

	// Build per-task effort override flag (empty = use Claude's global default)
	effort := effortFlag(task.EffortLevel)

	// Get session ID for environment
	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Build command - resume if we have a session ID, otherwise start fresh
	if sessionID != "" {
		return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q claude %s%s--resume %s`,
			task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag, effort, sessionID)
	}

	// Start fresh - if prompt is provided, write to temp file and pass it
	if prompt != "" {
		// Create temp file for prompt (avoids shell quoting issues)
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			c.logger.Error("BuildCommand: failed to create temp file", "error", err)
			return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q claude %s%s`,
				task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag, effort)
		}
		promptFile.WriteString(prompt)
		promptFile.Close()

		return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q claude %s%s"$(cat %q)"; rm -f %q`,
			task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag, effort, promptFile.Name(), promptFile.Name())
	}

	return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q claude %s%s`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath, dangerousFlag, effort)
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
