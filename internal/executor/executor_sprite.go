package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/sprites"
	sdk "github.com/superfly/sprites-go"
)

// SpriteRunner manages Claude execution on sprites using native exec sessions.
// Key improvements over tmux-based approach:
// - No tmux dependency on the sprite
// - Uses exec API directly with Env and Dir
// - Streams stdout for real-time logs
// - Uses Filesystem API for file operations
type SpriteRunner struct {
	db     *db.DB
	client *sdk.Client
	sprite *sdk.Sprite

	// Active task tracking
	activeTasks   map[int64]context.CancelFunc
	activeTasksMu sync.RWMutex

	mu     sync.Mutex
	logger func(format string, args ...interface{})
}

// NewSpriteRunner creates a new sprite runner.
func NewSpriteRunner(database *db.DB, logger func(format string, args ...interface{})) (*SpriteRunner, error) {
	ctx := context.Background()

	client, sprite, err := sprites.EnsureRunning(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("ensure sprite running: %w", err)
	}

	runner := &SpriteRunner{
		db:          database,
		client:      client,
		sprite:      sprite,
		activeTasks: make(map[int64]context.CancelFunc),
		logger:      logger,
	}

	// If sprite is nil, we need to create it
	if sprite == nil {
		spriteName := sprites.GetName(database)
		sprite, err = client.CreateSprite(ctx, spriteName, nil)
		if err != nil {
			return nil, fmt.Errorf("create sprite: %w", err)
		}
		runner.sprite = sprite

		// Save sprite name
		database.SetSetting(sprites.SettingName, spriteName)

		logger("Sprite created but may need setup. Run 'task sprite up' to initialize.")
	}

	return runner, nil
}

// RunClaude executes Claude on the sprite for a given task.
// Uses the exec API directly - sessions persist after disconnect.
func (r *SpriteRunner) RunClaude(ctx context.Context, task *db.Task, workDir string, prompt string) error {
	r.logger("Running Claude on sprite for task %d", task.ID)

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

	// Build the Claude command with proper environment
	cmd := r.sprite.CommandContext(taskCtx, "claude",
		"--dangerously-skip-permissions",
		"-p", prompt)

	// Set working directory and environment directly (no shell escaping needed)
	cmd.Dir = workDir
	cmd.Env = []string{
		fmt.Sprintf("TASK_ID=%d", task.ID),
	}

	// Get stdout for streaming logs to database
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	// Handle port notifications (when Claude starts a dev server)
	cmd.TextMessageHandler = func(data []byte) {
		var notification struct {
			Type     string `json:"type"`
			Port     int    `json:"port"`
			ProxyURL string `json:"proxy_url"`
		}
		if err := json.Unmarshal(data, &notification); err == nil {
			if notification.Type == "port_opened" {
				r.db.AppendTaskLog(task.ID, "system",
					fmt.Sprintf("Dev server available at %s", notification.ProxyURL))
				r.logger("Port %d opened, proxy URL: %s", notification.Port, notification.ProxyURL)
			}
		}
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start claude: %w", err)
	}

	r.logger("Claude started on sprite")

	// Stream stdout to task logs in background
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			r.db.AppendTaskLog(task.ID, "claude", line)
			// Parse Claude's output for status changes
			r.parseClaudeOutput(task.ID, line)
		}
	}()

	// Stream stderr to task logs in background
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			r.db.AppendTaskLog(task.ID, "claude_stderr", scanner.Text())
		}
	}()

	// Wait for Claude to exit - this blocks without polling!
	err = cmd.Wait()

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

// SyncWorkdir syncs the local workdir to the sprite using the Filesystem API.
func (r *SpriteRunner) SyncWorkdir(ctx context.Context, localPath string, remotePath string) error {
	// Use Filesystem API to create directory
	fs := r.sprite.Filesystem()
	if err := fs.MkdirAll(remotePath, 0755); err != nil {
		return fmt.Errorf("create remote dir: %w", err)
	}

	r.logger("Created remote directory %s on sprite", remotePath)
	return nil
}

// GetSprite returns the sprite reference.
func (r *SpriteRunner) GetSprite() *sdk.Sprite {
	return r.sprite
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

// StartIdleWatcher periodically creates checkpoints when sprite is idle.
// This saves money by suspending sprites that aren't being used.
func (r *SpriteRunner) StartIdleWatcher(ctx context.Context, idleTimeout time.Duration) {
	go func() {
		lastActivity := time.Now()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.activeTasksMu.RLock()
				hasActiveTasks := len(r.activeTasks) > 0
				r.activeTasksMu.RUnlock()

				if hasActiveTasks {
					lastActivity = time.Now()
				} else if time.Since(lastActivity) > idleTimeout {
					r.logger("Sprite idle for %v, creating checkpoint", idleTimeout)
					if stream, err := r.sprite.CreateCheckpointWithComment(ctx, "auto-idle"); err == nil {
						stream.ProcessAll(func(msg *sdk.StreamMessage) error { return nil })
					}
					lastActivity = time.Now()
				}
			}
		}
	}()
}
