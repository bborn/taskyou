package executor

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/sprites"
)

// SpriteRunner manages Claude execution on sprites using the sprite CLI.
// Uses the sprite CLI directly to leverage existing fly.io authentication.
type SpriteRunner struct {
	db         *db.DB
	spriteName string

	// Active task tracking
	activeTasks   map[int64]context.CancelFunc
	activeTasksMu sync.RWMutex

	logger func(format string, args ...interface{})
}

// NewSpriteRunner creates a new sprite runner.
func NewSpriteRunner(database *db.DB, logger func(format string, args ...interface{})) (*SpriteRunner, error) {
	if !sprites.IsAvailable() {
		return nil, fmt.Errorf("sprite CLI not available or not authenticated. Run 'sprite login' first")
	}

	spriteName := sprites.GetName(database)

	// Check if sprite exists
	spriteList, err := sprites.ListSprites()
	if err != nil {
		return nil, fmt.Errorf("list sprites: %w", err)
	}

	found := false
	for _, s := range spriteList {
		if s == spriteName {
			found = true
			break
		}
	}

	if !found {
		logger("Sprite '%s' not found. Available sprites: %v", spriteName, spriteList)
		logger("Create with 'sprite create %s' or set a different name with 'task sprite use <name>'", spriteName)
		return nil, fmt.Errorf("sprite '%s' not found", spriteName)
	}

	return &SpriteRunner{
		db:          database,
		spriteName:  spriteName,
		activeTasks: make(map[int64]context.CancelFunc),
		logger:      logger,
	}, nil
}

// RunClaude executes Claude on the sprite for a given task.
func (r *SpriteRunner) RunClaude(ctx context.Context, task *db.Task, workDir string, prompt string) error {
	r.logger("Running Claude on sprite '%s' for task %d", r.spriteName, task.ID)

	// Create cancellable context for this task
	taskCtx, cancel := context.WithCancel(ctx)
	r.activeTasksMu.Lock()
	r.activeTasks[task.ID] = cancel
	r.activeTasksMu.Unlock()

	defer func() {
		r.activeTasksMu.Lock()
		delete(r.activeTasks, task.ID)
		r.activeTasksMu.Unlock()
	}()

	// Build the Claude command
	// Note: sprite exec runs commands on the remote sprite
	claudeArgs := []string{
		"claude",
		"--dangerously-skip-permissions",
		"-p", prompt,
	}

	// Log the command we're running
	r.db.AppendTaskLog(task.ID, "system", fmt.Sprintf("Executing on sprite: claude -p '%s...'", truncate(prompt, 50)))

	// Stream output to task logs
	lineHandler := func(line string) {
		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			return
		}

		// Check for cancellation
		select {
		case <-taskCtx.Done():
			return
		default:
		}

		// Log to database
		if strings.HasPrefix(line, "[stderr]") {
			r.db.AppendTaskLog(task.ID, "claude_stderr", strings.TrimPrefix(line, "[stderr] "))
		} else {
			r.db.AppendTaskLog(task.ID, "claude", line)
		}

		// Parse for status changes
		r.parseClaudeOutput(task.ID, line)
	}

	// Execute with streaming
	err := sprites.ExecCommandStreaming(r.spriteName, lineHandler, claudeArgs...)

	if err != nil {
		// Check if it was cancelled
		if taskCtx.Err() != nil {
			r.logger("Claude cancelled for task %d", task.ID)
			return nil
		}
		return fmt.Errorf("claude execution failed: %w", err)
	}

	r.logger("Claude session completed for task %d", task.ID)
	return nil
}

// parseClaudeOutput parses Claude's output for status updates.
func (r *SpriteRunner) parseClaudeOutput(taskID int64, line string) {
	task, err := r.db.GetTask(taskID)
	if err != nil || task == nil || task.StartedAt == nil {
		return
	}

	// Detect when Claude is waiting for input (e.g., permission prompts)
	if strings.Contains(line, "Waiting for") || strings.Contains(line, "Press") {
		if task.Status == db.StatusProcessing {
			r.db.UpdateTaskStatus(taskID, db.StatusBlocked)
		}
	}

	// Detect when Claude resumes work
	if strings.Contains(line, "Running") || strings.Contains(line, "Executing") {
		if task.Status == db.StatusBlocked {
			r.db.UpdateTaskStatus(taskID, db.StatusProcessing)
		}
	}
}

// CancelTask cancels a running task.
func (r *SpriteRunner) CancelTask(taskID int64) bool {
	r.activeTasksMu.RLock()
	cancel, ok := r.activeTasks[taskID]
	r.activeTasksMu.RUnlock()

	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

// IsTaskRunning checks if a task is currently running on the sprite.
func (r *SpriteRunner) IsTaskRunning(taskID int64) bool {
	r.activeTasksMu.RLock()
	defer r.activeTasksMu.RUnlock()
	_, ok := r.activeTasks[taskID]
	return ok
}

// Shutdown gracefully shuts down the sprite runner.
func (r *SpriteRunner) Shutdown() {
	r.activeTasksMu.Lock()
	defer r.activeTasksMu.Unlock()

	// Cancel all running tasks
	for taskID, cancel := range r.activeTasks {
		r.logger("Cancelling task %d at shutdown", taskID)
		if cancel != nil {
			cancel()
		}
	}
}

// ListActiveTasks returns all currently running task IDs.
func (r *SpriteRunner) ListActiveTasks() []int64 {
	r.activeTasksMu.RLock()
	defer r.activeTasksMu.RUnlock()

	result := make([]int64, 0, len(r.activeTasks))
	for taskID := range r.activeTasks {
		result = append(result, taskID)
	}
	return result
}

// GetSpriteName returns the name of the sprite being used.
func (r *SpriteRunner) GetSpriteName() string {
	return r.spriteName
}

// truncate truncates a string to the given length.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
