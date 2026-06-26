// Package events provides a simple hook-based event system for task lifecycle events.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/notify"
)

// Event types for task lifecycle
const (
	TaskCreated       = "task.created"
	TaskUpdated       = "task.updated"
	TaskDeleted       = "task.deleted"
	TaskStarted       = "task.started"
	TaskWorktreeReady = "task.worktree_ready"
	TaskBlocked       = "task.blocked"       // Task needs input from user
	TaskAuthRequired  = "task.auth_required" // Executor session needs re-authentication
	TaskCompleted     = "task.completed"
	TaskFailed        = "task.failed"

	// RoutineFailed fires when a `ty run <routine>` execution fails (non-zero
	// exit, env.sh failure, or timeout). Event.Task is nil; routine name, run
	// ID, exit code, and log path arrive via Metadata.
	RoutineFailed = "routine.failed"
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
	notifier *notify.Notifier
	wg       sync.WaitGroup
}

// New creates a new event emitter.
func New(hooksDir string) *Emitter {
	return &Emitter{hooksDir: hooksDir}
}

// SetNotifier attaches a push notifier so lifecycle events also fan out to the
// user's phone (ntfy/webhook). Safe to pass nil to disable.
func (e *Emitter) SetNotifier(n *notify.Notifier) {
	e.notifier = n
}

// Emit triggers a hook script if it exists for the event type.
// Hooks run in a background goroutine — short-lived CLI commands should
// call Wait before exiting so the hook actually runs.
func (e *Emitter) Emit(event Event) {
	// Nothing to dispatch to: no hook scripts and no push notifier.
	if e.hooksDir == "" && e.notifier == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	// Build the notification synchronously, while the caller's DB handle is
	// guaranteed open, and get back a closure that does only the network send.
	// Short-lived CLI/MCP commands defer db.Close() the instant Run returns —
	// before PersistentPostRun flushes this wait group — so reading settings
	// inside the async goroutine would race the close and silently drop pushes.
	deliver := e.prepareNotify(event)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.runHook(event)
		if deliver != nil {
			_ = deliver()
		}
	}()
}

// prepareNotify maps an event to a notification and asks the notifier to read
// its settings now (synchronously) and return a send closure, or nil if there's
// nothing to send. The returned closure performs only network I/O.
func (e *Emitter) prepareNotify(event Event) func() error {
	if e.notifier == nil {
		return nil
	}
	key := notify.EventKey(event.Type)
	if key == "" {
		return nil
	}
	note := notify.Notification{
		Event:   key,
		TaskID:  event.TaskID,
		Message: event.Message,
	}
	if event.Task != nil {
		note.Title = event.Task.Title
		note.Status = event.Task.Status
		note.Project = event.Task.Project
		// Prefer a distilled summary over a bare status for completed tasks.
		if note.Message == "" && key == "completed" && event.Task.Summary != "" {
			note.Message = event.Task.Summary
		}
	}
	return e.notifier.Prepare(note)
}

// Wait blocks until all in-flight hooks have completed.
// CLI commands that exit after triggering a state change must call this,
// otherwise the process terminates before the hook goroutine runs.
func (e *Emitter) Wait() {
	e.wg.Wait()
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

func (e *Emitter) EmitTaskAuthRequired(task *db.Task, reason string) {
	e.Emit(Event{Type: TaskAuthRequired, TaskID: task.ID, Task: task, Message: reason})
}

func (e *Emitter) EmitTaskCompleted(task *db.Task) {
	e.Emit(Event{Type: TaskCompleted, TaskID: task.ID, Task: task})
}

func (e *Emitter) EmitTaskFailed(task *db.Task, reason string) {
	e.Emit(Event{Type: TaskFailed, TaskID: task.ID, Task: task, Message: reason})
}
