package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
)

// PiExecutor implements TaskExecutor for Pi coding agent.
type PiExecutor struct {
	executor *Executor
	logger   *log.Logger
}

// NewPiExecutor creates a new Pi executor.
func NewPiExecutor(e *Executor) *PiExecutor {
	return &PiExecutor{
		executor: e,
		logger:   e.logger,
	}
}

// Name returns the executor name.
func (p *PiExecutor) Name() string {
	return db.ExecutorPi
}

// IsAvailable checks if the pi CLI is installed.
func (p *PiExecutor) IsAvailable() bool {
	_, err := exec.LookPath("pi")
	return err == nil
}

// Execute runs a task using Pi.
func (p *PiExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	result := p.executor.runPi(ctx, task, workDir, prompt)
	return ExecResult(result)
}

// Resume resumes a previous Pi session with feedback.
func (p *PiExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	result := p.executor.runPiResume(ctx, task, workDir, prompt, feedback)
	return ExecResult(result)
}

// GetProcessID returns the PID of the Pi process for a task.
func (p *PiExecutor) GetProcessID(taskID int64) int {
	return p.executor.getPiPID(taskID)
}

// Kill terminates the Pi process for a task.
func (p *PiExecutor) Kill(taskID int64) bool {
	return p.executor.KillPiProcess(taskID)
}

// Suspend pauses the Pi process for a task.
func (p *PiExecutor) Suspend(taskID int64) bool {
	return p.executor.SuspendTask(taskID)
}

// IsSuspended checks if a task's Pi process is suspended.
func (p *PiExecutor) IsSuspended(taskID int64) bool {
	return p.executor.IsSuspended(taskID)
}

// BuildCommand returns the shell command to start an interactive Pi session.
func (p *PiExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string {
	// Get session ID for environment
	worktreeSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if worktreeSessionID == "" {
		worktreeSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Build system prompt flag
	systemPromptFlag := ""
	systemFile, err := os.CreateTemp("", "task-system-*.txt")
	if err == nil {
		systemFile.WriteString(p.executor.buildSystemInstructions())
		systemFile.Close()
		systemPromptFlag = fmt.Sprintf(`--append-system-prompt %q `, systemFile.Name())
	}

	// Build command - resume if we have a session ID, otherwise start fresh
	if sessionID != "" {
		cmd := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q pi %s--continue`,
			task.ID, worktreeSessionID, task.Port, task.WorktreePath, systemPromptFlag)
		if systemFile != nil {
			cmd += fmt.Sprintf(`; rm -f %q`, systemFile.Name())
		}
		return cmd
	}

	// Start fresh - if prompt is provided, write to temp file and pass it
	if prompt != "" {
		// Create temp file for prompt (avoids shell quoting issues)
		promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
		if err != nil {
			p.logger.Error("BuildCommand: failed to create temp file", "error", err)
			cmd := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q pi %s`,
				task.ID, worktreeSessionID, task.Port, task.WorktreePath, systemPromptFlag)
			if systemFile != nil {
				cmd += fmt.Sprintf(`; rm -f %q`, systemFile.Name())
			}
			return cmd
		}
		promptFile.WriteString(prompt)
		promptFile.Close()

		cmd := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q pi %s"$(cat %q)"; rm -f %q`,
			task.ID, worktreeSessionID, task.Port, task.WorktreePath, systemPromptFlag, promptFile.Name(), promptFile.Name())
		if systemFile != nil {
			cmd += fmt.Sprintf(` %q`, systemFile.Name())
		}
		return cmd
	}

	cmd := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q pi %s`,
		task.ID, worktreeSessionID, task.Port, task.WorktreePath, systemPromptFlag)
	if systemFile != nil {
		cmd += fmt.Sprintf(`; rm -f %q`, systemFile.Name())
	}
	return cmd
}

// ---- Session and Dangerous Mode Support ----

// SupportsSessionResume returns true - Pi supports session resume via --continue.
func (p *PiExecutor) SupportsSessionResume() bool {
	return true
}

// SupportsDangerousMode returns false - Pi doesn't have a dangerous mode flag.
func (p *PiExecutor) SupportsDangerousMode() bool {
	return false
}

// FindSessionID discovers the most recent Pi session ID for the given workDir.
func (p *PiExecutor) FindSessionID(workDir string) string {
	return findPiSessionID(workDir)
}

// ResumeDangerous is not supported for Pi.
func (p *PiExecutor) ResumeDangerous(task *db.Task, workDir string) bool {
	p.executor.logLine(task.ID, "system", "Pi executor does not support dangerous mode")
	return false
}

// ResumeSafe is not supported for Pi.
func (p *PiExecutor) ResumeSafe(task *db.Task, workDir string) bool {
	p.executor.logLine(task.ID, "system", "Pi executor does not support dangerous mode")
	return false
}

// findPiSessionID finds the most recent Pi session ID for a workDir.
// Pi stores sessions in ~/.pi/agent/sessions/<escaped-path>/
func findPiSessionID(workDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Pi escapes the path similar to Claude: /Users/bruno/foo -> --Users-bruno-foo--
	escapedPath := "--" + strings.ReplaceAll(workDir, "/", "-") + "--"
	sessionDir := filepath.Join(home, ".pi", "agent", "sessions", escapedPath)

	// Find the most recent .jsonl file
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return ""
	}

	var latestTime time.Time
	var latestSession string

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			// Session file format: 2026-01-31T16-56-49-866Z_aa857952-4ced-4fcc-a8c9-53966931221d.jsonl
			// We just need the path to use with --continue
			latestSession = filepath.Join(sessionDir, name)
		}
	}

	return latestSession
}

// piSessionExists checks if a Pi session file exists.
func piSessionExists(sessionPath string) bool {
	if sessionPath == "" {
		return false
	}
	_, err := os.Stat(sessionPath)
	return err == nil
}
