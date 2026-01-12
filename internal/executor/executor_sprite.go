package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/sprites"
	sdk "github.com/superfly/sprites-go"
)

// spriteHookEvent represents a hook event received from a sprite.
type spriteHookEvent struct {
	TaskID int64           `json:"task_id"`
	Event  string          `json:"event"`
	Data   json.RawMessage `json:"data"`
}

// spriteHookData is the data field from Claude's hook input.
type spriteHookData struct {
	SessionID        string `json:"session_id"`
	TranscriptPath   string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type,omitempty"`
	Message          string `json:"message,omitempty"`
	StopReason       string `json:"stop_reason,omitempty"`
}

// SpriteRunner manages Claude execution on sprites.
type SpriteRunner struct {
	db     *db.DB
	client *sdk.Client
	sprite *sdk.Sprite

	// Hook streaming
	hookCtx    context.Context
	hookCancel context.CancelFunc
	hookWg     sync.WaitGroup

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

	// If sprite is nil, we need to create it
	if sprite == nil {
		spriteName := sprites.GetName(database)
		sprite, err = client.CreateSprite(ctx, spriteName, nil)
		if err != nil {
			return nil, fmt.Errorf("create sprite: %w", err)
		}

		// Save sprite name
		database.SetSetting(sprites.SettingName, spriteName)

		// Setup will be done by the caller (setupSprite in cmd/task/sprite.go)
		// For now, we'll just note that it needs setup
		logger("Sprite created but may need setup. Run 'task sprite up' to initialize.")
	}

	return &SpriteRunner{
		db:     database,
		client: client,
		sprite: sprite,
		logger: logger,
	}, nil
}

// StartHookListener starts the background hook event listener.
func (r *SpriteRunner) StartHookListener() {
	r.hookCtx, r.hookCancel = context.WithCancel(context.Background())

	r.hookWg.Add(1)
	go r.streamHooks()
}

// StopHookListener stops the hook listener.
func (r *SpriteRunner) StopHookListener() {
	if r.hookCancel != nil {
		r.hookCancel()
	}
	r.hookWg.Wait()
}

// streamHooks tails the hooks file on the sprite and processes events.
func (r *SpriteRunner) streamHooks() {
	defer r.hookWg.Done()

	for {
		select {
		case <-r.hookCtx.Done():
			return
		default:
		}

		// Start tailing the hooks file
		cmd := r.sprite.CommandContext(r.hookCtx, "tail", "-n", "0", "-f", "/tmp/task-hooks.jsonl")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			r.logger("Failed to get stdout pipe: %v", err)
			time.Sleep(time.Second)
			continue
		}

		if err := cmd.Start(); err != nil {
			r.logger("Failed to start tail: %v", err)
			time.Sleep(time.Second)
			continue
		}

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var event spriteHookEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				r.logger("Failed to parse hook event: %v", err)
				continue
			}

			r.handleHookEvent(&event)
		}

		// If we get here, tail exited - wait and retry
		cmd.Wait()
		select {
		case <-r.hookCtx.Done():
			return
		case <-time.After(time.Second):
			// Retry
		}
	}
}

// handleHookEvent processes a hook event from the sprite.
func (r *SpriteRunner) handleHookEvent(event *spriteHookEvent) {
	// Parse the hook data
	var data spriteHookData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		r.logger("Failed to parse hook data: %v", err)
		return
	}

	// Get current task
	task, err := r.db.GetTask(event.TaskID)
	if err != nil || task == nil {
		return
	}

	// Only manage status if the task has started
	if task.StartedAt == nil {
		return
	}

	switch event.Event {
	case "Stop":
		if data.StopReason == "end_turn" && task.Status == db.StatusProcessing {
			r.db.UpdateTaskStatus(event.TaskID, db.StatusBlocked)
			r.db.AppendTaskLog(event.TaskID, "system", "Waiting for user input")
		}

	case "Notification":
		if (data.NotificationType == "idle_prompt" || data.NotificationType == "permission_prompt") &&
			task.Status == db.StatusProcessing {
			r.db.UpdateTaskStatus(event.TaskID, db.StatusBlocked)
			msg := "Waiting for user input"
			if data.NotificationType == "permission_prompt" {
				msg = "Waiting for permission"
			}
			r.db.AppendTaskLog(event.TaskID, "system", msg)
		}

	case "PreToolUse", "PostToolUse":
		if task.Status == db.StatusBlocked {
			r.db.UpdateTaskStatus(event.TaskID, db.StatusProcessing)
			r.db.AppendTaskLog(event.TaskID, "system", "Claude resumed working")
		}
	}
}

// RunClaude executes Claude on the sprite for a given task.
func (r *SpriteRunner) RunClaude(ctx context.Context, task *db.Task, workDir string, prompt string) error {
	r.logger("Running Claude on sprite for task %d", task.ID)

	// Create a tmux session on the sprite for this task
	sessionName := fmt.Sprintf("task-%d", task.ID)

	// Kill any existing session with this name
	killCmd := r.sprite.CommandContext(ctx, "tmux", "kill-session", "-t", sessionName)
	killCmd.Run() // Ignore errors - session may not exist

	// Build the claude command
	// Set TASK_ID env var so hooks know which task this is
	claudeCmd := fmt.Sprintf(`cd %s && TASK_ID=%d claude --dangerously-skip-permissions -p %q`,
		shellEscape(workDir), task.ID, prompt)

	// Create new tmux session running claude
	cmd := r.sprite.CommandContext(ctx, "tmux", "new-session", "-d", "-s", sessionName, "-c", workDir, "sh", "-c", claudeCmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start claude session: %w: %s", err, string(output))
	}

	r.logger("Claude started in tmux session %s on sprite", sessionName)

	// Wait for the session to complete (poll for session existence)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Check if session still exists
			checkCmd := r.sprite.CommandContext(ctx, "tmux", "has-session", "-t", sessionName)
			if err := checkCmd.Run(); err != nil {
				// Session ended
				r.logger("Claude session %s completed", sessionName)
				return nil
			}
		}
	}
}

// shellEscape escapes a string for use in a shell command.
func shellEscape(s string) string {
	// Simple escape - wrap in single quotes and escape any single quotes
	return "'" + escapeQuotes(s) + "'"
}

func escapeQuotes(s string) string {
	result := ""
	for _, c := range s {
		if c == '\'' {
			result += "'\\''"
		} else {
			result += string(c)
		}
	}
	return result
}

// SyncWorkdir syncs the local workdir to the sprite.
// This copies the task's git worktree to the sprite.
func (r *SpriteRunner) SyncWorkdir(ctx context.Context, localPath string, remotePath string) error {
	// For now, we'll just create the directory on the sprite
	// In the future, we could use rsync or git clone
	cmd := r.sprite.CommandContext(ctx, "mkdir", "-p", remotePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create remote dir: %w: %s", err, string(output))
	}

	// TODO: Implement actual sync (rsync, git clone, or file transfer)
	r.logger("Created remote directory %s on sprite", remotePath)
	return nil
}

// GetSprite returns the sprite reference.
func (r *SpriteRunner) GetSprite() *sdk.Sprite {
	return r.sprite
}
