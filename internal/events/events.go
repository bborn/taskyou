// Package events provides a simple hook-based event system for task lifecycle events.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// Event types for task lifecycle
const (
	TaskCreated       = "task.created"
	TaskUpdated       = "task.updated"
	TaskDeleted       = "task.deleted"
	TaskStarted       = "task.started"
	TaskWorktreeReady = "task.worktree_ready"
	TaskBlocked       = "task.blocked" // Task needs input from user
	TaskCompleted     = "task.completed"
	TaskFailed        = "task.failed"
)

// Event represents a task lifecycle event.
type Event struct {
	Type      string                 `json:"type"`
	TaskID    int64                  `json:"task_id"`
	Task      *db.Task               `json:"task,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// Emitter handles event emission via hooks.
type Emitter struct {
	hooksDir string
}

// New creates a new event emitter.
func New(hooksDir string) *Emitter {
	return &Emitter{hooksDir: hooksDir}
}

// Emit triggers a hook script if it exists for the event type.
func (e *Emitter) Emit(event Event) {
	if e.hooksDir == "" {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	go e.runHook(event)
}

// runHook executes the hook script for an event.
func (e *Emitter) runHook(event Event) {
	hookPath := filepath.Join(e.hooksDir, event.Type)
	if _, err := os.Stat(hookPath); os.IsNotExist(err) {
		return
	}

	env := os.Environ()
	env = append(env,
		fmt.Sprintf("TASK_ID=%d", event.TaskID),
		fmt.Sprintf("TASK_EVENT=%s", event.Type),
		fmt.Sprintf("TASK_TIMESTAMP=%s", event.Timestamp.Format(time.RFC3339)),
	)

	if event.Task != nil {
		env = append(env,
			fmt.Sprintf("TASK_TITLE=%s", event.Task.Title),
			fmt.Sprintf("TASK_STATUS=%s", event.Task.Status),
			fmt.Sprintf("TASK_PROJECT=%s", event.Task.Project),
		)
		if event.Task.WorktreePath != "" {
			env = append(env,
				fmt.Sprintf("WORKTREE_PATH=%s", event.Task.WorktreePath),
				fmt.Sprintf("WORKTREE_BRANCH=%s", event.Task.BranchName),
				fmt.Sprintf("WORKTREE_PORT=%d", event.Task.Port),
			)
		}
	}

	if len(event.Metadata) > 0 {
		if data, err := json.Marshal(event.Metadata); err == nil {
			env = append(env, fmt.Sprintf("TASK_METADATA=%s", string(data)))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, hookPath)
	cmd.Env = env
	_ = cmd.Run() // Ignore errors - hooks are best-effort
}

// Helper methods for common events

func (e *Emitter) EmitTaskCreated(task *db.Task) {
	e.Emit(Event{Type: TaskCreated, TaskID: task.ID, Task: task})
}

func (e *Emitter) EmitTaskUpdated(task *db.Task, changes map[string]interface{}) {
	e.Emit(Event{Type: TaskUpdated, TaskID: task.ID, Task: task, Metadata: changes})
}

func (e *Emitter) EmitTaskDeleted(taskID int64, title string) {
	e.Emit(Event{Type: TaskDeleted, TaskID: taskID, Message: title})
}

func (e *Emitter) EmitTaskPinned(task *db.Task) {
	e.Emit(Event{Type: TaskUpdated, TaskID: task.ID, Task: task, Metadata: map[string]interface{}{"pinned": true}})
}

func (e *Emitter) EmitTaskUnpinned(task *db.Task) {
	e.Emit(Event{Type: TaskUpdated, TaskID: task.ID, Task: task, Metadata: map[string]interface{}{"pinned": false}})
}

func (e *Emitter) EmitTaskStarted(task *db.Task) {
	e.Emit(Event{Type: TaskStarted, TaskID: task.ID, Task: task})
}

func (e *Emitter) EmitTaskWorktreeReady(task *db.Task) {
	e.Emit(Event{Type: TaskWorktreeReady, TaskID: task.ID, Task: task, Metadata: map[string]interface{}{
		"worktree_path": task.WorktreePath,
		"branch_name":   task.BranchName,
		"port":          task.Port,
	}})
}

func (e *Emitter) EmitTaskBlocked(task *db.Task, reason string) {
	e.Emit(Event{Type: TaskBlocked, TaskID: task.ID, Task: task, Message: reason})
}

func (e *Emitter) EmitTaskCompleted(task *db.Task) {
	e.Emit(Event{Type: TaskCompleted, TaskID: task.ID, Task: task})
}

func (e *Emitter) EmitTaskFailed(task *db.Task, reason string) {
	e.Emit(Event{Type: TaskFailed, TaskID: task.ID, Task: task, Message: reason})
}
