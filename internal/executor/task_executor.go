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
