package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/sprites"
)

// runClaudeOnSprite runs a task using Claude on a remote sprite.
// This enables dangerous mode safely since the sprite is isolated.
func (e *Executor) runClaudeOnSprite(ctx context.Context, task *db.Task, project *db.Project, workDir, prompt string) execResult {
	e.logLine(task.ID, "system", "Starting task on sprite: "+project.SpriteName)

	// Create sprites client
	spritesClient, err := sprites.NewClient(e.db)
	if err != nil {
		e.logLine(task.ID, "error", "Failed to create sprites client: "+err.Error())
		return execResult{Message: err.Error()}
	}

	spriteName := project.SpriteName

	// Ensure sprite is active (restore from checkpoint if needed)
	e.logLine(task.ID, "system", "Ensuring sprite is active...")
	if err := spritesClient.EnsureActive(ctx, spriteName); err != nil {
		e.logLine(task.ID, "error", "Failed to activate sprite: "+err.Error())
		return execResult{Message: err.Error()}
	}

	// Create worktree on sprite
	taskSlug := fmt.Sprintf("%d-%s", task.ID, slugify(task.Title, 30))
	branchName := fmt.Sprintf("task/%d-%s", task.ID, slugify(task.Title, 30))
	e.logLine(task.ID, "system", "Setting up worktree: "+taskSlug)

	spriteWorkDir, err := spritesClient.SetupWorktree(ctx, spriteName, taskSlug, branchName)
	if err != nil {
		e.logLine(task.ID, "error", "Failed to setup worktree: "+err.Error())
		return execResult{Message: err.Error()}
	}

	// Setup hooks configuration on sprite
	e.logLine(task.ID, "system", "Configuring Claude hooks...")
	if err := spritesClient.SetupHooks(ctx, spriteName, spriteWorkDir, task.ID); err != nil {
		e.logLine(task.ID, "error", "Failed to setup hooks: "+err.Error())
		return execResult{Message: err.Error()}
	}

	// Start hook streaming
	hookEvents, cancelHooks, err := spritesClient.StreamHooks(ctx, spriteName)
	if err != nil {
		e.logLine(task.ID, "error", "Failed to start hook streaming: "+err.Error())
		return execResult{Message: err.Error()}
	}
	defer cancelHooks()

	// Process hook events in background
	go e.processSpriteHooks(ctx, task.ID, hookEvents)

	// Run Claude on sprite (in tmux)
	e.logLine(task.ID, "system", "Starting Claude (dangerous mode enabled)...")
	if err := spritesClient.RunClaude(ctx, spriteName, task.ID, spriteWorkDir, prompt); err != nil {
		e.logLine(task.ID, "error", "Failed to start Claude: "+err.Error())
		return execResult{Message: err.Error()}
	}

	// Poll for completion
	return e.pollSpriteExecution(ctx, spritesClient, spriteName, task.ID)
}

// processSpriteHooks processes hook events from the sprite and updates task status.
func (e *Executor) processSpriteHooks(ctx context.Context, taskID int64, events <-chan sprites.HookEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}

			// Only process events for this task
			if event.TaskID != taskID {
				continue
			}

			switch event.Event {
			case "PreToolUse":
				e.updateStatus(taskID, db.StatusProcessing)
				if event.ToolName != "" {
					e.logLine(taskID, "tool", "Using: "+event.ToolName)
				}

			case "PostToolUse":
				// Keep in processing state
				e.updateStatus(taskID, db.StatusProcessing)

			case "Notification":
				// Check if it's a permission or idle prompt
				if event.Matcher == "idle_prompt" || event.Matcher == "permission_prompt" {
					e.updateStatus(taskID, db.StatusBlocked)
					e.logLine(taskID, "system", "Waiting for input")
				}

			case "Stop":
				// Check stop reason
				if event.Reason == "end_turn" {
					e.updateStatus(taskID, db.StatusBlocked)
					e.logLine(taskID, "system", "Claude stopped, waiting for input")
				}
			}
		}
	}
}

// pollSpriteExecution polls for task completion on the sprite.
func (e *Executor) pollSpriteExecution(ctx context.Context, client *sprites.Client, spriteName string, taskID int64) execResult {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return execResult{Interrupted: true}

		case <-ticker.C:
			// Check database status (set by hooks)
			task, err := e.db.GetTask(taskID)
			if err != nil {
				continue
			}

			// Check if status changed
			switch task.Status {
			case db.StatusBacklog:
				// User interrupted
				return execResult{Interrupted: true}
			case db.StatusDone:
				// Completed via MCP or hooks
				return execResult{Success: true}
			}

			// Check if tmux window still exists
			if !client.TmuxWindowExists(ctx, spriteName, taskID) {
				// Window closed - check final status
				if task.Status == db.StatusDone {
					return execResult{Success: true}
				}
				if task.Status == db.StatusBlocked {
					return execResult{NeedsInput: true, Message: "Task needs input"}
				}
				// Window closed unexpectedly
				return execResult{Message: "Claude process ended"}
			}
		}
	}
}

