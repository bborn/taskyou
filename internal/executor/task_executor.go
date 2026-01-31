// Package executor provides task execution with pluggable executor backends.
package executor

import (
	"context"

	"github.com/bborn/workflow/internal/db"
)

// ExecResult represents the result of executing a task.
type ExecResult struct {
	Success     bool   // Task completed successfully
	NeedsInput  bool   // Task is waiting for user input
	Interrupted bool   // Task was interrupted by user
	Message     string // Status message or error
}

// toInternal converts ExecResult to the internal execResult type.
func (r ExecResult) toInternal() execResult {
	return execResult(r)
}

// TaskExecutor defines the interface for task execution backends.
// Implementations handle the actual running of tasks using different CLI tools.
type TaskExecutor interface {
	// Name returns the executor name (e.g., "claude", "codex")
	Name() string

	// Execute runs a task with the given prompt and returns the result.
	// The workDir is the directory where the executor should run.
	Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult

	// Resume resumes a previous session with additional feedback.
	// If no previous session exists, it should start fresh with the full prompt + feedback.
	Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult

	// BuildCommand returns the shell command to start an interactive session.
	// This is used by the UI to start the executor in a tmux window.
	// The sessionID is optional - if provided, the executor should resume that session if supported.
	// The prompt is the task prompt to pass to the executor.
	BuildCommand(task *db.Task, sessionID, prompt string) string

	// IsAvailable checks if the executor CLI is installed and available.
	IsAvailable() bool

	// GetProcessID returns the PID of the executor process for a task, or 0 if not running.
	GetProcessID(taskID int64) int

	// Kill terminates the executor process for a task.
	Kill(taskID int64) bool

	// Suspend pauses the executor process for a task (to save memory).
	Suspend(taskID int64) bool

	// IsSuspended checks if a task's executor process is suspended.
	IsSuspended(taskID int64) bool

	// ---- Session and Dangerous Mode Support ----
	// These methods enable session tracking and runtime mode switching.
	// Executors that don't support these features should return appropriate defaults.

	// SupportsSessionResume returns true if the executor supports resuming previous sessions.
	// If true, FindSessionID and session-based Resume will work.
	SupportsSessionResume() bool

	// SupportsDangerousMode returns true if the executor has a "dangerous mode" flag
	// that skips permission prompts for autonomous operation.
	SupportsDangerousMode() bool

	// FindSessionID discovers and returns the most recent session ID for the given workDir.
	// Returns empty string if no session is found or sessions aren't supported.
	FindSessionID(workDir string) string

	// ResumeDangerous kills the current process and restarts with dangerous mode enabled.
	// Returns true if successfully restarted. Requires session resume support.
	ResumeDangerous(task *db.Task, workDir string) bool

	// ResumeSafe kills the current process and restarts with dangerous mode disabled.
	// Returns true if successfully restarted. Requires session resume support.
	ResumeSafe(task *db.Task, workDir string) bool
}

// ExecutorFactory manages creation of task executors.
type ExecutorFactory struct {
	executors map[string]TaskExecutor
}

// NewExecutorFactory creates a new executor factory.
func NewExecutorFactory() *ExecutorFactory {
	return &ExecutorFactory{
		executors: make(map[string]TaskExecutor),
	}
}

// Register adds an executor to the factory.
func (f *ExecutorFactory) Register(executor TaskExecutor) {
	f.executors[executor.Name()] = executor
}

// Get returns the executor for the given name, or nil if not found.
func (f *ExecutorFactory) Get(name string) TaskExecutor {
	if name == "" {
		name = db.DefaultExecutor()
	}
	return f.executors[name]
}

// Available returns the names of all registered executors that are available.
func (f *ExecutorFactory) Available() []string {
	var names []string
	for name, exec := range f.executors {
		if exec.IsAvailable() {
			names = append(names, name)
		}
	}
	return names
}

// All returns all registered executor names.
func (f *ExecutorFactory) All() []string {
	names := make([]string, 0, len(f.executors))
	for name := range f.executors {
		names = append(names, name)
	}
	return names
}
