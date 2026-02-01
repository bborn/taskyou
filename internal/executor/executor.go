// Package executor runs Claude Code tasks in the background.
package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/events"
	"github.com/bborn/workflow/internal/github"
	"github.com/bborn/workflow/internal/hooks"
	"github.com/charmbracelet/log"
)

// TaskEvent represents a change to a task.
type TaskEvent struct {
	Type   string   // "created", "updated", "deleted", "status_changed"
	Task   *db.Task // The task (may be nil for deleted)
	TaskID int64    // Always set
}

// Executor manages background task execution.
type Executor struct {
	db      *db.DB
	config  *config.Config
	logger  *log.Logger
	hooks   *hooks.Runner
	events  *events.Emitter
	prCache *github.PRCache

	// Executor factory for pluggable backends
	executorFactory *ExecutorFactory

	mu           sync.RWMutex
	runningTasks map[int64]bool               // tracks which tasks are currently executing
	cancelFuncs  map[int64]context.CancelFunc // cancel functions for running tasks
	running      bool
	stopCh       chan struct{}

	// Suspended task tracking
	suspendedTasks map[int64]time.Time // taskID -> time when suspended

	// Subscribers for real-time log updates (per-task)
	subsMu sync.RWMutex
	subs   map[int64][]chan *db.TaskLog

	// Subscribers for task events (global)
	taskSubsMu sync.RWMutex
	taskSubs   []chan TaskEvent

	// Silent mode suppresses log output (for TUI embedding)
	silent bool

	executorSlug string
	executorName string
}

// DefaultSuspendIdleTimeout is the default time a blocked task must be idle before being suspended.
const DefaultSuspendIdleTimeout = 6 * time.Hour

// DoneTaskCleanupTimeout is how long after a task is marked done before its Claude process is killed.
// This gives users time to review output or retry the task before the process is cleaned up.
const DoneTaskCleanupTimeout = 30 * time.Minute

const (
	defaultExecutorSlug = "claude"
	defaultExecutorName = "Claude"
)

var executorEnvKeys = []string{"TASK_EXECUTOR", "WORKFLOW_EXECUTOR", "TASKYOU_EXECUTOR", "WORKTREE_EXECUTOR"}

// detectExecutorIdentity determines the current executor based on environment variables.
// It falls back to the default Claude executor when no overrides are provided.
func detectExecutorIdentity() (slug, display string) {
	for _, key := range executorEnvKeys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			slug = strings.ToLower(value)
			return slug, formatExecutorDisplayName(slug, value)
		}
	}
	return defaultExecutorSlug, defaultExecutorName
}

func formatExecutorDisplayName(slug, raw string) string {
	switch slug {
	case "codex":
		return "Codex"
	case "claude":
		return defaultExecutorName
	case "gemini":
		return "Gemini"
	case "pi":
		return "Pi"
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultExecutorName
	}
	lower := strings.ToLower(trimmed)
	if trimmed == lower {
		runes := []rune(lower)
		if len(runes) == 0 {
			return defaultExecutorName
		}
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	}
	return trimmed
}

// DefaultExecutorName returns the fallback executor display name.
func DefaultExecutorName() string {
	return defaultExecutorName
}

// New creates a new executor.
func New(database *db.DB, cfg *config.Config) *Executor {
	slug, display := detectExecutorIdentity()
	eventsEmitter := events.New(hooks.DefaultHooksDir())
	e := &Executor{
		db:              database,
		config:          cfg,
		logger:          log.NewWithOptions(io.Discard, log.Options{Prefix: "executor"}),
		hooks:           hooks.NewSilent(hooks.DefaultHooksDir()),
		events:          eventsEmitter,
		prCache:         github.NewPRCache(),
		executorFactory: NewExecutorFactory(),
		stopCh:          make(chan struct{}),
		subs:            make(map[int64][]chan *db.TaskLog),
		taskSubs:        make([]chan TaskEvent, 0),
		runningTasks:    make(map[int64]bool),
		cancelFuncs:     make(map[int64]context.CancelFunc),
		suspendedTasks:  make(map[int64]time.Time),
		silent:          true,
		executorSlug:    slug,
		executorName:    display,
	}

	// Register the events emitter with the database for event emission
	database.SetEventEmitter(eventsEmitter)

	// Register available executors
	e.executorFactory.Register(NewClaudeExecutor(e))
	e.executorFactory.Register(NewCodexExecutor(e))
	e.executorFactory.Register(NewGeminiExecutor(e))
	e.executorFactory.Register(NewOpenClawExecutor(e))
	e.executorFactory.Register(NewOpenCodeExecutor(e))
	e.executorFactory.Register(NewPiExecutor(e))

	return e
}

// NewWithLogging creates an executor that logs to stderr (for daemon mode).
func NewWithLogging(database *db.DB, cfg *config.Config, w io.Writer) *Executor {
	slug, display := detectExecutorIdentity()
	eventsEmitter := events.New(hooks.DefaultHooksDir())
	e := &Executor{
		db:              database,
		config:          cfg,
		logger:          log.NewWithOptions(w, log.Options{Prefix: "executor"}),
		hooks:           hooks.New(hooks.DefaultHooksDir()),
		events:          eventsEmitter,
		prCache:         github.NewPRCache(),
		executorFactory: NewExecutorFactory(),
		stopCh:          make(chan struct{}),
		subs:            make(map[int64][]chan *db.TaskLog),
		taskSubs:        make([]chan TaskEvent, 0),
		runningTasks:    make(map[int64]bool),
		cancelFuncs:     make(map[int64]context.CancelFunc),
		suspendedTasks:  make(map[int64]time.Time),
		silent:          false,
		executorSlug:    slug,
		executorName:    display,
	}

	// Register the events emitter with the database for event emission
	database.SetEventEmitter(eventsEmitter)

	// Register available executors
	e.executorFactory.Register(NewClaudeExecutor(e))
	e.executorFactory.Register(NewCodexExecutor(e))
	e.executorFactory.Register(NewGeminiExecutor(e))
	e.executorFactory.Register(NewOpenClawExecutor(e))
	e.executorFactory.Register(NewOpenCodeExecutor(e))
	e.executorFactory.Register(NewPiExecutor(e))

	return e
}

// DisplayName returns the configured executor display name.
func (e *Executor) DisplayName() string {
	if e == nil || e.executorName == "" {
		return defaultExecutorName
	}
	return e.executorName
}

// ExecutorSlug returns the normalized identifier for the executor (e.g., "codex").
func (e *Executor) ExecutorSlug() string {
	if e == nil || e.executorSlug == "" {
		return defaultExecutorSlug
	}
	return e.executorSlug
}

// Start begins the background worker.
func (e *Executor) Start(ctx context.Context) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.mu.Unlock()

	e.logger.Info("Background executor started")

	go e.worker(ctx)
}

// Stop stops the background worker.
func (e *Executor) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.running = false
	close(e.stopCh)
	e.mu.Unlock()

	e.logger.Info("Background executor stopped")
}

// RunningTasks returns the IDs of currently processing tasks.
func (e *Executor) RunningTasks() []int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ids := make([]int64, 0, len(e.runningTasks))
	for id := range e.runningTasks {
		ids = append(ids, id)
	}
	return ids
}

// IsRunning checks if a specific task is currently executing.
func (e *Executor) IsRunning(taskID int64) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.runningTasks[taskID]
}

// Interrupt cancels a running task.
// If running in this process, cancels directly. Also marks in DB for cross-process interrupt.
func (e *Executor) Interrupt(taskID int64) bool {
	// Get task before interrupting
	task, _ := e.db.GetTask(taskID)

	// Mark as backlog in database (for cross-process communication)
	e.updateStatus(taskID, db.StatusBacklog)
	e.logLine(taskID, "system", "Task interrupted by user")

	// Emit interrupt event
	if task != nil {
		e.events.EmitTaskFailed(task, "interrupted")
	}

	// If running locally, cancel the context
	e.mu.RLock()
	cancel, ok := e.cancelFuncs[taskID]
	e.mu.RUnlock()
	if ok {
		cancel()
	}
	return true
}

// SuspendTask suspends a task's Claude process using SIGTSTP (same as Ctrl+Z) to save memory.
// Returns true if successfully suspended.
func (e *Executor) SuspendTask(taskID int64) bool {
	pid := e.getClaudePID(taskID)
	if pid == 0 {
		return false
	}

	// Send SIGTSTP to suspend the process (same as Ctrl+Z, allows Claude to handle gracefully)
	proc, err := os.FindProcess(pid)
	if err != nil {
		e.logger.Debug("Failed to find process", "pid", pid, "error", err)
		return false
	}

	if err := proc.Signal(syscall.SIGTSTP); err != nil {
		e.logger.Debug("Failed to suspend process", "pid", pid, "error", err)
		return false
	}

	e.mu.Lock()
	e.suspendedTasks[taskID] = time.Now()
	e.mu.Unlock()

	e.logger.Info("Suspended Claude process", "task", taskID, "pid", pid)
	e.logLine(taskID, "system", "Claude suspended (idle timeout)")
	return true
}

// ResumeTask resumes a suspended task's Claude process using SIGCONT.
// Returns true if successfully resumed.
func (e *Executor) ResumeTask(taskID int64) bool {
	e.mu.RLock()
	_, isSuspended := e.suspendedTasks[taskID]
	e.mu.RUnlock()

	if !isSuspended {
		return false
	}

	pid := e.getClaudePID(taskID)
	if pid == 0 {
		// Process gone, clean up suspended state
		e.mu.Lock()
		delete(e.suspendedTasks, taskID)
		e.mu.Unlock()
		return false
	}

	// Send SIGCONT to resume the process
	proc, err := os.FindProcess(pid)
	if err != nil {
		e.mu.Lock()
		delete(e.suspendedTasks, taskID)
		e.mu.Unlock()
		return false
	}

	if err := proc.Signal(syscall.SIGCONT); err != nil {
		e.logger.Debug("Failed to resume process", "pid", pid, "error", err)
		return false
	}

	e.mu.Lock()
	delete(e.suspendedTasks, taskID)
	e.mu.Unlock()

	e.logger.Info("Resumed Claude process", "task", taskID, "pid", pid)
	e.logLine(taskID, "system", "Claude resumed")
	return true
}

// IsSuspended checks if a task is currently suspended.
func (e *Executor) IsSuspended(taskID int64) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, suspended := e.suspendedTasks[taskID]
	return suspended
}

// getClaudePID finds the PID of the Claude process for a task.
// It first checks the stored daemon session, then searches all sessions for the task window.
func (e *Executor) getClaudePID(taskID int64) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	windowName := TmuxWindowName(taskID)

	// Search all tmux sessions for a window with this task's name
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{session_name}:#{window_name}:#{pane_index} #{pane_pid}").Output()
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse "session:window:pane pid"
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		target := parts[0]
		pidStr := parts[1]

		// Only match panes in windows named after this task
		if !strings.Contains(target, windowName) {
			continue
		}

		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Check if this is a Claude process or has Claude as child
		cmdOut, _ := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
		if strings.Contains(string(cmdOut), "claude") {
			return pid
		}

		// Check for claude child process
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "claude").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
			if err == nil {
				return childPid
			}
		}
	}

	return 0
}

// GetClaudePIDFromPane returns the Claude PID for a specific tmux pane.
// This is used by the UI when it knows the exact pane ID.
func GetClaudePIDFromPane(paneID string) int {
	if paneID == "" {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Get the PID of the process in this pane
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-t", paneID, "-p", "#{pane_pid}").Output()
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}

	// Check if this is Claude
	cmdOut, _ := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if strings.Contains(string(cmdOut), "claude") {
		return pid
	}

	// Check for claude child
	childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "claude").Output()
	if err == nil && len(childOut) > 0 {
		childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
		if err == nil {
			return childPid
		}
	}

	return pid // Return the pane PID as fallback
}

// KillClaudeProcess terminates the Claude process for a task to free up memory.
// This is called when a task is completed, closed, or deleted.
// Exported for use by the UI when deleting tasks.
func (e *Executor) KillClaudeProcess(taskID int64) bool {
	pid := e.getClaudePID(taskID)
	if pid == 0 {
		return false
	}

	// Send SIGTERM for graceful shutdown
	proc, err := os.FindProcess(pid)
	if err != nil {
		e.logger.Debug("Failed to find Claude process", "pid", pid, "error", err)
		return false
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		e.logger.Debug("Failed to terminate Claude process", "pid", pid, "error", err)
		return false
	}

	e.logger.Info("Terminated Claude process", "task", taskID, "pid", pid)

	// Clean up suspended task tracking if present
	e.mu.Lock()
	delete(e.suspendedTasks, taskID)
	e.mu.Unlock()

	return true
}

// IsClaudeRunning checks if a Claude process is running for a task.
func (e *Executor) IsClaudeRunning(taskID int64) bool {
	return e.getClaudePID(taskID) != 0
}

// Subscribe to log updates for a task.
func (e *Executor) Subscribe(taskID int64) chan *db.TaskLog {
	ch := make(chan *db.TaskLog, 100)
	e.subsMu.Lock()
	e.subs[taskID] = append(e.subs[taskID], ch)
	e.subsMu.Unlock()
	return ch
}

// Unsubscribe from log updates.
func (e *Executor) Unsubscribe(taskID int64, ch chan *db.TaskLog) {
	e.subsMu.Lock()
	defer e.subsMu.Unlock()

	subs := e.subs[taskID]
	for i, sub := range subs {
		if sub == ch {
			e.subs[taskID] = append(subs[:i], subs[i+1:]...)
			close(ch)
			break
		}
	}
}

func (e *Executor) broadcast(taskID int64, log *db.TaskLog) {
	e.subsMu.RLock()
	defer e.subsMu.RUnlock()

	for _, ch := range e.subs[taskID] {
		select {
		case ch <- log:
		default:
			// Channel full, skip
		}
	}
}

// SubscribeTaskEvents subscribes to task change events (status changes, etc.).
func (e *Executor) SubscribeTaskEvents() chan TaskEvent {
	ch := make(chan TaskEvent, 100)
	e.taskSubsMu.Lock()
	e.taskSubs = append(e.taskSubs, ch)
	e.taskSubsMu.Unlock()
	return ch
}

// UnsubscribeTaskEvents unsubscribes from task events.
func (e *Executor) UnsubscribeTaskEvents(ch chan TaskEvent) {
	e.taskSubsMu.Lock()
	defer e.taskSubsMu.Unlock()

	for i, sub := range e.taskSubs {
		if sub == ch {
			e.taskSubs = append(e.taskSubs[:i], e.taskSubs[i+1:]...)
			close(ch)
			break
		}
	}
}

// broadcastTaskEvent sends a task event to all subscribers.
func (e *Executor) broadcastTaskEvent(event TaskEvent) {
	e.taskSubsMu.RLock()
	defer e.taskSubsMu.RUnlock()

	for _, ch := range e.taskSubs {
		select {
		case ch <- event:
		default:
			// Channel full, skip
		}
	}
}

// NotifyTaskChange notifies subscribers of a task change (for use by UI/other components).
func (e *Executor) NotifyTaskChange(eventType string, task *db.Task) {
	event := TaskEvent{
		Type:   eventType,
		Task:   task,
		TaskID: task.ID,
	}
	e.broadcastTaskEvent(event)
}

// updateStatus updates task status in DB and broadcasts the change.
func (e *Executor) updateStatus(taskID int64, status string) error {
	// Get old status for event
	oldTask, _ := e.db.GetTask(taskID)
	oldStatus := ""
	if oldTask != nil {
		oldStatus = oldTask.Status
	}

	if err := e.db.UpdateTaskStatus(taskID, status); err != nil {
		return err
	}

	// Fetch updated task and broadcast
	task, err := e.db.GetTask(taskID)
	if err == nil && task != nil {
		e.broadcastTaskEvent(TaskEvent{
			Type:   "status_changed",
			Task:   task,
			TaskID: taskID,
		})

		// Emit events based on status
		switch status {
		case db.StatusQueued, db.StatusProcessing:
			e.events.EmitTaskStarted(task)
		case db.StatusDone:
			e.events.EmitTaskCompleted(task)
		default:
			if oldStatus != "" && oldStatus != status {
				e.events.EmitTaskUpdated(task, map[string]interface{}{
					"old_status": oldStatus,
					"new_status": status,
				})
			}
		}
	}
	return nil
}

func (e *Executor) worker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Check merged branches every 30 seconds (15 ticks)
	// Check for idle blocked tasks to suspend every 60 seconds (30 ticks)
	// Check for due scheduled tasks every 10 seconds (5 ticks)
	// Check for inactive done tasks to cleanup every 5 minutes (150 ticks)
	tickCount := 0
	const mergeCheckInterval = 15
	const suspendCheckInterval = 30
	const doneCleanupInterval = 150 // 5 minutes at 2 second ticks

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.processNextTask(ctx)

			tickCount++

			// Periodically check for merged branches
			if tickCount%mergeCheckInterval == 0 {
				e.checkMergedBranches()
			}

			// Periodically check for idle blocked tasks to suspend
			if tickCount%suspendCheckInterval == 0 {
				e.suspendIdleBlockedTasks()
			}

			// Periodically cleanup Claude processes for inactive done tasks
			if tickCount%doneCleanupInterval == 0 {
				e.cleanupInactiveDoneTasks()
			}
		}
	}
}

// suspendIdleBlockedTasks finds blocked tasks that have been idle and suspends their Claude processes.
func (e *Executor) suspendIdleBlockedTasks() {
	tasks, err := e.db.ListTasks(db.ListTasksOptions{Status: db.StatusBlocked, Limit: 100})
	if err != nil {
		return
	}

	for _, task := range tasks {
		// Skip if already suspended
		e.mu.RLock()
		_, alreadySuspended := e.suspendedTasks[task.ID]
		e.mu.RUnlock()
		if alreadySuspended {
			continue
		}

		// Check if task has been blocked for long enough
		// Use UpdatedAt as proxy for when it became blocked
		if task.UpdatedAt.Time.IsZero() {
			continue
		}

		idleDuration := time.Since(task.UpdatedAt.Time)
		if idleDuration >= e.getSuspendIdleTimeout() {
			// Check if there's actually a Claude process to suspend
			pid := e.getClaudePID(task.ID)
			if pid > 0 {
				e.logger.Info("Suspending idle blocked task", "task", task.ID, "idle", idleDuration.Round(time.Second))
				e.SuspendTask(task.ID)
			}
		}
	}
}

// cleanupInactiveDoneTasks kills Claude processes for done tasks that have been inactive
// for longer than DoneTaskCleanupTimeout. This frees up memory from orphaned processes.
func (e *Executor) cleanupInactiveDoneTasks() {
	tasks, err := e.db.ListTasks(db.ListTasksOptions{
		Status:        db.StatusDone,
		IncludeClosed: true,
		Limit:         100,
	})
	if err != nil {
		e.logger.Debug("Failed to list done tasks for cleanup", "error", err)
		return
	}

	for _, task := range tasks {
		// Skip if not completed or completed recently
		if task.CompletedAt == nil {
			continue
		}

		doneDuration := time.Since(task.CompletedAt.Time)
		if doneDuration < DoneTaskCleanupTimeout {
			continue
		}

		// Check if there's a Claude process to kill
		pid := e.getClaudePID(task.ID)
		if pid == 0 {
			continue
		}

		// Kill the Claude process
		e.logger.Info("Cleaning up inactive done task", "task", task.ID, "done_for", doneDuration.Round(time.Minute))
		e.KillClaudeProcess(task.ID)

		// Also kill the tmux window to fully clean up (kill ALL duplicates)
		windowName := TmuxWindowName(task.ID)
		killAllWindowsByNameAllSessions(windowName)

		e.logLine(task.ID, "system", "Claude process cleaned up (inactive done task)")
	}
}

func (e *Executor) processNextTask(ctx context.Context) {
	// Get all queued tasks
	tasks, err := e.db.GetQueuedTasks()
	if err != nil {
		e.logger.Error("Failed to get queued tasks", "error", err)
		return
	}

	for _, task := range tasks {
		// Skip if already running
		e.mu.RLock()
		alreadyRunning := e.runningTasks[task.ID]
		e.mu.RUnlock()
		if alreadyRunning {
			continue
		}

		// Mark as running and spawn goroutine
		e.mu.Lock()
		e.runningTasks[task.ID] = true
		e.mu.Unlock()

		go e.executeTask(ctx, task)
	}
}

// ExecuteNow runs a task immediately (blocking).
func (e *Executor) ExecuteNow(ctx context.Context, taskID int64) error {
	task, err := e.db.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}

	e.executeTask(ctx, task)
	return nil
}

func (e *Executor) executeTask(ctx context.Context, task *db.Task) {
	// Create cancellable context for this task
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Store the cancel function so we can interrupt this task
	e.mu.Lock()
	e.cancelFuncs[task.ID] = cancel
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.runningTasks, task.ID)
		delete(e.cancelFuncs, task.ID)
		e.mu.Unlock()
	}()

	e.logger.Info("Processing task", "id", task.ID, "title", task.Title)

	// Mark task as started
	e.db.MarkTaskStarted(task.ID)

	// Update status to processing
	if err := e.updateStatus(task.ID, db.StatusProcessing); err != nil {
		e.logger.Error("Failed to update status", "error", err)
		return
	}

	// Log start and trigger hook
	startMsg := fmt.Sprintf("Starting task #%d: %s", task.ID, task.Title)
	e.logLine(task.ID, "system", startMsg)
	e.hooks.OnStatusChange(task, db.StatusProcessing, startMsg)

	// Setup worktree for isolated execution (symlinks claude config from project)
	// SECURITY: We must have a valid worktree - never fall back to project directory
	// to prevent Claude from accidentally writing to the main repo
	workDir, err := e.setupWorktree(task)
	if err != nil {
		e.logger.Error("Failed to setup worktree", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to setup worktree: %v", err))
		_ = e.db.UpdateTaskStatus(task.ID, db.StatusBlocked)
		e.hooks.OnStatusChange(task, db.StatusBlocked, "Worktree setup failed - cannot execute task safely")
		return
	}

	// Prepare attachments (write to .claude/attachments for seamless access)
	attachmentPaths, cleanupAttachments := e.prepareAttachments(task.ID, workDir)
	defer cleanupAttachments()
	if len(attachmentPaths) > 0 {
		e.logLine(task.ID, "system", fmt.Sprintf("Task has %d attachment(s)", len(attachmentPaths)))
	}

	// Check if this is a retry (has previous session to resume)
	retryFeedback, _ := e.db.GetRetryFeedback(task.ID)
	isRetry := retryFeedback != ""

	// Build prompt based on task type
	prompt := e.buildPrompt(task, attachmentPaths)

	// Get the appropriate executor for this task
	executorName := task.Executor
	if executorName == "" {
		executorName = db.DefaultExecutor()
	}
	taskExecutor := e.executorFactory.Get(executorName)
	if taskExecutor == nil {
		// Fall back to default executor if specified executor not found
		e.logLine(task.ID, "system", fmt.Sprintf("Executor '%s' not found, falling back to '%s'", executorName, db.DefaultExecutor()))
		taskExecutor = e.executorFactory.Get(db.DefaultExecutor())
	}
	if taskExecutor == nil {
		e.logLine(task.ID, "error", "No executor available")
		e.updateStatus(task.ID, db.StatusBlocked)
		return
	}

	// Check if the executor is available
	if !taskExecutor.IsAvailable() {
		e.logLine(task.ID, "error", fmt.Sprintf("Executor '%s' is not installed", executorName))
		e.updateStatus(task.ID, db.StatusBlocked)
		return
	}

	// Run the executor
	var result execResult
	if isRetry {
		// Include attachments info in retry feedback so Claude knows about them
		// This is important when attachments are added after the initial run or when resuming
		feedbackWithAttachments := retryFeedback
		if len(attachmentPaths) > 0 {
			feedbackWithAttachments = retryFeedback + "\n" + e.getAttachmentsSection(task.ID, attachmentPaths, workDir)
		}
		e.logLine(task.ID, "system", fmt.Sprintf("Resuming previous session with feedback (executor: %s)", executorName))
		execResult := taskExecutor.Resume(taskCtx, task, workDir, prompt, feedbackWithAttachments)
		result = execResult.toInternal()
	} else {
		e.logLine(task.ID, "system", fmt.Sprintf("Starting new session (executor: %s)", executorName))
		execResult := taskExecutor.Execute(taskCtx, task, workDir, prompt)
		result = execResult.toInternal()
	}

	// Check current status - hooks may have already set it
	currentTask, _ := e.db.GetTask(task.ID)
	currentStatus := ""
	if currentTask != nil {
		currentStatus = currentTask.Status
	}

	// Update final status and trigger hooks
	// Respect status set by hooks - don't override blocked with done
	if result.Interrupted {
		// Status already set by Interrupt(), just run hook
		e.hooks.OnStatusChange(task, db.StatusBacklog, "Task interrupted by user")
		// Kill executor process to free memory when task is interrupted
		taskExecutor.Kill(task.ID)
	} else if currentStatus == db.StatusBlocked {
		// Hooks already marked as blocked - respect that
		e.logLine(task.ID, "system", "Task waiting for input")
		e.hooks.OnStatusChange(task, db.StatusBlocked, "Task waiting for input")
	} else if currentStatus == db.StatusDone {
		// Hooks/MCP already marked as done - respect that
		e.logLine(task.ID, "system", "Task completed")
		e.hooks.OnStatusChange(task, db.StatusDone, "Task completed")

		// NOTE: We intentionally do NOT kill the executor here - keep it running so user can
		// easily retry/resume the task. Old done task executors are cleaned up after 2h
		// by the cleanupOrphanedClaudes routine.
	} else if result.Success {
		e.updateStatus(task.ID, db.StatusDone)
		e.logLine(task.ID, "system", "Task completed successfully")
		e.hooks.OnStatusChange(task, db.StatusDone, "Task completed successfully")

		// NOTE: We intentionally do NOT kill the executor here - keep it running so user can
		// easily retry/resume the task. Old done task executors are cleaned up after 2h
		// by the cleanupOrphanedClaudes routine.
	} else if result.NeedsInput {
		e.updateStatus(task.ID, db.StatusBlocked)
		// Log the question with special type so UI can display it
		e.logLine(task.ID, "question", result.Message)
		e.logLine(task.ID, "system", "Task needs input - use 'r' to retry with your answer")
		e.hooks.OnStatusChange(task, db.StatusBlocked, result.Message)
	} else {
		e.updateStatus(task.ID, db.StatusBlocked)
		msg := fmt.Sprintf("Task failed: %s", result.Message)
		e.logLine(task.ID, "error", msg)
		e.hooks.OnStatusChange(task, db.StatusBlocked, msg)
	}

	e.logger.Info("Task finished", "id", task.ID, "success", result.Success)
}

// GetProjectDir returns the directory for a project (exported for UI).
func (e *Executor) GetProjectDir(project string) string {
	return e.config.GetProjectDir(project)
}

// GetExecutor returns the executor for a task by name.
func (e *Executor) GetExecutor(name string) TaskExecutor {
	return e.executorFactory.Get(name)
}

// GetTaskExecutor returns the executor for a specific task.
func (e *Executor) GetTaskExecutor(task *db.Task) TaskExecutor {
	name := task.Executor
	if name == "" {
		name = db.DefaultExecutor()
	}
	return e.executorFactory.Get(name)
}

// AvailableExecutors returns the names of all available executors.
func (e *Executor) AvailableExecutors() []string {
	return e.executorFactory.Available()
}

// AllExecutors returns the names of all registered executors.
func (e *Executor) AllExecutors() []string {
	return e.executorFactory.All()
}

func (e *Executor) getProjectDir(project string) string {
	return e.config.GetProjectDir(project)
}

// getSuspendIdleTimeout returns the configured idle timeout before suspended blocked tasks.
// Falls back to DefaultSuspendIdleTimeout if not configured.
func (e *Executor) getSuspendIdleTimeout() time.Duration {
	if val, err := e.db.GetSetting(config.SettingIdleSuspendTimeout); err == nil && val != "" {
		if duration, err := time.ParseDuration(val); err == nil {
			return duration
		}
	}
	return DefaultSuspendIdleTimeout
}

// getProjectInstructions returns the custom instructions for a project.
func (e *Executor) getProjectInstructions(project string) string {
	if project == "" {
		return ""
	}
	p, err := e.db.GetProjectByName(project)
	if err != nil || p == nil {
		return ""
	}
	return p.Instructions
}

// prepareAttachments writes task attachments to .claude/attachments/ in the worktree.
// This allows Claude to read them without permission prompts since .claude/ is trusted.
// Returns a list of file paths and a cleanup function.
func (e *Executor) prepareAttachments(taskID int64, worktreePath string) ([]string, func()) {
	attachments, err := e.db.ListAttachmentsWithData(taskID)
	if err != nil || len(attachments) == 0 {
		return nil, func() {}
	}

	// Create attachments directory inside .claude/ which Claude has permission to read
	attachmentsDir := filepath.Join(worktreePath, ".claude", "attachments", fmt.Sprintf("task-%d", taskID))
	if err := os.MkdirAll(attachmentsDir, 0755); err != nil {
		e.logger.Error("Failed to create attachments dir", "error", err)
		return nil, func() {}
	}

	var paths []string
	for _, a := range attachments {
		path := filepath.Join(attachmentsDir, a.Filename)
		if err := os.WriteFile(path, a.Data, 0644); err != nil {
			e.logger.Error("Failed to write attachment", "file", a.Filename, "error", err)
			continue
		}
		paths = append(paths, path)
	}

	cleanup := func() {
		os.RemoveAll(attachmentsDir)
	}

	return paths, cleanup
}

// getAttachmentsSection returns a prompt section describing attachments.
// The worktreePath parameter is used to convert absolute paths to relative paths
// so they match the permission pattern Read(.claude/attachments/**).
func (e *Executor) getAttachmentsSection(taskID int64, paths []string, worktreePath string) string {
	if len(paths) == 0 {
		return ""
	}

	var section strings.Builder
	section.WriteString("\n## Attachments\n\n")
	section.WriteString("The following files are attached to this task:\n")
	for _, p := range paths {
		// Convert absolute paths to relative paths so they match permission patterns
		relPath := p
		if worktreePath != "" && strings.HasPrefix(p, worktreePath) {
			relPath = strings.TrimPrefix(p, worktreePath)
			relPath = strings.TrimPrefix(relPath, string(filepath.Separator))
		}
		section.WriteString(fmt.Sprintf("- %s\n", relPath))
	}
	section.WriteString("\nYou can read these files using the Read tool.\n\n")
	return section.String()
}

func (e *Executor) buildPrompt(task *db.Task, attachmentPaths []string) string {
	var prompt strings.Builder

	// Check for on_create action (triage/preprocessing)
	// Only run on first execution, not retries
	if task.StartedAt == nil {
		if onCreateInstructions := e.getOnCreateInstructions(task); onCreateInstructions != "" {
			prompt.WriteString("## Pre-Task Instructions\n\n")
			prompt.WriteString(onCreateInstructions)
			prompt.WriteString("\n\n---\n\n")
		}
	}

	// Similar tasks feature has been removed - always empty
	similarTasks := ""

	// Get project-specific instructions
	projectInstructions := e.getProjectInstructions(task.Project)

	// Check for conversation history (from previous runs/retries)
	conversationHistory := e.getConversationHistory(task.ID)

	// Get attachments section (use relative paths to match permission patterns)
	attachments := e.getAttachmentsSection(task.ID, attachmentPaths, task.WorktreePath)

	// Always include the core task information first - title and body
	prompt.WriteString(fmt.Sprintf("# Task: %s\n\n", task.Title))
	if task.Body != "" {
		prompt.WriteString(fmt.Sprintf("%s\n\n", task.Body))
	}

	// Include task metadata (branch, PR, tags) right after the task description
	taskMeta := e.buildTaskMetadataSection(task)
	if taskMeta != "" {
		prompt.WriteString(taskMeta)
	}

	// Look up task type instructions from database
	if task.Type != "" {
		taskType, err := e.db.GetTaskTypeByName(task.Type)
		if err == nil && taskType != nil {
			// Apply template substitutions for type-specific instructions
			instructions := e.applyTemplateSubstitutions(taskType.Instructions, task, projectInstructions, similarTasks, attachments, conversationHistory)
			prompt.WriteString(instructions)
			prompt.WriteString("\n")
		} else {
			// Fallback to generic context if type not found
			prompt.WriteString(e.buildGenericContextSection(projectInstructions, similarTasks, attachments, conversationHistory))
		}
	} else {
		// No type specified - use generic context
		prompt.WriteString(e.buildGenericContextSection(projectInstructions, similarTasks, attachments, conversationHistory))
	}

	// Note: Task guidance is now passed via system prompt (Claude) or GEMINI.md (Gemini)
	// to keep the user conversation thread clean. See buildSystemInstructions().

	return prompt.String()
}

// buildSystemInstructions returns the system-level instructions that guide task execution.
// These instructions are passed via system prompt mechanisms (e.g., --append-system-prompt for Claude,
// GEMINI.md for Gemini) rather than in the user conversation thread to keep it clean.
func (e *Executor) buildSystemInstructions() string {
	return `═══════════════════════════════════════════════════════════════
                      TASK GUIDANCE
═══════════════════════════════════════════════════════════════

⚡ BEFORE EXPLORING THE CODEBASE:
  Call workflow_get_project_context first via MCP.
  - If it returns context, use it and skip exploration
  - If empty, explore once and save a summary via workflow_set_project_context
  This caches your exploration for future tasks in this project.

Work on this task until completion. When you're done or need input:

✓ WHEN TASK IS COMPLETE:
  Provide a clear summary of what was accomplished

✓ WHEN YOU NEED INPUT/CLARIFICATION:
  Ask your question clearly and wait for a response

✓ FOR VISUAL/FRONTEND WORK:
  Use the workflow_screenshot MCP tool to take screenshots of the
  screen. This helps verify correctness and document changes.

⚠ CRITICAL - WORKING DIRECTORY CONSTRAINT:
  You are running in an isolated git worktree. This worktree IS your
  project - it is NOT a copy. NEVER access the "original" project
  directory or any path outside your current working directory.

  - ONLY use paths within your current working directory
  - NEVER read/write files in /Users/*/Projects/* except this worktree
  - If you see a path like .task-worktrees/, you're in the right place
  - The parent repo does NOT exist for you - only this worktree does

The task system will automatically detect your status.
═══════════════════════════════════════════════════════════════`
}

// applyTemplateSubstitutions replaces template placeholders in task type instructions.
func (e *Executor) applyTemplateSubstitutions(template string, task *db.Task, projectInstructions, similarTasks, attachments, conversationHistory string) string {
	result := template

	// Replace placeholders
	result = strings.ReplaceAll(result, "{{project}}", task.Project)
	result = strings.ReplaceAll(result, "{{title}}", task.Title)
	result = strings.ReplaceAll(result, "{{body}}", task.Body)
	result = strings.ReplaceAll(result, "{{branch}}", task.BranchName)
	result = strings.ReplaceAll(result, "{{tags}}", task.Tags)
	if task.PRURL != "" {
		result = strings.ReplaceAll(result, "{{pr_url}}", task.PRURL)
	} else {
		result = strings.ReplaceAll(result, "{{pr_url}}", "")
	}
	if task.PRNumber > 0 {
		result = strings.ReplaceAll(result, "{{pr_number}}", fmt.Sprintf("%d", task.PRNumber))
	} else {
		result = strings.ReplaceAll(result, "{{pr_number}}", "")
	}
	result = strings.ReplaceAll(result, "{{task_id}}", fmt.Sprintf("%d", task.ID))

	// Include task metadata section for templates that want it
	taskMeta := e.buildTaskMetadataSection(task)
	result = strings.ReplaceAll(result, "{{task_metadata}}", taskMeta)

	// For conditional sections, only include if non-empty
	if projectInstructions != "" {
		result = strings.ReplaceAll(result, "{{project_instructions}}", fmt.Sprintf("## Project Instructions\n\n%s", projectInstructions))
	} else {
		result = strings.ReplaceAll(result, "{{project_instructions}}", "")
	}

	result = strings.ReplaceAll(result, "{{memories}}", "")

	// Similar tasks are injected after memories (no template placeholder for now)
	if similarTasks != "" {
		result = strings.ReplaceAll(result, "{{similar_tasks}}", similarTasks)
	} else {
		result = strings.ReplaceAll(result, "{{similar_tasks}}", "")
	}

	if attachments != "" {
		result = strings.ReplaceAll(result, "{{attachments}}", attachments)
	} else {
		result = strings.ReplaceAll(result, "{{attachments}}", "")
	}

	if conversationHistory != "" {
		result = strings.ReplaceAll(result, "{{history}}", conversationHistory)
	} else {
		result = strings.ReplaceAll(result, "{{history}}", "")
	}

	// Clean up any resulting double blank lines
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}

	return result
}

// buildGenericContextSection builds the context section (project instructions, memories, etc.)
// for tasks without a specific type. The task title and body are added separately in buildPrompt.
func (e *Executor) buildGenericContextSection(projectInstructions, similarTasks, attachments, conversationHistory string) string {
	var prompt strings.Builder

	if projectInstructions != "" {
		prompt.WriteString(fmt.Sprintf("## Project Instructions\n\n%s\n\n", projectInstructions))
	}
	if similarTasks != "" {
		prompt.WriteString(similarTasks)
	}
	if attachments != "" {
		prompt.WriteString(attachments)
	}
	if conversationHistory != "" {
		prompt.WriteString(conversationHistory)
	}
	prompt.WriteString("Complete this task and summarize what you did.\n")

	return prompt.String()
}

// buildTaskMetadataSection creates a section with task metadata (branch, PR, tags).
func (e *Executor) buildTaskMetadataSection(task *db.Task) string {
	var parts []string

	if task.BranchName != "" {
		parts = append(parts, fmt.Sprintf("Branch: %s", task.BranchName))
	}
	if task.PRURL != "" {
		parts = append(parts, fmt.Sprintf("PR: %s", task.PRURL))
	} else if task.PRNumber > 0 {
		parts = append(parts, fmt.Sprintf("PR #%d", task.PRNumber))
	}
	if task.Tags != "" {
		parts = append(parts, fmt.Sprintf("Tags: %s", task.Tags))
	}

	if len(parts) == 0 {
		return ""
	}

	return fmt.Sprintf("## Task Details\n\n%s\n\n", strings.Join(parts, "\n"))
}

// getOnCreateInstructions returns instructions to prepend for new tasks.
// Returns the project's on_create action if set, or default triage instructions
// if the task needs basic triage (missing project/type or very short description).
func (e *Executor) getOnCreateInstructions(task *db.Task) string {
	// Check if project has an on_create action
	if task.Project != "" {
		project, _ := e.db.GetProjectByName(task.Project)
		if project != nil {
			if action := project.GetAction("on_create"); action != nil {
				return action.Instructions
			}
		}
	}

	// Check if task needs default triage (missing info or very short)
	needsDefaultTriage := task.Project == "" || task.Type == "" ||
		(len(task.Body) < 20 && len(task.Title) < 30)

	if needsDefaultTriage {
		return e.getDefaultTriageInstructions(task)
	}

	return ""
}

// getDefaultTriageInstructions returns basic triage instructions for underspecified tasks.
func (e *Executor) getDefaultTriageInstructions(task *db.Task) string {
	var sb strings.Builder

	sb.WriteString("Before starting, please review this task and ask for any clarification needed.\n\n")

	if task.Project == "" {
		projects, _ := e.db.ListProjects()
		if len(projects) > 0 {
			sb.WriteString("Available projects:\n")
			for _, p := range projects {
				sb.WriteString(fmt.Sprintf("- %s (%s)\n", p.Name, p.Path))
			}
			sb.WriteString("\nPlease confirm which project this task is for.\n\n")
		}
	}

	if task.Type == "" {
		// Load task types from database
		taskTypes, _ := e.db.ListTaskTypes()
		if len(taskTypes) > 0 {
			var typeNames []string
			for _, t := range taskTypes {
				typeNames = append(typeNames, t.Name)
			}
			sb.WriteString(fmt.Sprintf("Task types: %s\n", strings.Join(typeNames, ", ")))
		} else {
			sb.WriteString("Task types: code, writing, thinking\n")
		}
		sb.WriteString("Please confirm what type of task this is.\n\n")
	}

	if len(task.Body) < 20 && len(task.Title) < 30 {
		sb.WriteString("The task description is brief. If you need more details to proceed, please ask.\n\n")
	}

	sb.WriteString("Once you have the information you need, proceed with the task.\n")

	return sb.String()
}

type execResult struct {
	Success     bool
	NeedsInput  bool
	Interrupted bool
	Message     string
}

// TmuxDaemonSession is the default session name that holds all Claude task windows.
// This is now deprecated - use getDaemonSessionName() for instance-specific names.
const TmuxDaemonSession = "task-daemon"

// findExistingDaemonSession searches for any existing task-daemon-* session.
// Returns the session name if found, empty string otherwise.
func findExistingDaemonSession() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return ""
	}

	for _, session := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(session, "task-daemon-") {
			return session
		}
	}
	return ""
}

// getDaemonSessionName returns the task-daemon session name for this instance.
// It first checks for an existing session, then falls back to creating a new name.
func getDaemonSessionName() string {
	// First, check for any existing task-daemon-* session
	if existing := findExistingDaemonSession(); existing != "" {
		return existing
	}
	// Check if SESSION_ID is set (for child processes)
	if sid := os.Getenv("WORKTREE_SESSION_ID"); sid != "" {
		return fmt.Sprintf("task-daemon-%s", sid)
	}
	// Generate new session ID based on PID
	return fmt.Sprintf("task-daemon-%d", os.Getpid())
}

// TmuxWindowName returns the window name for a task.
func TmuxWindowName(taskID int64) string {
	return fmt.Sprintf("task-%d", taskID)
}

// TmuxSessionName returns the full tmux target for a task (session:window).
func TmuxSessionName(taskID int64) string {
	return fmt.Sprintf("%s:%s", getDaemonSessionName(), TmuxWindowName(taskID))
}

// killAllWindowsByNameAllSessions kills ALL windows with a given name across all daemon sessions.
// Also kills any -shell variant windows.
func killAllWindowsByNameAllSessions(windowName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// List all windows across all sessions
	out, err := exec.CommandContext(ctx, "tmux", "list-windows",
		"-a", "-F", "#{session_name}:#{window_id}:#{window_name}").Output()
	if err != nil {
		return
	}

	shellWindowName := windowName + "-shell"

	// Kill matching windows BY ID
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		sessionName := parts[0]
		windowID := parts[1]
		name := parts[2]

		// Only kill windows in daemon sessions
		if !strings.HasPrefix(sessionName, "task-daemon-") {
			continue
		}

		// Kill if name matches (including -shell variant)
		if name == windowName || name == shellWindowName {
			exec.CommandContext(ctx, "tmux", "kill-window", "-t", windowID).Run()
		}
	}
}

// getWindowID returns the window ID for a window with the given name in a session.
// Returns the LAST match (most recently created) if multiple windows have the same name.
// Returns empty string if no matching window found.
func getWindowID(session, windowName string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "tmux", "list-windows",
		"-t", session, "-F", "#{window_id}:#{window_name}").Output()
	if err != nil {
		return ""
	}

	// Return LAST match (most recently created)
	var windowID string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && parts[1] == windowName {
			windowID = parts[0]
		}
	}
	return windowID
}

// CleanupDuplicateWindows removes duplicate tmux windows for a task, keeping only the canonical one.
// This prevents window proliferation that can occur from repeated break-pane operations.
func (e *Executor) CleanupDuplicateWindows(taskID int64) {
	windowName := TmuxWindowName(taskID)

	// Get task's canonical window ID from DB
	task, err := e.db.GetTask(taskID)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// List all windows across all sessions
	out, err := exec.CommandContext(ctx, "tmux", "list-windows",
		"-a", "-F", "#{session_name}:#{window_id}:#{window_name}").Output()
	if err != nil {
		return
	}

	var windowsToKill []string
	var canonicalFound bool

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		sessionName := parts[0]
		windowID := parts[1]
		name := parts[2]

		// Only look at daemon sessions
		if !strings.HasPrefix(sessionName, "task-daemon-") {
			continue
		}

		// Check for matching window name (including -shell variant)
		if name != windowName && name != windowName+"-shell" {
			continue
		}

		// Keep canonical window, kill duplicates
		if task.TmuxWindowID != "" && windowID == task.TmuxWindowID {
			canonicalFound = true
			continue // Keep this one
		}

		if task.TmuxWindowID == "" && !canonicalFound {
			// No canonical set - keep first, set it as canonical
			if name == windowName { // Only set canonical for main window, not -shell
				e.db.UpdateTaskWindowID(taskID, windowID)
				canonicalFound = true
				continue
			}
		}

		windowsToKill = append(windowsToKill, windowID)
	}

	// Kill duplicates
	for _, windowID := range windowsToKill {
		e.logger.Debug("Cleaning up duplicate window", "task", taskID, "windowID", windowID)
		exec.CommandContext(ctx, "tmux", "kill-window", "-t", windowID).Run()
	}
}

// GetTasksWithRunningShellProcess returns a map of task IDs that have a running process
// in their shell pane. A process is considered "running" if the shell pane exists and
// has a foreground command that differs from the user's default shell.
func GetTasksWithRunningShellProcess() map[int64]bool {
	result := make(map[int64]bool)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// List all panes across all sessions with their command and window name
	// Format: session:window:pane_index pane_current_command
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{session_name}:#{window_name}:#{pane_index} #{pane_current_command}").Output()
	if err != nil {
		return result
	}

	// Get user's default shell (basename only for comparison)
	userShell := os.Getenv("SHELL")
	if userShell == "" {
		userShell = "/bin/zsh"
	}
	// Extract basename (e.g., "/bin/zsh" -> "zsh")
	if idx := strings.LastIndex(userShell, "/"); idx >= 0 {
		userShell = userShell[idx+1:]
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}

		// Split into "session:window:index" and "command"
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}

		location := parts[0]
		command := parts[1]

		// Parse window name to extract task ID
		// Format: task-daemon-XXX:task-123:1 (pane index 1 is the shell pane)
		locParts := strings.Split(location, ":")
		if len(locParts) < 3 {
			continue
		}

		windowName := locParts[1]
		paneIndex := locParts[2]

		// Only check pane index 1 (the shell pane, not the Claude pane at index 0)
		if paneIndex != "1" {
			continue
		}

		// Extract task ID from window name "task-123"
		if !strings.HasPrefix(windowName, "task-") {
			continue
		}

		var taskID int64
		if _, err := fmt.Sscanf(windowName, "task-%d", &taskID); err != nil {
			continue
		}

		// If the command differs from user's shell, a process is running
		// This catches cases like ./bin/dev (shows as "bash" even if user's shell is zsh)
		if command != userShell {
			result[taskID] = true
		}
	}

	return result
}

// HasRunningProcessInTaskUI checks if the task-ui session has a running process
// in the shell pane (pane index 2). This is used to detect running processes
// for the currently viewed task, whose panes are joined to task-ui rather than
// being in the daemon.
func HasRunningProcessInTaskUI() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Find task-ui session
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{session_name}:#{pane_index} #{pane_current_command}").Output()
	if err != nil {
		return false
	}

	// Get user's default shell (basename only for comparison)
	userShell := os.Getenv("SHELL")
	if userShell == "" {
		userShell = "/bin/zsh"
	}
	// Extract basename (e.g., "/bin/zsh" -> "zsh")
	if idx := strings.LastIndex(userShell, "/"); idx >= 0 {
		userShell = userShell[idx+1:]
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}

		location := parts[0]
		command := parts[1]

		// Look for task-ui session, pane index 2 (the shell pane when viewing a task)
		if strings.HasPrefix(location, "task-ui") && strings.HasSuffix(location, ":2") {
			// If the command differs from user's shell, a process is running
			// This catches cases like ./bin/dev (shows as "bash" even if user's shell is zsh)
			if command != userShell {
				return true
			}
		}
	}

	return false
}

// ensureTmuxDaemon ensures the task-daemon session exists.
// Returns the session name on success for callers that need it.
func ensureTmuxDaemon() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First, check for any existing task-daemon-* session
	if existing := findExistingDaemonSession(); existing != "" {
		return existing, nil
	}

	// No existing session found, create a new one
	daemonSession := getDaemonSessionName()

	// Create it with a placeholder window that stays alive (empty windows exit immediately)
	cmd := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", daemonSession, "-n", "_placeholder", "tail", "-f", "/dev/null")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if it failed because session already exists (race condition with another process)
		if existing := findExistingDaemonSession(); existing != "" {
			return existing, nil
		}
		return "", fmt.Errorf("new-session failed: %v (output: %s)", err, string(output))
	}

	// Verify the session was actually created
	if exec.CommandContext(ctx, "tmux", "has-session", "-t", daemonSession).Run() != nil {
		return "", fmt.Errorf("session %s not found after creation", daemonSession)
	}

	return daemonSession, nil
}

// createTmuxWindow creates a new tmux window in the daemon session with retry logic.
// If the session doesn't exist, it will re-create it and retry once.
// SECURITY: workDir must be within a .task-worktrees directory to prevent Claude from
// accidentally writing to the main project directory.
func createTmuxWindow(daemonSession, windowName, workDir, script string) (string, error) {
	// SECURITY: Validate that workDir is within a .task-worktrees directory
	// This prevents Claude from running in the main project directory
	if !isValidWorktreePath(workDir) {
		return "", fmt.Errorf("security: refusing to create tmux window with workDir outside .task-worktrees: %s", workDir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", "new-window", "-d", "-t", daemonSession, "-n", windowName, "-c", workDir, "sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return daemonSession, nil
	}

	// Check if the error is due to missing session
	outputStr := string(output)
	if strings.Contains(outputStr, "can't find") || strings.Contains(outputStr, "no server running") {
		// Session doesn't exist, try to re-create it
		newSession, createErr := ensureTmuxDaemon()
		if createErr != nil {
			return "", fmt.Errorf("new-window failed: %v (output: %s), and re-create failed: %v", err, outputStr, createErr)
		}

		// Retry with new session
		retryCtx, retryCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer retryCancel()

		retryCmd := exec.CommandContext(retryCtx, "tmux", "new-window", "-d", "-t", newSession, "-n", windowName, "-c", workDir, "sh", "-c", script)
		retryOutput, retryErr := retryCmd.CombinedOutput()
		if retryErr != nil {
			return "", fmt.Errorf("new-window retry failed: %v (output: %s)", retryErr, string(retryOutput))
		}
		return newSession, nil
	}

	return "", fmt.Errorf("new-window failed: %v (output: %s)", err, outputStr)
}

// setupClaudeHooks creates a .claude/settings.local.json in workDir to configure hooks.
// The hooks call back to `task claude-hook` to update task status.
func (e *Executor) setupClaudeHooks(workDir string, taskID int64) (cleanup func(), err error) {
	// Create .claude directory if it doesn't exist
	claudeDir := filepath.Join(workDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return nil, fmt.Errorf("create .claude dir: %w", err)
	}

	settingsPath := filepath.Join(claudeDir, "settings.local.json")

	// Find the task binary path - use absolute path for hooks
	taskBin, err := os.Executable()
	if err != nil {
		// Fall back to PATH lookup
		taskBin, _ = exec.LookPath("task")
		if taskBin == "" {
			taskBin = "task"
		}
	}

	// Configure hooks to call our task binary
	// The WORKTREE_TASK_ID env var is set when launching Claude
	// We use multiple hook types to ensure accurate task state tracking:
	// - PreToolUse: Fires before tool execution - ensures task is "processing"
	// - PostToolUse: Fires after tool completes - ensures task stays "processing"
	// - Notification: Fires when Claude is idle or needs permission - marks task "blocked"
	// - Stop: Fires when Claude finishes responding - marks task "blocked" when waiting for input
	hooksConfig := map[string]interface{}{
		// Pre-approve reading from .claude/attachments/ so Claude can access task attachments
		// without permission prompts (attachments are written there by prepareAttachments)
		"permissions": map[string]interface{}{
			"allow": []string{
				"Read(.claude/attachments/**)",
			},
		},
		"hooks": map[string]interface{}{
			"PreToolUse": []map[string]interface{}{
				{
					"hooks": []map[string]interface{}{
						{
							"type":    "command",
							"command": fmt.Sprintf("%s claude-hook --event PreToolUse", taskBin),
						},
					},
				},
			},
			"PostToolUse": []map[string]interface{}{
				{
					"hooks": []map[string]interface{}{
						{
							"type":    "command",
							"command": fmt.Sprintf("%s claude-hook --event PostToolUse", taskBin),
						},
					},
				},
			},
			"Notification": []map[string]interface{}{
				{
					"matcher": "idle_prompt|permission_prompt",
					"hooks": []map[string]interface{}{
						{
							"type":    "command",
							"command": fmt.Sprintf("%s claude-hook --event Notification", taskBin),
						},
					},
				},
			},
			"Stop": []map[string]interface{}{
				{
					"hooks": []map[string]interface{}{
						{
							"type":    "command",
							"command": fmt.Sprintf("%s claude-hook --event Stop", taskBin),
						},
					},
				},
			},
		},
	}

	// Check if settings.local.json already exists
	existingData, existingErr := os.ReadFile(settingsPath)
	var finalConfig map[string]interface{}

	if existingErr == nil {
		// Merge our hooks and permissions with existing settings
		if json.Unmarshal(existingData, &finalConfig) != nil {
			finalConfig = hooksConfig
		} else {
			// Merge hooks into existing config
			finalConfig["hooks"] = hooksConfig["hooks"]

			// Merge permissions - add our allow rules to existing ones
			if existingPerms, ok := finalConfig["permissions"].(map[string]interface{}); ok {
				if existingAllow, ok := existingPerms["allow"].([]interface{}); ok {
					// Add our permission if not already present
					attachmentPerm := "Read(.claude/attachments/**)"
					found := false
					for _, p := range existingAllow {
						if p == attachmentPerm {
							found = true
							break
						}
					}
					if !found {
						existingPerms["allow"] = append(existingAllow, attachmentPerm)
					}
				} else {
					existingPerms["allow"] = hooksConfig["permissions"].(map[string]interface{})["allow"]
				}
			} else {
				finalConfig["permissions"] = hooksConfig["permissions"]
			}
		}
	} else {
		finalConfig = hooksConfig
	}

	data, err := json.MarshalIndent(finalConfig, "", "  ")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		return nil, err
	}

	cleanup = func() {
		if existingErr == nil {
			// Restore original file
			os.WriteFile(settingsPath, existingData, 0644)
		} else {
			// Remove our file
			os.Remove(settingsPath)
		}
	}

	return cleanup, nil
}

// runClaude runs a task using Claude CLI in a tmux window for interactive access
func (e *Executor) runClaude(ctx context.Context, task *db.Task, workDir, prompt string) execResult {
	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		e.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return execResult{Message: "tmux is not installed"}
	}

	paths := e.claudePathsForProject(task.Project)

	// Ensure task-daemon session exists
	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		e.logger.Error("could not create task-daemon session", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return execResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Kill ALL existing windows with this name (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	// Setup Claude hooks for status updates
	cleanupHooks, err := e.setupClaudeHooks(workDir, task.ID)
	if err != nil {
		e.logger.Warn("could not setup Claude hooks", "error", err)
	}
	// Note: we don't clean up hooks config immediately - it needs to persist for the session

	// Create a temp file for the prompt (avoids quoting issues)
	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		e.logger.Error("could not create temp file", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return execResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}
	promptFile.WriteString(prompt)
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	// Create a temp file for system instructions (passed via --append-system-prompt)
	// This keeps the task guidance out of the user conversation thread
	systemFile, err := os.CreateTemp("", "task-system-*.txt")
	if err != nil {
		e.logger.Error("could not create system file", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create system file: %s", err.Error()))
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return execResult{Message: fmt.Sprintf("failed to create system file: %s", err.Error())}
	}
	systemFile.WriteString(e.buildSystemInstructions())
	systemFile.Close()
	defer os.Remove(systemFile.Name())

	// Script that runs claude interactively with worktree environment variables
	// Note: tmux starts in workDir (-c flag), so claude inherits proper permissions and hooks config
	// Run interactively (no -p) so user can attach and see/interact in real-time
	// Environment variables passed:
	// - WORKTREE_TASK_ID: Task identifier for hooks
	// - WORKTREE_SESSION_ID: Consistent session naming across processes
	// - WORKTREE_PORT: Unique port for running the application
	// - WORKTREE_PATH: Path to the task's git worktree
	sessionID := os.Getenv("WORKTREE_SESSION_ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", os.Getpid())
	}
	// Use --dangerously-skip-permissions if task has dangerous mode enabled or WORKTREE_DANGEROUS_MODE is set
	dangerousFlag := ""
	if task.DangerousMode || os.Getenv("WORKTREE_DANGEROUS_MODE") == "1" {
		dangerousFlag = "--dangerously-skip-permissions "
	}
	// Build system prompt flag - passes task guidance via system prompt to keep conversation clean
	systemPromptFlag := fmt.Sprintf(`--append-system-prompt "$(cat %q)" `, systemFile.Name())

	// Check for existing Claude session to resume instead of starting fresh
	// Only use stored session ID - no file-based fallback to avoid cross-task contamination
	// Validate session file exists before attempting resume
	var script string
	existingSessionID := task.ClaudeSessionID
	envPrefix := claudeEnvPrefix(paths.configDir)
	if existingSessionID != "" && ClaudeSessionExists(existingSessionID, workDir, paths.configDir) {
		e.logLine(task.ID, "system", fmt.Sprintf("Resuming existing session %s", existingSessionID))
		script = fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sclaude %s%s--chrome --resume %s "$(cat %q)"`,
			task.ID, sessionID, task.Port, task.WorktreePath, envPrefix, dangerousFlag, systemPromptFlag, existingSessionID, promptFile.Name())
	} else {
		if existingSessionID != "" {
			e.logLine(task.ID, "system", fmt.Sprintf("Session %s no longer exists, starting fresh", existingSessionID))
			// Clear the stale session ID
			if err := e.db.UpdateTaskClaudeSessionID(task.ID, ""); err != nil {
				e.logger.Warn("failed to clear stale session ID", "task", task.ID, "error", err)
			}
		}
		script = fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sclaude %s%s--chrome "$(cat %q)"`,
			task.ID, sessionID, task.Port, task.WorktreePath, envPrefix, dangerousFlag, systemPromptFlag, promptFile.Name())
	}

	// Create new window in task-daemon session (with retry logic for race conditions)
	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		e.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return execResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	// Update windowTarget if session changed during retry
	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	// Give tmux a moment to fully create the window and start the Claude process
	time.Sleep(200 * time.Millisecond)

	// Save which daemon session owns this task's window (for kill logic)
	if err := e.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}

	// Capture and store the window ID for reliable targeting
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := e.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			e.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	// Ensure shell pane exists alongside Claude pane with environment variables
	e.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)

	// Configure tmux window with helpful status bar
	e.configureTmuxWindow(windowTarget)

	// Poll for output and completion
	result := e.pollTmuxSession(ctx, task.ID, windowTarget)

	// Clean up hooks config after session ends
	if cleanupHooks != nil {
		cleanupHooks()
	}

	return result
}

// runClaudeResume resumes a previous Claude session with feedback.
// If no previous session exists, starts fresh with the full prompt + feedback.
func (e *Executor) runClaudeResume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) execResult {
	paths := e.claudePathsForProject(task.Project)

	// Only use stored session ID - no file-based fallback to avoid cross-task contamination
	// Validate session file exists before attempting resume
	claudeSessionID := task.ClaudeSessionID
	if claudeSessionID == "" || !ClaudeSessionExists(claudeSessionID, workDir, paths.configDir) {
		if claudeSessionID != "" {
			e.logLine(task.ID, "system", fmt.Sprintf("Session %s no longer exists, starting fresh", claudeSessionID))
			// Clear the stale session ID
			if err := e.db.UpdateTaskClaudeSessionID(task.ID, ""); err != nil {
				e.logger.Warn("failed to clear stale session ID", "task", task.ID, "error", err)
			}
		} else {
			e.logLine(task.ID, "system", "No previous session found, starting fresh")
		}
		// Build a combined prompt with the feedback included
		fullPrompt := prompt + "\n\n## User Feedback\n\n" + feedback
		return e.runClaude(ctx, task, workDir, fullPrompt)
	}

	e.logLine(task.ID, "system", fmt.Sprintf("Resuming session %s", claudeSessionID))

	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		e.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return execResult{Message: "tmux is not installed"}
	}

	// Ensure task-daemon session exists
	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		e.logger.Error("could not create task-daemon session", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return execResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Kill ALL existing windows with this name (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	// Setup Claude hooks for status updates
	cleanupHooks, err := e.setupClaudeHooks(workDir, task.ID)
	if err != nil {
		e.logger.Warn("could not setup Claude hooks", "error", err)
	}

	// Create a temp file for the feedback (avoids quoting issues)
	feedbackFile, err := os.CreateTemp("", "task-feedback-*.txt")
	if err != nil {
		e.logger.Error("could not create temp file", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return execResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}
	feedbackFile.WriteString(feedback)
	feedbackFile.Close()
	defer os.Remove(feedbackFile.Name())

	// Create a temp file for system instructions (passed via --append-system-prompt)
	// This keeps the task guidance out of the user conversation thread
	systemFile, err := os.CreateTemp("", "task-system-*.txt")
	if err != nil {
		e.logger.Error("could not create system file", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create system file: %s", err.Error()))
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return execResult{Message: fmt.Sprintf("failed to create system file: %s", err.Error())}
	}
	systemFile.WriteString(e.buildSystemInstructions())
	systemFile.Close()
	defer os.Remove(systemFile.Name())

	// Script that resumes claude with session ID (interactive mode)
	// Environment variables passed:
	// - WORKTREE_TASK_ID: Task identifier for hooks
	// - WORKTREE_SESSION_ID: Consistent session naming across processes
	// - WORKTREE_PORT: Unique port for running the application
	// - WORKTREE_PATH: Path to the task's git worktree
	taskSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if taskSessionID == "" {
		taskSessionID = fmt.Sprintf("%d", os.Getpid())
	}
	// Use --dangerously-skip-permissions if task has dangerous mode enabled or WORKTREE_DANGEROUS_MODE is set
	dangerousFlag := ""
	if task.DangerousMode || os.Getenv("WORKTREE_DANGEROUS_MODE") == "1" {
		dangerousFlag = "--dangerously-skip-permissions "
	}
	// Build system prompt flag - passes task guidance via system prompt to keep conversation clean
	systemPromptFlag := fmt.Sprintf(`--append-system-prompt "$(cat %q)" `, systemFile.Name())

	envPrefix := claudeEnvPrefix(paths.configDir)
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sclaude %s%s--chrome --resume %s "$(cat %q)"`,
		task.ID, taskSessionID, task.Port, task.WorktreePath, envPrefix, dangerousFlag, systemPromptFlag, claudeSessionID, feedbackFile.Name())

	// Create new window in task-daemon session (with retry logic for race conditions)
	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		e.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return execResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	// Update windowTarget if session changed during retry
	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	// Give tmux a moment to fully create the window and start the Claude process
	time.Sleep(200 * time.Millisecond)

	// Save which daemon session owns this task's window (for kill logic)
	if err := e.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}

	// Capture and store the window ID for reliable targeting
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := e.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			e.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	// Ensure shell pane exists alongside Claude pane with environment variables
	e.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)

	// Configure tmux window with helpful status bar
	e.configureTmuxWindow(windowTarget)

	// Poll for output and completion
	result := e.pollTmuxSession(ctx, task.ID, windowTarget)

	// Clean up hooks config after session ends
	if cleanupHooks != nil {
		cleanupHooks()
	}

	return result
}

// ResumeDangerous kills the current executor process and restarts it with dangerous mode enabled.
// This allows switching a running task to dangerous mode without restarting the daemon.
// Returns true if successfully restarted, false otherwise.
// This is executor-aware and will delegate to the appropriate executor's implementation.
func (e *Executor) ResumeDangerous(taskID int64) bool {
	// Get the task to determine which executor to use
	task, err := e.db.GetTask(taskID)
	if err != nil || task == nil {
		e.logger.Error("Failed to get task", "taskID", taskID, "error", err)
		return false
	}

	workDir := task.WorktreePath
	if workDir == "" {
		e.logger.Error("Task has no worktree path", "taskID", taskID)
		return false
	}

	// Get the executor for this task
	exec := e.executorFactory.Get(task.Executor)
	if exec == nil {
		e.logger.Error("Unknown executor", "executor", task.Executor)
		return false
	}

	// Check if this executor supports dangerous mode
	if !exec.SupportsDangerousMode() {
		e.logLine(taskID, "system", fmt.Sprintf("Executor %s does not support dangerous mode", exec.Name()))
		return false
	}

	// Check if this executor supports session resume (required for mode switching)
	if !exec.SupportsSessionResume() {
		e.logLine(taskID, "system", fmt.Sprintf("Executor %s does not support session resume - cannot toggle mode", exec.Name()))
		return false
	}

	// Delegate to the executor's implementation
	return exec.ResumeDangerous(task, workDir)
}

// resumeClaudeDangerous is the Claude-specific implementation of dangerous mode resume.
// It kills the current Claude process and restarts with --dangerously-skip-permissions.
func (e *Executor) resumeClaudeDangerous(task *db.Task, workDir string) bool {
	paths := e.claudePathsForProject(task.Project)
	taskID := task.ID

	claudeSessionID := task.ClaudeSessionID
	if claudeSessionID == "" {
		e.logLine(taskID, "system", "No Claude session found - cannot resume in dangerous mode")
		return false
	}

	// Validate session file exists before attempting resume
	if !ClaudeSessionExists(claudeSessionID, workDir, paths.configDir) {
		e.logLine(taskID, "system", fmt.Sprintf("Session %s no longer exists - cannot resume in dangerous mode", claudeSessionID))
		// Clear the stale session ID
		if err := e.db.UpdateTaskClaudeSessionID(taskID, ""); err != nil {
			e.logger.Warn("failed to clear stale session ID", "task", taskID, "error", err)
		}
		return false
	}

	// Log the action
	e.logLine(taskID, "system", "Restarting Claude with --dangerously-skip-permissions")

	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		e.logLine(taskID, "system", "Tmux not available - cannot resume")
		return false
	}

	windowName := TmuxWindowName(taskID)

	// Kill ALL existing windows with this name across all sessions (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	// Ensure task-daemon session exists for creating new window
	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		e.logger.Warn("could not create task-daemon session", "error", err)
		return false
	}

	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Setup Claude hooks for status updates
	cleanupHooks, err := e.setupClaudeHooks(workDir, taskID)
	if err != nil {
		e.logger.Warn("could not setup Claude hooks", "error", err)
	}

	// Script that resumes claude with session ID in dangerous mode (interactive mode)
	// Environment variables passed:
	// - WORKTREE_TASK_ID: Task identifier for hooks
	// - WORKTREE_SESSION_ID: Consistent session naming across processes
	// - WORKTREE_PORT: Unique port for running the application
	// - WORKTREE_PATH: Path to the task's git worktree
	taskSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if taskSessionID == "" {
		taskSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Force dangerous mode regardless of WORKTREE_DANGEROUS_MODE setting
	envPrefix := claudeEnvPrefix(paths.configDir)
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sclaude --dangerously-skip-permissions --chrome --resume %s`,
		taskID, taskSessionID, task.Port, task.WorktreePath, envPrefix, claudeSessionID)

	// Create new window in task-daemon session (with retry logic for race conditions)
	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		e.logger.Warn("tmux failed to create window", "error", tmuxErr, "session", daemonSession)
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return false
	}

	// Update windowTarget if session changed during retry
	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	// Give tmux a moment to fully create the window and start the Claude process
	time.Sleep(200 * time.Millisecond)

	// Save which daemon session owns this task's window (for kill logic)
	if err := e.db.UpdateTaskDaemonSession(taskID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", taskID, "error", err)
	}

	// Capture and store the window ID for reliable targeting
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := e.db.UpdateTaskWindowID(taskID, windowID); err != nil {
			e.logger.Warn("failed to save window ID", "task", taskID, "error", err)
		}
	}

	// Ensure shell pane exists alongside Claude pane with environment variables
	e.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)

	// Configure tmux window with helpful status bar
	e.configureTmuxWindow(windowTarget)

	// Update the task's dangerous mode flag in the database
	if err := e.db.UpdateTaskDangerousMode(taskID, true); err != nil {
		e.logger.Warn("could not update task dangerous mode", "error", err)
	}

	e.logLine(taskID, "system", "Claude restarted in dangerous mode (--dangerously-skip-permissions enabled)")

	// Don't poll for completion here - the process will continue running in tmux
	// The existing polling infrastructure will handle it
	return true
}

// ResumeSafe kills the current executor process and restarts it with dangerous mode disabled.
// This allows switching a running task back from dangerous mode to safe mode.
// Returns true if successfully restarted, false otherwise.
// This is executor-aware and will delegate to the appropriate executor's implementation.
func (e *Executor) ResumeSafe(taskID int64) bool {
	// Get the task to determine which executor to use
	task, err := e.db.GetTask(taskID)
	if err != nil || task == nil {
		e.logger.Error("Failed to get task", "taskID", taskID, "error", err)
		return false
	}

	workDir := task.WorktreePath
	if workDir == "" {
		e.logger.Error("Task has no worktree path", "taskID", taskID)
		return false
	}

	// Get the executor for this task
	exec := e.executorFactory.Get(task.Executor)
	if exec == nil {
		e.logger.Error("Unknown executor", "executor", task.Executor)
		return false
	}

	// Check if this executor supports dangerous mode
	if !exec.SupportsDangerousMode() {
		e.logLine(taskID, "system", fmt.Sprintf("Executor %s does not support dangerous mode", exec.Name()))
		return false
	}

	// Check if this executor supports session resume (required for mode switching)
	if !exec.SupportsSessionResume() {
		e.logLine(taskID, "system", fmt.Sprintf("Executor %s does not support session resume - cannot toggle mode", exec.Name()))
		return false
	}

	// Delegate to the executor's implementation
	return exec.ResumeSafe(task, workDir)
}

// resumeClaudeSafe is the Claude-specific implementation of safe mode resume.
// It kills the current Claude process and restarts without --dangerously-skip-permissions.
func (e *Executor) resumeClaudeSafe(task *db.Task, workDir string) bool {
	paths := e.claudePathsForProject(task.Project)
	taskID := task.ID

	claudeSessionID := task.ClaudeSessionID
	if claudeSessionID == "" {
		e.logLine(taskID, "system", "No Claude session found - cannot resume in safe mode")
		return false
	}

	// Validate session file exists before attempting resume
	if !ClaudeSessionExists(claudeSessionID, workDir, paths.configDir) {
		e.logLine(taskID, "system", fmt.Sprintf("Session %s no longer exists - cannot resume in safe mode", claudeSessionID))
		// Clear the stale session ID
		if err := e.db.UpdateTaskClaudeSessionID(taskID, ""); err != nil {
			e.logger.Warn("failed to clear stale session ID", "task", taskID, "error", err)
		}
		return false
	}

	// Log the action
	e.logLine(taskID, "system", "Restarting Claude in safe mode (permissions enabled)")

	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		e.logLine(taskID, "system", "Tmux not available - cannot resume")
		return false
	}

	windowName := TmuxWindowName(taskID)

	// Kill ALL existing windows with this name across all sessions (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	// Ensure task-daemon session exists for creating new window
	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		e.logger.Warn("could not create task-daemon session", "error", err)
		return false
	}

	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Setup Claude hooks for status updates
	cleanupHooks, err := e.setupClaudeHooks(workDir, taskID)
	if err != nil {
		e.logger.Warn("could not setup Claude hooks", "error", err)
	}

	// Script that resumes claude with session ID in safe mode (without dangerous flag)
	// Environment variables passed:
	// - WORKTREE_TASK_ID: Task identifier for hooks
	// - WORKTREE_SESSION_ID: Consistent session naming across processes
	// - WORKTREE_PORT: Unique port for running the application
	// - WORKTREE_PATH: Path to the task's git worktree
	taskSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if taskSessionID == "" {
		taskSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Resume without --dangerously-skip-permissions (safe mode)
	envPrefix := claudeEnvPrefix(paths.configDir)
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sclaude --chrome --resume %s`,
		taskID, taskSessionID, task.Port, task.WorktreePath, envPrefix, claudeSessionID)

	// Create new window in task-daemon session (with retry logic for race conditions)
	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		e.logger.Warn("tmux failed to create window", "error", tmuxErr, "session", daemonSession)
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return false
	}

	// Update windowTarget if session changed during retry
	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	// Give tmux a moment to fully create the window and start the Claude process
	time.Sleep(200 * time.Millisecond)

	// Save which daemon session owns this task's window (for kill logic)
	if err := e.db.UpdateTaskDaemonSession(taskID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", taskID, "error", err)
	}

	// Capture and store the window ID for reliable targeting
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := e.db.UpdateTaskWindowID(taskID, windowID); err != nil {
			e.logger.Warn("failed to save window ID", "task", taskID, "error", err)
		}
	}

	// Ensure shell pane exists alongside Claude pane with environment variables
	e.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)

	// Configure tmux window with helpful status bar
	e.configureTmuxWindow(windowTarget)

	// Update the task's dangerous mode flag in the database
	if err := e.db.UpdateTaskDangerousMode(taskID, false); err != nil {
		e.logger.Warn("could not update task dangerous mode", "error", err)
	}

	e.logLine(taskID, "system", "Claude restarted in safe mode (permissions enabled)")

	// Don't poll for completion here - the process will continue running in tmux
	// The existing polling infrastructure will handle it
	return true
}

// resumeCodexWithMode kills the current Codex process and restarts with the specified mode.
// If dangerousMode is true, uses --dangerously-bypass-approvals-and-sandbox.
func (e *Executor) resumeCodexWithMode(task *db.Task, workDir string, dangerousMode bool) bool {
	taskID := task.ID
	paths := e.claudePathsForProject(task.Project)

	sessionID := task.ClaudeSessionID
	if sessionID == "" {
		e.logLine(taskID, "system", "No Codex session found - cannot toggle mode")
		return false
	}

	// Validate session file exists before attempting resume
	if !codexSessionExists(sessionID) {
		e.logLine(taskID, "system", fmt.Sprintf("Session %s no longer exists - cannot toggle mode", sessionID))
		// Clear the stale session ID
		if err := e.db.UpdateTaskClaudeSessionID(taskID, ""); err != nil {
			e.logger.Warn("failed to clear stale session ID", "task", taskID, "error", err)
		}
		return false
	}

	modeStr := "safe"
	if dangerousMode {
		modeStr = "dangerous"
	}
	e.logLine(taskID, "system", fmt.Sprintf("Restarting Codex in %s mode", modeStr))

	if _, err := exec.LookPath("tmux"); err != nil {
		e.logLine(taskID, "system", "Tmux not available - cannot resume")
		return false
	}

	windowName := TmuxWindowName(taskID)
	killAllWindowsByNameAllSessions(windowName)

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		e.logger.Warn("could not create task-daemon session", "error", err)
		return false
	}

	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	taskSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if taskSessionID == "" {
		taskSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Build dangerous flag
	dangerousFlag := ""
	if dangerousMode {
		dangerousFlag = "--dangerously-bypass-approvals-and-sandbox "
	}

	// Build script with --resume flag
	envPrefix := claudeEnvPrefix(paths.configDir)
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %scodex %s--resume %s`,
		taskID, taskSessionID, task.Port, task.WorktreePath, envPrefix, dangerousFlag, sessionID)

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		e.logger.Warn("tmux failed to create window", "error", tmuxErr, "session", daemonSession)
		return false
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := e.db.UpdateTaskDaemonSession(taskID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", taskID, "error", err)
	}

	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := e.db.UpdateTaskWindowID(taskID, windowID); err != nil {
			e.logger.Warn("failed to save window ID", "task", taskID, "error", err)
		}
	}

	e.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)
	e.configureTmuxWindow(windowTarget)

	if err := e.db.UpdateTaskDangerousMode(taskID, dangerousMode); err != nil {
		e.logger.Warn("could not update task dangerous mode", "error", err)
	}

	e.logLine(taskID, "system", fmt.Sprintf("Codex restarted in %s mode", modeStr))
	return true
}

// resumeGeminiWithMode kills the current Gemini process and restarts with the specified mode.
// If dangerousMode is true, uses --dangerously-allow-run.
func (e *Executor) resumeGeminiWithMode(task *db.Task, workDir string, dangerousMode bool) bool {
	taskID := task.ID
	paths := e.claudePathsForProject(task.Project)

	sessionID := task.ClaudeSessionID
	if sessionID == "" {
		e.logLine(taskID, "system", "No Gemini session found - cannot toggle mode")
		return false
	}

	// Validate session file exists before attempting resume
	if !geminiSessionExists(sessionID) {
		e.logLine(taskID, "system", fmt.Sprintf("Session %s no longer exists - cannot toggle mode", sessionID))
		// Clear the stale session ID
		if err := e.db.UpdateTaskClaudeSessionID(taskID, ""); err != nil {
			e.logger.Warn("failed to clear stale session ID", "task", taskID, "error", err)
		}
		return false
	}

	modeStr := "safe"
	if dangerousMode {
		modeStr = "dangerous"
	}
	e.logLine(taskID, "system", fmt.Sprintf("Restarting Gemini in %s mode", modeStr))

	if _, err := exec.LookPath("tmux"); err != nil {
		e.logLine(taskID, "system", "Tmux not available - cannot resume")
		return false
	}

	windowName := TmuxWindowName(taskID)
	killAllWindowsByNameAllSessions(windowName)

	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		e.logger.Warn("could not create task-daemon session", "error", err)
		return false
	}

	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	taskSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if taskSessionID == "" {
		taskSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Build dangerous flag
	dangerousFlag := ""
	if dangerousMode {
		flag := strings.TrimSpace(os.Getenv("GEMINI_DANGEROUS_ARGS"))
		if flag == "" {
			flag = "--dangerously-allow-run"
		}
		dangerousFlag = flag + " "
	}

	// Build script with --resume flag
	envPrefix := claudeEnvPrefix(paths.configDir)
	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q %sgemini %s--resume %s`,
		taskID, taskSessionID, task.Port, task.WorktreePath, envPrefix, dangerousFlag, sessionID)

	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		e.logger.Warn("tmux failed to create window", "error", tmuxErr, "session", daemonSession)
		return false
	}

	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	time.Sleep(200 * time.Millisecond)

	if err := e.db.UpdateTaskDaemonSession(taskID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", taskID, "error", err)
	}

	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := e.db.UpdateTaskWindowID(taskID, windowID); err != nil {
			e.logger.Warn("failed to save window ID", "task", taskID, "error", err)
		}
	}

	e.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, paths.configDir)
	e.configureTmuxWindow(windowTarget)

	if err := e.db.UpdateTaskDangerousMode(taskID, dangerousMode); err != nil {
		e.logger.Warn("could not update task dangerous mode", "error", err)
	}

	e.logLine(taskID, "system", fmt.Sprintf("Gemini restarted in %s mode", modeStr))
	return true
}

// FindClaudeSessionID finds the most recent claude session ID for a workDir using the default config dir.
// Exported for use by the UI to check for resumable sessions.
func FindClaudeSessionID(workDir string) string {
	return findClaudeSessionIDImpl(workDir, DefaultClaudeConfigDir())
}

// findClaudeSessionIDImpl is the shared implementation
func findClaudeSessionIDImpl(workDir, configDir string) string {
	// Claude stores sessions in CLAUDE_CONFIG_DIR/projects/<escaped-path>/
	// The path is escaped: /Users/bruno/foo -> -Users-bruno-foo
	baseDir := ResolveClaudeConfigDir(configDir)

	// Escape the workDir path to match Claude's project directory naming
	// Claude replaces / with - and . with - (keeps leading dash)
	escapedPath := strings.ReplaceAll(workDir, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")

	projectDir := filepath.Join(baseDir, "projects", escapedPath)

	// Find the most recent UUID.jsonl file (not agent-*.jsonl)
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}

	var latestTime time.Time
	var latestSession string

	for _, entry := range entries {
		name := entry.Name()
		// Skip agent files and non-jsonl files
		if strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		// Extract UUID (filename without .jsonl)
		sessionID := strings.TrimSuffix(name, ".jsonl")

		// Check if it looks like a UUID (contains dashes)
		if !strings.Contains(sessionID, "-") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latestSession = sessionID
		}
	}

	return latestSession
}

// ClaudeSessionExists checks if a Claude session file exists for the given session ID and workDir.
func ClaudeSessionExists(sessionID, workDir, configDir string) bool {
	if sessionID == "" {
		return false
	}

	baseDir := ResolveClaudeConfigDir(configDir)
	escapedPath := strings.ReplaceAll(workDir, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
	sessionFile := filepath.Join(baseDir, "projects", escapedPath, sessionID+".jsonl")

	_, err := os.Stat(sessionFile)
	return err == nil
}

// RenameClaudeSession renames the Claude session for a given workDir to the new name.
// It uses Claude's /rename slash command via print mode.
// This is useful when a task title changes and we want the Claude session to reflect it.
func RenameClaudeSession(workDir, newName, configDir string) error {
	resolvedDir := ResolveClaudeConfigDir(configDir)
	sessionID := findClaudeSessionIDImpl(workDir, resolvedDir)
	if sessionID == "" {
		return nil // No session to rename
	}

	// Use claude --resume <session-id> -p "/rename <new-name>" to rename the session
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "--resume", sessionID, "-p", "/rename "+newName)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), fmt.Sprintf("CLAUDE_CONFIG_DIR=%s", resolvedDir))
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Log but don't fail - renaming is a nice-to-have
		return fmt.Errorf("rename session %s: %w (output: %s)", sessionID, err, string(output))
	}

	return nil
}

// RenameClaudeSessionForTask renames the Claude session for a task if it has a worktree.
// This is a convenience method that handles the common case of renaming based on task.
func (e *Executor) RenameClaudeSessionForTask(task *db.Task, newName string) {
	if task == nil || task.WorktreePath == "" {
		return
	}

	paths := e.claudePathsForProject(task.Project)
	if err := RenameClaudeSession(task.WorktreePath, newName, paths.configDir); err != nil {
		e.logger.Debug("Could not rename Claude session", "taskID", task.ID, "error", err)
	}
}

// pollTmuxSession waits for the tmux session to end or task status to change.
// Status is managed entirely by Claude hooks - we just wait and check the result.
// Task only goes to "done" if user/MCP explicitly marks it done.
// NOTE: We intentionally do NOT kill tmux windows here - they're kept around so
// users can review Claude's work. Windows are only killed on task deletion.
func (e *Executor) pollTmuxSession(ctx context.Context, taskID int64, sessionName string) execResult {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Don't kill window - user may want to review what happened
			return execResult{Interrupted: true}

		case <-ticker.C:
			// Check DB status (set by hooks, user, or MCP)
			task, err := e.db.GetTask(taskID)
			if err == nil && task != nil {
				if task.Status == db.StatusBacklog {
					// Don't kill window - keep it for review
					return execResult{Interrupted: true}
				}
				if task.Status == db.StatusDone {
					// Don't kill window - keep it so user can review Claude's work
					return execResult{Success: true}
				}
			}

			// Check if tmux window still exists (with timeout to prevent blocking)
			tmuxCtx, tmuxCancel := context.WithTimeout(context.Background(), 3*time.Second)
			windowExists := exec.CommandContext(tmuxCtx, "tmux", "list-panes", "-t", sessionName).Run() == nil
			tmuxCancel()

			// Also check task-ui (pane might be joined there)
			if !windowExists {
				checkCtx, checkCancel := context.WithTimeout(context.Background(), 3*time.Second)
				checkCmd := exec.CommandContext(checkCtx, "tmux", "list-panes", "-t", "task-ui", "-F", "#{pane_current_command}")
				if out, err := checkCmd.Output(); err == nil {
					if strings.Contains(string(out), "claude") {
						windowExists = true
					}
				}
				checkCancel()
			}

			if !windowExists {
				// Window closed - check final status from hooks
				task, _ := e.db.GetTask(taskID)
				if task != nil {
					if task.Status == db.StatusDone {
						return execResult{Success: true}
					}
					if task.Status == db.StatusBacklog {
						return execResult{Interrupted: true}
					}
				}
				// Default: blocked (user must mark done or retry)
				return execResult{NeedsInput: true, Message: "Task needs review"}
			}
		}
	}
}

// ensureShellPane creates a shell pane alongside the Claude pane in the daemon window.
// This ensures every task always has a persistent shell pane that survives navigation.
// It also sets environment variables (WORKTREE_TASK_ID, WORKTREE_PORT, WORKTREE_PATH) in the shell.
func (e *Executor) ensureShellPane(windowTarget, workDir string, taskID int64, port int, worktreePath string, claudeConfigDir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check if pane .1 already exists by counting panes (shell might already be there from previous session)
	// IMPORTANT: We can't just try to access .1 because tmux returns success even if .1 doesn't exist!
	// It just returns the ID of pane .0 instead. We must check window_panes count.
	countCmd := exec.CommandContext(ctx, "tmux", "display-message", "-t", windowTarget, "-p", "#{window_panes}")
	countOut, err := countCmd.Output()
	if err == nil && strings.TrimSpace(string(countOut)) == "2" {
		// Pane .1 already exists, just ensure it's in the right directory and has env vars set
		exec.CommandContext(ctx, "tmux", "send-keys", "-t", windowTarget+".1", fmt.Sprintf("cd %q", workDir), "Enter").Run()
		// Set environment variables in the existing shell pane
		envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", taskID, port, worktreePath)
		if claudeConfigDir != "" && !isDefaultClaudeConfigDir(claudeConfigDir) {
			envCmd += fmt.Sprintf(" CLAUDE_CONFIG_DIR=%q", claudeConfigDir)
		}
		exec.CommandContext(ctx, "tmux", "send-keys", "-t", windowTarget+".1", envCmd, "Enter").Run()
		exec.CommandContext(ctx, "tmux", "send-keys", "-t", windowTarget+".1", "clear", "Enter").Run()
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".1", "-T", "Shell").Run()
		// Save pane IDs to database for deterministic identification
		e.savePaneIDs(ctx, windowTarget, taskID)
		return
	}

	// Create shell pane to the right of Claude (horizontal split, 50/50)
	// Use user's default shell, fallback to zsh (common on macOS)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	splitCmd := exec.CommandContext(ctx, "tmux", "split-window",
		"-h",                    // horizontal split (side by side)
		"-t", windowTarget+".0", // split from Claude pane
		"-c", workDir, // start in task workdir
		shell, // user's shell to prevent immediate exit
	)
	splitOut, splitErr := splitCmd.CombinedOutput()
	if splitErr != nil {
		e.logger.Warn("failed to create shell pane", "window", windowTarget, "error", splitErr, "output", string(splitOut))
		return
	}

	// Verify the split actually created a second pane
	verifyCmd := exec.CommandContext(ctx, "tmux", "display-message", "-t", windowTarget, "-p", "#{window_panes}")
	verifyOut, _ := verifyCmd.Output()
	if strings.TrimSpace(string(verifyOut)) != "2" {
		e.logger.Warn("split-window did not create a second pane", "windowTarget", windowTarget)
		return
	}

	// Set pane titles
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".0", "-T", "Claude").Run()
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".1", "-T", "Shell").Run()

	// Set environment variables in the shell pane
	// Use export commands so they persist for all commands in the shell
	envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", taskID, port, worktreePath)
	if claudeConfigDir != "" && !isDefaultClaudeConfigDir(claudeConfigDir) {
		envCmd += fmt.Sprintf(" CLAUDE_CONFIG_DIR=%q", claudeConfigDir)
	}
	exec.CommandContext(ctx, "tmux", "send-keys", "-t", windowTarget+".1", envCmd, "Enter").Run()
	// Clear the screen so the export command doesn't clutter the shell
	exec.CommandContext(ctx, "tmux", "send-keys", "-t", windowTarget+".1", "clear", "Enter").Run()

	// Select Claude pane so it's active (user sees Claude output)
	exec.CommandContext(ctx, "tmux", "select-pane", "-t", windowTarget+".0").Run()

	// Save pane IDs to database for deterministic identification
	e.savePaneIDs(ctx, windowTarget, taskID)

	e.logger.Info("created shell pane with env vars", "window", windowTarget, "taskID", taskID, "port", port)
}

// savePaneIDs saves the tmux pane IDs for Claude (.0) and Shell (.1) panes to the database.
// This enables deterministic pane identification when joining/breaking panes.
func (e *Executor) savePaneIDs(ctx context.Context, windowTarget string, taskID int64) {
	// Get Claude pane ID (pane .0)
	claudePaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-t", windowTarget+".0", "-p", "#{pane_id}")
	claudePaneOut, err := claudePaneCmd.Output()
	if err != nil {
		e.logger.Warn("failed to get Claude pane ID", "window", windowTarget, "error", err)
		return
	}
	claudePaneID := strings.TrimSpace(string(claudePaneOut))

	// Get Shell pane ID (pane .1)
	shellPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-t", windowTarget+".1", "-p", "#{pane_id}")
	shellPaneOut, err := shellPaneCmd.Output()
	if err != nil {
		e.logger.Warn("failed to get Shell pane ID", "window", windowTarget, "error", err)
		return
	}
	shellPaneID := strings.TrimSpace(string(shellPaneOut))

	// Save to database
	if err := e.db.UpdateTaskPaneIDs(taskID, claudePaneID, shellPaneID); err != nil {
		e.logger.Warn("failed to save pane IDs", "taskID", taskID, "error", err)
		return
	}

	e.logger.Debug("saved pane IDs", "taskID", taskID, "claudePaneID", claudePaneID, "shellPaneID", shellPaneID)
}

// configureTmuxWindow sets up helpful UI elements for a task window.
func (e *Executor) configureTmuxWindow(windowTarget string) {
	// Window-specific options are limited; most styling is session-wide
	// Just ensure the daemon session has good defaults
	// Use timeout to prevent blocking if tmux is unresponsive
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	daemonSession := getDaemonSessionName()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", daemonSession, "status", "on").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", daemonSession, "status-style", "bg=#f59e0b,fg=black").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", daemonSession, "status-left", " TASK DAEMON ").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", daemonSession, "status-right", " Ctrl+C kills Claude ").Run()
	exec.CommandContext(ctx, "tmux", "set-option", "-t", daemonSession, "status-right-length", "30").Run()
}

func (e *Executor) logLine(taskID int64, lineType, content string) {
	// Store in database
	e.db.AppendTaskLog(taskID, lineType, content)

	// Broadcast to subscribers
	logEntry := &db.TaskLog{
		TaskID:    taskID,
		LineType:  lineType,
		Content:   content,
		CreatedAt: db.LocalTime{Time: time.Now()},
	}
	e.broadcast(taskID, logEntry)
}

// getConversationHistory builds a context section from previous task runs.
// This includes questions asked, user responses, and continuation markers.
func (e *Executor) getConversationHistory(taskID int64) string {
	logs, err := e.db.GetTaskLogs(taskID, 500)
	if err != nil || len(logs) == 0 {
		return ""
	}

	// Look for continuation markers - if none, this is a fresh run
	hasContinuation := false
	for _, log := range logs {
		if log.LineType == "system" && strings.Contains(log.Content, "--- Continuation ---") {
			hasContinuation = true
			break
		}
	}
	if !hasContinuation {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Previous Conversation\n\n")
	sb.WriteString("This task was previously attempted. Here is the relevant history:\n\n")

	// Extract questions and feedback from logs
	for _, log := range logs {
		switch log.LineType {
		case "question":
			sb.WriteString(fmt.Sprintf("**Your question:** %s\n\n", log.Content))
		case "text":
			if strings.HasPrefix(log.Content, "Feedback: ") {
				feedback := strings.TrimPrefix(log.Content, "Feedback: ")
				sb.WriteString(fmt.Sprintf("**User's response:** %s\n\n", feedback))
			}
		case "system":
			if strings.Contains(log.Content, "--- Continuation ---") {
				sb.WriteString("---\n\n")
			}
		}
	}

	sb.WriteString("Please continue with this context in mind.\n\n")
	return sb.String()
}

// CheckPRStateAndUpdateTask checks the PR state for a specific task and updates it if merged.
// This is called reactively when task views are opened/closed.
func (e *Executor) CheckPRStateAndUpdateTask(taskID int64) {
	task, err := e.db.GetTask(taskID)
	if err != nil || task == nil {
		return
	}

	// Only auto-close backlog tasks - if task ever started (queued/processing/blocked),
	// let user decide what to do with it
	if task.Status != db.StatusBacklog || task.BranchName == "" {
		return
	}

	// Skip tasks currently being processed
	e.mu.RLock()
	isRunning := e.runningTasks[task.ID]
	e.mu.RUnlock()
	if isRunning {
		return
	}

	// Check if the branch has been merged (via git or PR status)
	if e.isBranchMerged(task) {
		e.logger.Info("Branch merged, closing task", "id", task.ID, "branch", task.BranchName)
		e.logLine(task.ID, "system", fmt.Sprintf("Branch %s has been merged - automatically closing task", task.BranchName))
		e.updateStatus(task.ID, db.StatusDone)
		e.hooks.OnStatusChange(task, db.StatusDone, "PR merged")
	}
}

// checkMergedBranches checks for tasks whose branches have been merged into the default branch.
// If a task's branch is merged, it automatically closes the task.
// This checks both via GitHub PR status (if available) and git merge detection.
func (e *Executor) checkMergedBranches() {
	// Get all tasks that have branches and aren't done
	tasks, err := e.db.GetTasksWithBranches()
	if err != nil {
		e.logger.Debug("Failed to get tasks with branches", "error", err)
		return
	}

	for _, task := range tasks {
		// Only auto-close backlog tasks - if task ever started, let user decide
		if task.Status != db.StatusBacklog {
			continue
		}

		// Skip tasks currently being processed
		e.mu.RLock()
		isRunning := e.runningTasks[task.ID]
		e.mu.RUnlock()
		if isRunning {
			continue
		}

		// Check if the branch has been merged (via git or PR status)
		if e.isBranchMerged(task) {
			e.logger.Info("Branch merged, closing task", "id", task.ID, "branch", task.BranchName)
			e.logLine(task.ID, "system", fmt.Sprintf("Branch %s has been merged - automatically closing task", task.BranchName))
			e.updateStatus(task.ID, db.StatusDone)
			e.hooks.OnStatusChange(task, db.StatusDone, "PR merged")
		}
	}
}

// isBranchMerged checks if a task's branch has been merged into the default branch.
// First checks GitHub API for PR merge status (most reliable), then falls back to git commands.
// All commands have timeouts to prevent blocking.
func (e *Executor) isBranchMerged(task *db.Task) bool {
	projectDir := e.getProjectDir(task.Project)
	if projectDir == "" {
		return false
	}

	// Check if it's a git repo
	gitDir := filepath.Join(projectDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return false
	}

	// First, check GitHub API for PR merge status (most reliable method)
	// This directly tells us if the PR was merged, regardless of branch deletion
	if e.prCache != nil && task.BranchName != "" {
		prInfo := e.prCache.GetPRForBranch(projectDir, task.BranchName)
		if prInfo != nil && prInfo.State == github.PRStateMerged {
			e.logger.Debug("PR detected as merged via GitHub API", "branch", task.BranchName, "pr", prInfo.Number)
			return true
		}
	}

	// Get the default branch
	defaultBranch := e.getDefaultBranch(projectDir)

	// Timeouts for git operations
	const networkTimeout = 10 * time.Second // For network ops (fetch, ls-remote)
	const localTimeout = 5 * time.Second    // For local ops

	// Fetch from remote to get latest state (with timeout)
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), networkTimeout)
	defer fetchCancel()
	fetchCmd := exec.CommandContext(fetchCtx, "git", "fetch", "--quiet", "origin")
	fetchCmd.Dir = projectDir
	fetchCmd.Run() // Ignore errors - might be offline or timeout

	// Check if the branch has been merged into the default branch
	// Use git branch --merged to see which branches have been merged
	branchCtx, branchCancel := context.WithTimeout(context.Background(), localTimeout)
	defer branchCancel()
	cmd := exec.CommandContext(branchCtx, "git", "branch", "-r", "--merged", defaultBranch)
	cmd.Dir = projectDir
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	// Look for our branch in the merged list
	// Branches appear as "  origin/branch-name" or "* origin/branch-name"
	mergedBranches := strings.Split(string(output), "\n")
	for _, branch := range mergedBranches {
		branch = strings.TrimSpace(branch)
		branch = strings.TrimPrefix(branch, "* ")

		// Check both with and without origin/ prefix
		branchName := task.BranchName
		if strings.Contains(branch, branchName) ||
			strings.HasSuffix(branch, "/"+branchName) ||
			branch == "origin/"+branchName {
			return true
		}
	}

	// Also check if the branch no longer exists on remote (was deleted after merge)
	// This is common when PRs are merged and branches are auto-deleted
	lsCtx, lsCancel := context.WithTimeout(context.Background(), networkTimeout)
	defer lsCancel()
	lsRemoteCmd := exec.CommandContext(lsCtx, "git", "ls-remote", "--heads", "origin", task.BranchName)
	lsRemoteCmd.Dir = projectDir
	lsOutput, err := lsRemoteCmd.Output()
	if err == nil && len(strings.TrimSpace(string(lsOutput))) == 0 {
		// Branch doesn't exist on remote - check if it ever had commits
		// that are now part of the default branch
		logCtx, logCancel := context.WithTimeout(context.Background(), localTimeout)
		defer logCancel()
		logCmd := exec.CommandContext(logCtx, "git", "log", "--oneline", "-1", "origin/"+defaultBranch, "--grep="+task.BranchName)
		logCmd.Dir = projectDir
		logOutput, err := logCmd.Output()
		if err == nil && len(strings.TrimSpace(string(logOutput))) > 0 {
			return true
		}

		// Check if local branch exists
		listCtx, listCancel := context.WithTimeout(context.Background(), localTimeout)
		defer listCancel()
		localLogCmd := exec.CommandContext(listCtx, "git", "branch", "--list", task.BranchName)
		localLogCmd.Dir = projectDir
		localOutput, _ := localLogCmd.Output()
		if len(strings.TrimSpace(string(localOutput))) > 0 {
			// Local branch exists - but we need to be careful not to flag newly created branches
			// A newly created branch from main has no unique commits, so its tip equals merge-base
			// Only consider it merged if:
			// 1. The branch has unique commits (tip != merge-base with default)
			// 2. AND all those commits are now in the default branch

			// Get branch tip commit
			tipCtx, tipCancel := context.WithTimeout(context.Background(), localTimeout)
			defer tipCancel()
			branchTipCmd := exec.CommandContext(tipCtx, "git", "rev-parse", task.BranchName)
			branchTipCmd.Dir = projectDir
			branchTip, err := branchTipCmd.Output()
			if err != nil {
				return false
			}

			// Get merge-base with default branch
			mbCtx, mbCancel := context.WithTimeout(context.Background(), localTimeout)
			defer mbCancel()
			mergeBaseRevCmd := exec.CommandContext(mbCtx, "git", "merge-base", task.BranchName, defaultBranch)
			mergeBaseRevCmd.Dir = projectDir
			mergeBase, err := mergeBaseRevCmd.Output()
			if err != nil {
				return false
			}

			// If branch tip equals merge-base, the branch has no unique commits
			// This means it's a newly created branch, NOT a merged branch
			if strings.TrimSpace(string(branchTip)) == strings.TrimSpace(string(mergeBase)) {
				return false // Newly created branch, not merged
			}

			// Branch has unique commits - check if they're all in default branch now
			// (meaning the branch was merged)
			ancestorCtx, ancestorCancel := context.WithTimeout(context.Background(), localTimeout)
			defer ancestorCancel()
			mergeCheckCmd := exec.CommandContext(ancestorCtx, "git", "merge-base", "--is-ancestor", task.BranchName, defaultBranch)
			mergeCheckCmd.Dir = projectDir
			if mergeCheckCmd.Run() == nil {
				return true
			}
		}
	}

	return false
}

// setupWorktree creates a git worktree for the task if the project is a git repo.
// Returns the working directory to use (worktree path or project path).
func (e *Executor) setupWorktree(task *db.Task) (string, error) {
	// Ensure task has a project (default to 'personal' if empty)
	if task.Project == "" {
		task.Project = "personal"
		e.db.UpdateTask(task)
	}

	paths := e.claudePathsForProject(task.Project)

	// Get project directory
	projectDir := e.getProjectDir(task.Project)

	if projectDir == "" {
		return "", fmt.Errorf("project directory not found for project: %s", task.Project)
	}

	// Check if project is a git repo, initialize one if not
	// Git is required for worktree isolation - tasks always run in worktrees
	// NOTE: This should rarely happen now - git repos are initialized during project creation.
	// If we get here, it's a legacy project created before that change.
	gitDir := filepath.Join(projectDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		// Initialize git repo so we can create worktrees
		e.logger.Warn("Project missing git repo - initializing (legacy project?)", "project", task.Project, "path", projectDir)
		cmd := exec.Command("git", "init")
		cmd.Dir = projectDir
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to initialize git repo: %v\n%s", err, string(output))
		}

		// Create initial commit so we have a branch to create worktrees from
		cmd = exec.Command("git", "add", "-A")
		cmd.Dir = projectDir
		cmd.Run() // Ignore errors - might be empty repo

		cmd = exec.Command("git", "commit", "--allow-empty", "-m", "Initial commit for taskyou worktree support")
		cmd.Dir = projectDir
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to create initial commit: %v\n%s", err, string(output))
		}
	} else {
		// Git repo exists, but check if it has any commits
		// Worktrees require at least one commit to have a base branch
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = projectDir
		if err := cmd.Run(); err != nil {
			// No commits exist - create an initial commit
			e.logger.Warn("Git repo has no commits - creating initial commit", "project", task.Project, "path", projectDir)

			cmd = exec.Command("git", "add", "-A")
			cmd.Dir = projectDir
			cmd.Run() // Ignore errors - might be empty repo

			cmd = exec.Command("git", "commit", "--allow-empty", "-m", "Initial commit for taskyou worktree support")
			cmd.Dir = projectDir
			if output, err := cmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("failed to create initial commit: %v\n%s", err, string(output))
			}
		}
	}

	// If task already has a worktree path, reuse it (don't recalculate from title)
	// This prevents creating duplicate worktrees when a task is renamed
	if task.WorktreePath != "" {
		if _, err := os.Stat(task.WorktreePath); err == nil {
			trustMiseConfig(task.WorktreePath)
			e.writeWorktreeEnvFile(projectDir, task.WorktreePath, task, paths.configDir)
			symlinkClaudeConfig(projectDir, task.WorktreePath)
			symlinkMCPConfig(projectDir, task.WorktreePath)
			copyMCPConfig(paths.configFile, projectDir, task.WorktreePath)
			return task.WorktreePath, nil
		}
		// Worktree path was set but directory doesn't exist, clear it and create fresh
		task.WorktreePath = ""
		task.BranchName = ""
	}

	// Create worktree directory inside the project
	// This allows Claude to inherit the project's MCP config and settings
	worktreesDir := filepath.Join(projectDir, ".task-worktrees")
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return "", fmt.Errorf("create worktrees dir: %w", err)
	}

	// Ensure .task-worktrees is in .gitignore
	e.ensureGitignore(projectDir, ".task-worktrees")

	// Generate slug from title (e.g., "Add contact email" -> "add-contact-email")
	slug := slugify(task.Title, 40)
	branchName := fmt.Sprintf("task/%d-%s", task.ID, slug)
	dirName := fmt.Sprintf("%d-%s", task.ID, slug)
	worktreePath := filepath.Join(worktreesDir, dirName)

	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		// Worktree exists, reuse it
		task.WorktreePath = worktreePath
		task.BranchName = branchName

		// Fetch PR information if available
		e.updateTaskPRInfo(task, projectDir)

		e.db.UpdateTask(task)
		// Allocate a port if not already assigned
		if task.Port == 0 {
			port, err := e.db.AllocatePort(task.ID)
			if err != nil {
				e.logger.Warn("could not allocate port", "error", err)
			} else {
				task.Port = port
			}
		}
		trustMiseConfig(worktreePath)
		e.writeWorktreeEnvFile(projectDir, worktreePath, task, paths.configDir)
		symlinkClaudeConfig(projectDir, worktreePath)
		symlinkMCPConfig(projectDir, worktreePath)
		copyMCPConfig(paths.configFile, projectDir, worktreePath)
		e.runWorktreeInitScript(projectDir, worktreePath, task)
		return worktreePath, nil
	}

	// Get default branch name
	defaultBranch := e.getDefaultBranch(projectDir)

	// Create new branch and worktree
	cmd := exec.Command("git", "worktree", "add", "-b", branchName, worktreePath, defaultBranch)
	cmd.Dir = projectDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if branch already exists
		if strings.Contains(string(output), "already exists") {
			// Try using existing branch
			cmd = exec.Command("git", "worktree", "add", worktreePath, branchName)
			cmd.Dir = projectDir
			output2, err2 := cmd.CombinedOutput()
			if err2 != nil {
				// Check if worktree was created by another process
				if strings.Contains(string(output2), "already checked out") {
					// Worktree exists, reuse it
					task.WorktreePath = worktreePath
					task.BranchName = branchName

					// Fetch PR information if available
					e.updateTaskPRInfo(task, projectDir)

					e.db.UpdateTask(task)
					// Allocate a port if not already assigned
					if task.Port == 0 {
						port, err := e.db.AllocatePort(task.ID)
						if err != nil {
							e.logger.Warn("could not allocate port", "error", err)
						} else {
							task.Port = port
						}
					}
					trustMiseConfig(worktreePath)
					e.writeWorktreeEnvFile(projectDir, worktreePath, task, paths.configDir)
					symlinkClaudeConfig(projectDir, worktreePath)
					symlinkMCPConfig(projectDir, worktreePath)
					copyMCPConfig(paths.configFile, projectDir, worktreePath)
					e.runWorktreeInitScript(projectDir, worktreePath, task)
					return worktreePath, nil
				}
				return "", fmt.Errorf("create worktree: %v\n%s\n%s", err, string(output), string(output2))
			}
		} else {
			return "", fmt.Errorf("create worktree: %v\n%s", err, string(output))
		}
	}

	// Update task with worktree info
	task.WorktreePath = worktreePath
	task.BranchName = branchName

	// Fetch PR information if available
	e.updateTaskPRInfo(task, projectDir)

	e.db.UpdateTask(task)

	e.logLine(task.ID, "system", fmt.Sprintf("Created worktree at %s (branch: %s)", worktreePath, branchName))

	// Allocate a port if not already assigned
	if task.Port == 0 {
		port, err := e.db.AllocatePort(task.ID)
		if err != nil {
			e.logger.Warn("could not allocate port", "error", err)
		} else {
			task.Port = port
			e.logLine(task.ID, "system", fmt.Sprintf("Allocated port %d for application", port))
		}
	}

	trustMiseConfig(worktreePath)
	e.writeWorktreeEnvFile(projectDir, worktreePath, task, paths.configDir)
	symlinkClaudeConfig(projectDir, worktreePath)
	symlinkMCPConfig(projectDir, worktreePath)
	copyMCPConfig(paths.configFile, projectDir, worktreePath)

	// Run worktree init script if configured
	e.runWorktreeInitScript(projectDir, worktreePath, task)

	return worktreePath, nil
}

// getUserShell returns the login shell for the given username.
// On macOS it uses dscl, on Linux it reads /etc/passwd.
func getUserShell(username string) (string, error) {
	// Try macOS dscl first
	if output, err := exec.Command("dscl", ".", "-read", "/Users/"+username, "UserShell").Output(); err == nil {
		line := strings.TrimSpace(string(output))
		if strings.HasPrefix(line, "UserShell: ") {
			return strings.TrimPrefix(line, "UserShell: "), nil
		}
	}

	// Fall back to /etc/passwd for Linux
	file, err := os.Open("/etc/passwd")
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, username+":") {
			fields := strings.Split(line, ":")
			if len(fields) >= 7 {
				return fields[6], nil
			}
		}
	}
	return "", fmt.Errorf("user %s shell not found", username)
}

// writeWorktreeEnvFile creates .envrc with the task's environment variables.
// This is the standard direnv format - users with direnv get auto-loading,
// others can manually run "source .envrc".
// It also adds .envrc to .git/info/exclude so it doesn't pollute git status.
func (e *Executor) writeWorktreeEnvFile(projectDir, worktreePath string, task *db.Task, claudeConfigDir string) error {
	// Write the .envrc file
	envContent := fmt.Sprintf(`export WORKTREE_TASK_ID=%d
export WORKTREE_PORT=%d
export WORKTREE_PATH=%q
`, task.ID, task.Port, worktreePath)
	// Only include CLAUDE_CONFIG_DIR if it's different from the default.
	// Setting it to the default breaks MCP discovery.
	if claudeConfigDir != "" && !isDefaultClaudeConfigDir(claudeConfigDir) {
		envContent += fmt.Sprintf("export CLAUDE_CONFIG_DIR=%q\n", claudeConfigDir)
	}

	envPath := filepath.Join(worktreePath, ".envrc")
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		return fmt.Errorf("write .envrc: %w", err)
	}

	e.logLine(task.ID, "system", "Created .envrc with WORKTREE_TASK_ID, WORKTREE_PORT, WORKTREE_PATH (use direnv or 'source .envrc')")

	// Add .envrc to git exclude so it doesn't show in git status
	ensureGitExclude(projectDir, ".envrc")

	return nil
}

// trustMiseConfig trusts mise config files in a directory (no-op if mise not installed).
func trustMiseConfig(dir string) {
	if _, err := exec.LookPath("mise"); err == nil {
		exec.Command("mise", "trust", dir).Run()
	}
}

type claudePaths struct {
	configDir  string
	configFile string
}

// DefaultClaudeConfigDir returns the resolved CLAUDE_CONFIG_DIR taking the environment into account.
func DefaultClaudeConfigDir() string {
	if env := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); env != "" {
		return filepath.Clean(expandUserPath(env))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// ResolveClaudeConfigDir resolves a custom CLAUDE_CONFIG_DIR override.
// If custom is empty, the default directory (respecting CLAUDE_CONFIG_DIR env) is returned.
func ResolveClaudeConfigDir(custom string) string {
	custom = strings.TrimSpace(custom)
	if custom == "" {
		return DefaultClaudeConfigDir()
	}
	return filepath.Clean(expandUserPath(custom))
}

// ClaudeConfigFilePath returns the path to the claude.json configuration alongside the directory.
func ClaudeConfigFilePath(dir string) string {
	if dir == "" {
		dir = DefaultClaudeConfigDir()
	}
	dir = strings.TrimRight(dir, string(os.PathSeparator))
	return dir + ".json"
}

func expandUserPath(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

func (e *Executor) claudePathsForProject(project string) claudePaths {
	dir := DefaultClaudeConfigDir()
	if project != "" {
		if p, err := e.db.GetProjectByName(project); err == nil && p != nil {
			dir = ResolveClaudeConfigDir(p.ClaudeConfigDir)
		}
	}
	return claudePaths{
		configDir:  dir,
		configFile: ClaudeConfigFilePath(dir),
	}
}

// isDefaultClaudeConfigDir returns true if dir is the default Claude config directory (~/.claude).
// Setting CLAUDE_CONFIG_DIR to the default breaks MCP discovery because Claude then looks for
// config at ~/.claude/.claude.json instead of ~/.claude.json.
func isDefaultClaudeConfigDir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return true
	}
	home, _ := os.UserHomeDir()
	defaultDir := filepath.Join(home, ".claude")
	return dir == defaultDir
}

func claudeEnvPrefix(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || isDefaultClaudeConfigDir(dir) {
		return ""
	}
	return fmt.Sprintf("CLAUDE_CONFIG_DIR=%q ", dir)
}

// ensureGitExclude adds an entry to .git/info/exclude if not already present.
// This prevents generated files (symlinks, .envrc) from showing in git status
// without modifying the shared .gitignore file.
func ensureGitExclude(projectDir, entry string) {
	excludePath := filepath.Join(projectDir, ".git", "info", "exclude")

	// Create the info directory if it doesn't exist
	infoDir := filepath.Dir(excludePath)
	if err := os.MkdirAll(infoDir, 0755); err != nil {
		return
	}

	// Check if entry is already in the exclude file
	excludeContent, err := os.ReadFile(excludePath)
	if err == nil {
		lines := strings.Split(string(excludeContent), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == entry {
				return // Already present
			}
		}
	}

	// Append the exclude entry
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	// Add newline before entry if file doesn't end with one
	if len(excludeContent) > 0 && excludeContent[len(excludeContent)-1] != '\n' {
		f.WriteString("\n")
	}
	f.WriteString(entry + "\n")
}

// symlinkClaudeConfig symlinks the worktree's .claude directory to the main project's .claude.
// This ensures permissions granted in any worktree are shared across all worktrees and the main project.
func symlinkClaudeConfig(projectDir, worktreePath string) error {
	mainClaudeDir := filepath.Join(projectDir, ".claude")
	worktreeClaudeDir := filepath.Join(worktreePath, ".claude")

	// Safety check: prevent circular symlinks if paths are the same
	if mainClaudeDir == worktreeClaudeDir {
		return fmt.Errorf("projectDir and worktreePath must be different (both resolve to %s)", mainClaudeDir)
	}

	// If mainClaudeDir exists as a symlink (possibly broken/circular), remove it
	// The main project's .claude should always be a real directory, never a symlink
	if info, err := os.Lstat(mainClaudeDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		os.RemoveAll(mainClaudeDir)
	}

	// Ensure main project has .claude directory
	if err := os.MkdirAll(mainClaudeDir, 0755); err != nil {
		return fmt.Errorf("create main .claude dir: %w", err)
	}

	// Check if worktree .claude is already a symlink to the right place
	if target, err := os.Readlink(worktreeClaudeDir); err == nil {
		if target == mainClaudeDir {
			return nil // Already correctly symlinked
		}
	}

	// Remove any existing .claude in worktree (file, dir, or wrong symlink)
	os.RemoveAll(worktreeClaudeDir)

	// Create symlink: worktree/.claude -> project/.claude
	if err := os.Symlink(mainClaudeDir, worktreeClaudeDir); err != nil {
		return fmt.Errorf("create .claude symlink: %w", err)
	}

	// Exclude .claude from git so the symlink isn't accidentally committed.
	// The .gitignore entry ".claude/" (with trailing slash) only matches directories, not symlinks.
	// Use projectDir because worktree .git is a file pointing to the main repo's git dir.
	ensureGitExclude(projectDir, ".claude")

	return nil
}

// symlinkMCPConfig symlinks the project's .mcp.json file to the worktree if it exists
// and is not tracked by git. If the file is tracked, the worktree already has it from
// checkout and we shouldn't replace it with a symlink (which would show as a modification).
func symlinkMCPConfig(projectDir, worktreePath string) error {
	mainMCPFile := filepath.Join(projectDir, ".mcp.json")
	worktreeMCPFile := filepath.Join(worktreePath, ".mcp.json")

	// Safety check: prevent circular symlinks if paths are the same
	if mainMCPFile == worktreeMCPFile {
		return nil
	}

	// Check if main project has .mcp.json
	if _, err := os.Stat(mainMCPFile); os.IsNotExist(err) {
		return nil // No .mcp.json in project, nothing to symlink
	}

	// Check if .mcp.json is tracked by git - if so, don't create symlink
	// The worktree already has the file from checkout
	cmd := exec.Command("git", "ls-files", ".mcp.json")
	cmd.Dir = projectDir
	if output, err := cmd.Output(); err == nil && len(output) > 0 {
		return nil // File is tracked by git, don't replace with symlink
	}

	// Check if worktree .mcp.json is already a symlink to the right place
	if target, err := os.Readlink(worktreeMCPFile); err == nil {
		if target == mainMCPFile {
			return nil // Already correctly symlinked
		}
	}

	// Remove any existing .mcp.json in worktree (file or wrong symlink)
	os.Remove(worktreeMCPFile)

	// Create symlink: worktree/.mcp.json -> project/.mcp.json
	if err := os.Symlink(mainMCPFile, worktreeMCPFile); err != nil {
		return fmt.Errorf("create .mcp.json symlink: %w", err)
	}

	// Exclude .mcp.json from git so the symlink isn't accidentally committed.
	// Use projectDir because worktree .git is a file pointing to the main repo's git dir.
	ensureGitExclude(projectDir, ".mcp.json")

	return nil
}

// copyMCPConfig copies the MCP server configuration from the source project to the worktree
// in the claude.json file so that Claude Code in the worktree has the same MCP servers available.
func copyMCPConfig(configPath, srcDir, dstDir string) error {
	if configPath == "" {
		configPath = ClaudeConfigFilePath("")
	}

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No config file, nothing to copy
		}
		return fmt.Errorf("read claude config: %w", err)
	}

	// Parse as generic JSON to preserve all fields
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse claude config: %w", err)
	}

	// Get projects map
	projectsRaw, ok := config["projects"]
	if !ok {
		return nil // No projects configured
	}
	projects, ok := projectsRaw.(map[string]interface{})
	if !ok {
		return nil // Invalid projects format
	}

	// Get source project config
	srcConfigRaw, ok := projects[srcDir]
	if !ok {
		return nil // Source project not configured
	}
	srcConfig, ok := srcConfigRaw.(map[string]interface{})
	if !ok {
		return nil // Invalid source config format
	}

	// Get MCP servers from source
	mcpServersRaw, ok := srcConfig["mcpServers"]
	if !ok {
		return nil // No MCP servers configured
	}

	// Get or create destination project config
	dstConfigRaw, ok := projects[dstDir]
	var dstConfig map[string]interface{}
	if ok {
		dstConfig, _ = dstConfigRaw.(map[string]interface{})
	}
	if dstConfig == nil {
		dstConfig = make(map[string]interface{})
	}

	// Copy MCP servers to destination
	dstConfig["mcpServers"] = mcpServersRaw

	// Also copy hasTrustDialogAccepted if present
	if trusted, ok := srcConfig["hasTrustDialogAccepted"]; ok {
		dstConfig["hasTrustDialogAccepted"] = trusted
	}

	// Update projects map
	projects[dstDir] = dstConfig
	config["projects"] = projects

	// Write back config
	newData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal claude config: %w", err)
	}

	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		return fmt.Errorf("write claude config: %w", err)
	}

	return nil
}

// runWorktreeInitScript runs the worktree init script if configured or conventionally present.
// It sets environment variables WORKTREE_TASK_ID, WORKTREE_PORT, and WORKTREE_PATH.
// Non-zero exit codes are logged as warnings but do not cause the worktree setup to fail.
// Output is streamed line-by-line in real-time to provide feedback during long-running scripts.
func (e *Executor) runWorktreeInitScript(projectDir, worktreePath string, task *db.Task) {
	// Look for the init script in the worktree (not projectDir) so that __dir__ resolves correctly in scripts
	scriptPath := GetWorktreeInitScript(worktreePath)
	if scriptPath == "" {
		return
	}

	e.logLine(task.ID, "system", fmt.Sprintf("Running worktree init script: %s", scriptPath))

	// Run through user's login interactive shell so that shell init (mise, nvm, etc.) is sourced
	shell := "bash" // default fallback
	if currentUser, err := user.Current(); err == nil {
		if userShell, err := getUserShell(currentUser.Username); err == nil && userShell != "" {
			shell = userShell
		}
	}
	cmd := exec.Command(shell, "-l", "-i", "-c", scriptPath)
	cmd.Dir = worktreePath

	// Set environment variables as specified in the feature request
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("WORKTREE_TASK_ID=%d", task.ID),
		fmt.Sprintf("WORKTREE_PORT=%d", task.Port),
		fmt.Sprintf("WORKTREE_PATH=%s", worktreePath),
	)

	// Set up pipes for streaming output in real-time
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		e.logger.Warn("failed to create stdout pipe for worktree init script", "error", err)
		e.logLine(task.ID, "system", fmt.Sprintf("Warning: failed to set up script output: %v", err))
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		e.logger.Warn("failed to create stderr pipe for worktree init script", "error", err)
		e.logLine(task.ID, "system", fmt.Sprintf("Warning: failed to set up script output: %v", err))
		return
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		e.logger.Warn("failed to start worktree init script", "script", scriptPath, "error", err)
		e.logLine(task.ID, "system", fmt.Sprintf("Warning: failed to start worktree init script: %v", err))
		return
	}

	// Stream output from both stdout and stderr concurrently
	var wg sync.WaitGroup
	wg.Add(2)

	// Stream stdout
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			e.logLine(task.ID, "system", fmt.Sprintf("[init] %s", line))
		}
	}()

	// Stream stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			e.logLine(task.ID, "system", fmt.Sprintf("[init] %s", line))
		}
	}()

	// Wait for all output to be read
	wg.Wait()

	// Wait for the command to finish
	if err := cmd.Wait(); err != nil {
		e.logger.Warn("worktree init script failed",
			"script", scriptPath,
			"error", err,
		)
		e.logLine(task.ID, "system", fmt.Sprintf("Warning: worktree init script failed: %v", err))
		return
	}

	e.logLine(task.ID, "system", "Worktree init script completed successfully")
}

// runWorktreeTeardownScript runs the worktree teardown script if configured or conventionally present.
// It sets environment variables WORKTREE_TASK_ID, WORKTREE_PORT, and WORKTREE_PATH.
// Non-zero exit codes are logged as warnings but do not cause the worktree cleanup to fail.
// Output is streamed line-by-line in real-time to provide feedback during long-running scripts.
func (e *Executor) runWorktreeTeardownScript(projectDir, worktreePath string, task *db.Task) {
	scriptPath := GetWorktreeTeardownScript(projectDir)
	if scriptPath == "" {
		return
	}

	e.logLine(task.ID, "system", fmt.Sprintf("Running worktree teardown script: %s", scriptPath))

	cmd := exec.Command(scriptPath)
	cmd.Dir = worktreePath

	// Set environment variables
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("WORKTREE_TASK_ID=%d", task.ID),
		fmt.Sprintf("WORKTREE_PORT=%d", task.Port),
		fmt.Sprintf("WORKTREE_PATH=%s", worktreePath),
	)

	// Set up pipes for streaming output in real-time
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		e.logger.Warn("failed to create stdout pipe for worktree teardown script", "error", err)
		e.logLine(task.ID, "system", fmt.Sprintf("Warning: failed to set up script output: %v", err))
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		e.logger.Warn("failed to create stderr pipe for worktree teardown script", "error", err)
		e.logLine(task.ID, "system", fmt.Sprintf("Warning: failed to set up script output: %v", err))
		return
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		e.logger.Warn("failed to start worktree teardown script", "script", scriptPath, "error", err)
		e.logLine(task.ID, "system", fmt.Sprintf("Warning: failed to start worktree teardown script: %v", err))
		return
	}

	// Stream output from both stdout and stderr concurrently
	var wg sync.WaitGroup
	wg.Add(2)

	// Stream stdout
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			e.logLine(task.ID, "system", fmt.Sprintf("[teardown] %s", line))
		}
	}()

	// Stream stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			e.logLine(task.ID, "system", fmt.Sprintf("[teardown] %s", line))
		}
	}()

	// Wait for all output to be read
	wg.Wait()

	// Wait for the command to finish
	if err := cmd.Wait(); err != nil {
		e.logger.Warn("worktree teardown script failed",
			"script", scriptPath,
			"error", err,
		)
		e.logLine(task.ID, "system", fmt.Sprintf("Warning: worktree teardown script failed: %v", err))
		return
	}

	e.logLine(task.ID, "system", "Worktree teardown script completed successfully")
}

// ensureGitignore adds an entry to .gitignore if not already present.
func (e *Executor) ensureGitignore(projectDir, entry string) {
	gitignorePath := filepath.Join(projectDir, ".gitignore")

	// Read existing content
	content, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return
	}

	// Check if entry already exists
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == entry {
			return // Already present
		}
	}

	// Append entry
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	// Add newline before entry if file doesn't end with one
	if len(content) > 0 && content[len(content)-1] != '\n' {
		f.WriteString("\n")
	}
	f.WriteString(entry + "\n")
}

// getDefaultBranch returns the default branch name for a git repo.
func (e *Executor) getDefaultBranch(projectDir string) string {
	// Try to get default branch from remote
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = projectDir
	if output, err := cmd.Output(); err == nil {
		ref := strings.TrimSpace(string(output))
		// refs/remotes/origin/main -> main
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}

	// Fallback: check if main or master exists
	for _, branch := range []string{"main", "master"} {
		cmd := exec.Command("git", "rev-parse", "--verify", branch)
		cmd.Dir = projectDir
		if err := cmd.Run(); err == nil {
			return branch
		}
	}

	return "main" // Default to main
}

// updateTaskPRInfo fetches and updates PR information for a task if a PR exists for the branch.
func (e *Executor) updateTaskPRInfo(task *db.Task, projectDir string) {
	if task.BranchName == "" || e.prCache == nil {
		return
	}

	// Fetch PR info for the branch
	prInfo := e.prCache.GetPRForBranch(projectDir, task.BranchName)
	if prInfo != nil {
		task.PRURL = prInfo.URL
		task.PRNumber = prInfo.Number
	}
}

// CleanupWorktree removes a task's worktree.
func (e *Executor) CleanupWorktree(task *db.Task) error {
	if task.WorktreePath == "" {
		return nil
	}

	// Get project directory to run git commands from
	projectDir := e.getProjectDir(task.Project)
	if projectDir == "" {
		return nil
	}
	paths := e.claudePathsForProject(task.Project)

	// Run teardown script before removing the worktree
	e.runWorktreeTeardownScript(projectDir, task.WorktreePath, task)

	// Remove worktree
	cmd := exec.Command("git", "worktree", "remove", "--force", task.WorktreePath)
	cmd.Dir = projectDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remove worktree: %v\n%s", err, string(output))
	}

	// Optionally delete the branch too
	if task.BranchName != "" {
		cmd = exec.Command("git", "branch", "-D", task.BranchName)
		cmd.Dir = projectDir
		cmd.Run() // Ignore errors - branch might have been merged/deleted
	}

	// Remove project entry from ~/.claude.json (run async - this can be slow with large configs)
	go func(path, config string) {
		if err := RemoveClaudeProjectConfig(config, path); err != nil {
			// Log warning but don't fail - this is cleanup
			fmt.Fprintf(os.Stderr, "Warning: could not remove Claude project config: %v\n", err)
		}
	}(task.WorktreePath, paths.configFile)

	// Clear worktree info from task
	task.WorktreePath = ""
	task.BranchName = ""
	e.db.UpdateTask(task)

	return nil
}

// CleanupClaudeSessions removes Claude session files for a given worktree path.
// Claude stores sessions under CLAUDE_CONFIG_DIR/projects/<escaped-path>/.
// This should be called when deleting a task to clean up session data.
func CleanupClaudeSessions(worktreePath, configDir string) error {
	if worktreePath == "" {
		return nil
	}

	baseDir := ResolveClaudeConfigDir(configDir)

	// Escape the worktree path to match Claude's project directory naming
	// Claude replaces / with - and . with - (keeps leading dash)
	escapedPath := strings.ReplaceAll(worktreePath, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")

	projectDir := filepath.Join(baseDir, "projects", escapedPath)

	// Check if directory exists
	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		return nil // Nothing to clean up
	}

	// Remove the entire project directory
	if err := os.RemoveAll(projectDir); err != nil {
		return fmt.Errorf("remove claude session dir %s: %w", projectDir, err)
	}

	return nil
}

// RemoveClaudeProjectConfig removes a project entry from claude.json.
// This should be called when deleting a worktree to clean up stale config entries.
func RemoveClaudeProjectConfig(configPath, projectPath string) error {
	if projectPath == "" {
		return nil
	}

	if configPath == "" {
		configPath = ClaudeConfigFilePath("")
	}

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No config file, nothing to remove
		}
		return fmt.Errorf("read claude config: %w", err)
	}

	// Parse as generic JSON to preserve all fields
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse claude config: %w", err)
	}

	// Get projects map
	projectsRaw, ok := config["projects"]
	if !ok {
		return nil // No projects configured
	}
	projects, ok := projectsRaw.(map[string]interface{})
	if !ok {
		return nil // Invalid projects format
	}

	// Check if project exists
	if _, exists := projects[projectPath]; !exists {
		return nil // Project not in config
	}

	// Remove the project entry
	delete(projects, projectPath)
	config["projects"] = projects

	// Write back config
	newData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal claude config: %w", err)
	}

	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		return fmt.Errorf("write claude config: %w", err)
	}

	return nil
}

// PurgeStaleClaudeProjectConfigs removes entries from claude.json for paths that no longer exist.
// Returns the number of entries removed and any error encountered.
func PurgeStaleClaudeProjectConfigs(configPath string) (int, error) {
	if configPath == "" {
		configPath = ClaudeConfigFilePath("")
	}

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // No config file
		}
		return 0, fmt.Errorf("read claude config: %w", err)
	}

	// Parse as generic JSON to preserve all fields
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return 0, fmt.Errorf("parse claude config: %w", err)
	}

	// Get projects map
	projectsRaw, ok := config["projects"]
	if !ok {
		return 0, nil // No projects configured
	}
	projects, ok := projectsRaw.(map[string]interface{})
	if !ok {
		return 0, nil // Invalid projects format
	}

	// Find and remove stale entries
	removed := 0
	for path := range projects {
		// Check if path exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			delete(projects, path)
			removed++
		}
	}

	if removed == 0 {
		return 0, nil // Nothing to remove
	}

	// Write back config
	config["projects"] = projects
	newData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("marshal claude config: %w", err)
	}

	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		return 0, fmt.Errorf("write claude config: %w", err)
	}

	return removed, nil
}

// ---- Pi Executor Support ----

// runPi runs a task using Pi coding agent in a tmux window for interactive access.
func (e *Executor) runPi(ctx context.Context, task *db.Task, workDir, prompt string) execResult {
	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		e.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return execResult{Message: "tmux is not installed"}
	}

	// Check if pi is available
	if _, err := exec.LookPath("pi"); err != nil {
		e.logLine(task.ID, "error", "pi is not installed")
		return execResult{Message: "pi is not installed"}
	}

	// Ensure task-daemon session exists
	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		e.logger.Error("could not create task-daemon session", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return execResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Kill ALL existing windows with this name (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	// Create a temp file for the prompt (avoids quoting issues)
	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		e.logger.Error("could not create temp file", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		return execResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}
	promptFile.WriteString(prompt)
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	// Create a temp file for system instructions (passed via --append-system-prompt)
	systemFile, err := os.CreateTemp("", "task-system-*.txt")
	if err != nil {
		e.logger.Error("could not create system file", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create system file: %s", err.Error()))
		return execResult{Message: fmt.Sprintf("failed to create system file: %s", err.Error())}
	}
	systemFile.WriteString(e.buildSystemInstructions())
	systemFile.Close()
	defer os.Remove(systemFile.Name())

	// Script that runs pi interactively with worktree environment variables
	sessionID := os.Getenv("WORKTREE_SESSION_ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Build system prompt flag
	systemPromptFlag := fmt.Sprintf(`--append-system-prompt %q `, systemFile.Name())

	// Check for existing Pi session to resume instead of starting fresh
	existingSessionPath := task.ClaudeSessionID
	var script string
	if existingSessionPath != "" && piSessionExists(existingSessionPath) {
		e.logLine(task.ID, "system", fmt.Sprintf("Resuming existing session %s", filepath.Base(existingSessionPath)))
		script = fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q pi %s--continue "$(cat %q)"`,
			task.ID, sessionID, task.Port, task.WorktreePath, systemPromptFlag, promptFile.Name())
	} else {
		if existingSessionPath != "" {
			e.logLine(task.ID, "system", fmt.Sprintf("Session %s no longer exists, starting fresh", filepath.Base(existingSessionPath)))
			// Clear the stale session ID
			if err := e.db.UpdateTaskClaudeSessionID(task.ID, ""); err != nil {
				e.logger.Warn("failed to clear stale session ID", "task", task.ID, "error", err)
			}
		}
		script = fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q pi %s"$(cat %q)"`,
			task.ID, sessionID, task.Port, task.WorktreePath, systemPromptFlag, promptFile.Name())
	}

	// Create new window in task-daemon session (with retry logic for race conditions)
	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		e.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		return execResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	// Update windowTarget if session changed during retry
	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	// Give tmux a moment to fully create the window and start the Pi process
	time.Sleep(200 * time.Millisecond)

	// Save which daemon session owns this task's window (for kill logic)
	if err := e.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}

	// Capture and store the window ID for reliable targeting
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := e.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			e.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	// Ensure shell pane exists alongside Pi pane with environment variables
	e.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, "")

	// Configure tmux window with helpful status bar
	e.configureTmuxWindow(windowTarget)

	// Poll for output and completion
	result := e.pollTmuxSession(ctx, task.ID, windowTarget)

	return result
}

// runPiResume resumes a previous Pi session with feedback.
func (e *Executor) runPiResume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) execResult {
	// Check for existing session
	piSessionPath := task.ClaudeSessionID
	if piSessionPath == "" || !piSessionExists(piSessionPath) {
		if piSessionPath != "" {
			e.logLine(task.ID, "system", fmt.Sprintf("Session %s no longer exists, starting fresh", filepath.Base(piSessionPath)))
			// Clear the stale session ID
			if err := e.db.UpdateTaskClaudeSessionID(task.ID, ""); err != nil {
				e.logger.Warn("failed to clear stale session ID", "task", task.ID, "error", err)
			}
		} else {
			e.logLine(task.ID, "system", "No previous session found, starting fresh")
		}
		// Build a combined prompt with the feedback included
		fullPrompt := prompt + "\n\n## User Feedback\n\n" + feedback
		return e.runPi(ctx, task, workDir, fullPrompt)
	}

	e.logLine(task.ID, "system", fmt.Sprintf("Resuming session %s", filepath.Base(piSessionPath)))

	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		e.logLine(task.ID, "error", "tmux is not installed - required for task execution")
		return execResult{Message: "tmux is not installed"}
	}

	// Ensure task-daemon session exists
	daemonSession, err := ensureTmuxDaemon()
	if err != nil {
		e.logger.Error("could not create task-daemon session", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux daemon: %s", err.Error()))
		return execResult{Message: fmt.Sprintf("failed to create tmux daemon: %s", err.Error())}
	}

	windowName := TmuxWindowName(task.ID)
	windowTarget := fmt.Sprintf("%s:%s", daemonSession, windowName)

	// Kill ALL existing windows with this name (handles duplicates)
	killAllWindowsByNameAllSessions(windowName)

	// Create a temp file for the feedback (avoids quoting issues)
	feedbackFile, err := os.CreateTemp("", "task-feedback-*.txt")
	if err != nil {
		e.logger.Error("could not create temp file", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create temp file: %s", err.Error()))
		return execResult{Message: fmt.Sprintf("failed to create temp file: %s", err.Error())}
	}
	feedbackFile.WriteString(feedback)
	feedbackFile.Close()
	defer os.Remove(feedbackFile.Name())

	// Create a temp file for system instructions
	systemFile, err := os.CreateTemp("", "task-system-*.txt")
	if err != nil {
		e.logger.Error("could not create system file", "error", err)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create system file: %s", err.Error()))
		return execResult{Message: fmt.Sprintf("failed to create system file: %s", err.Error())}
	}
	systemFile.WriteString(e.buildSystemInstructions())
	systemFile.Close()
	defer os.Remove(systemFile.Name())

	// Script that resumes pi with session ID (interactive mode)
	taskSessionID := os.Getenv("WORKTREE_SESSION_ID")
	if taskSessionID == "" {
		taskSessionID = fmt.Sprintf("%d", os.Getpid())
	}

	// Build system prompt flag
	systemPromptFlag := fmt.Sprintf(`--append-system-prompt %q `, systemFile.Name())

	script := fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q pi %s--continue "$(cat %q)"`,
		task.ID, taskSessionID, task.Port, task.WorktreePath, systemPromptFlag, feedbackFile.Name())

	// Create new window in task-daemon session (with retry logic for race conditions)
	actualSession, tmuxErr := createTmuxWindow(daemonSession, windowName, workDir, script)
	if tmuxErr != nil {
		e.logger.Error("tmux new-window failed", "error", tmuxErr, "session", daemonSession)
		e.logLine(task.ID, "error", fmt.Sprintf("Failed to create tmux window: %s", tmuxErr.Error()))
		return execResult{Message: fmt.Sprintf("failed to create tmux window: %s", tmuxErr.Error())}
	}

	// Update windowTarget if session changed during retry
	if actualSession != daemonSession {
		windowTarget = fmt.Sprintf("%s:%s", actualSession, windowName)
		daemonSession = actualSession
	}

	// Give tmux a moment to fully create the window and start the Pi process
	time.Sleep(200 * time.Millisecond)

	// Save which daemon session owns this task's window (for kill logic)
	if err := e.db.UpdateTaskDaemonSession(task.ID, daemonSession); err != nil {
		e.logger.Warn("failed to save daemon session", "task", task.ID, "error", err)
	}

	// Capture and store the window ID for reliable targeting
	if windowID := getWindowID(daemonSession, windowName); windowID != "" {
		if err := e.db.UpdateTaskWindowID(task.ID, windowID); err != nil {
			e.logger.Warn("failed to save window ID", "task", task.ID, "error", err)
		}
	}

	// Ensure shell pane exists alongside Pi pane with environment variables
	e.ensureShellPane(windowTarget, workDir, task.ID, task.Port, task.WorktreePath, "")

	// Configure tmux window with helpful status bar
	e.configureTmuxWindow(windowTarget)

	// Poll for output and completion
	result := e.pollTmuxSession(ctx, task.ID, windowTarget)

	return result
}

// getPiPID finds the PID of the Pi process for a task.
func (e *Executor) getPiPID(taskID int64) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	windowName := TmuxWindowName(taskID)

	// Search all tmux sessions for a window with this task's name
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{session_name}:#{window_name}:#{pane_index} #{pane_pid}").Output()
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse "session:window:pane pid"
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		target := parts[0]
		pidStr := parts[1]

		// Only match panes in windows named after this task
		if !strings.Contains(target, windowName) {
			continue
		}

		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Check if this is a Pi process or has Pi as child
		cmdOut, _ := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
		if strings.Contains(string(cmdOut), "pi") || strings.Contains(string(cmdOut), "node") {
			return pid
		}

		// Check for pi child process
		childOut, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid), "pi").Output()
		if err == nil && len(childOut) > 0 {
			childPid, err := strconv.Atoi(strings.TrimSpace(string(childOut)))
			if err == nil {
				return childPid
			}
		}
	}

	return 0
}

// KillPiProcess terminates the Pi process for a task to free up memory.
func (e *Executor) KillPiProcess(taskID int64) bool {
	pid := e.getPiPID(taskID)
	if pid == 0 {
		return false
	}

	// Send SIGTERM for graceful shutdown
	proc, err := os.FindProcess(pid)
	if err != nil {
		e.logger.Debug("Failed to find Pi process", "pid", pid, "error", err)
		return false
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		e.logger.Debug("Failed to terminate Pi process", "pid", pid, "error", err)
		return false
	}

	e.logger.Info("Terminated Pi process", "task", taskID, "pid", pid)

	// Clean up suspended task tracking if present
	e.mu.Lock()
	delete(e.suspendedTasks, taskID)
	e.mu.Unlock()

	return true
}

// isValidWorktreePath validates that a working directory is within a .task-worktrees directory.
// This prevents Claude from accidentally writing to the main project directory.
// Returns true if the path is valid for task execution.
func isValidWorktreePath(workDir string) bool {
	// Empty path is never valid
	if workDir == "" {
		return false
	}

	// Resolve symlinks and clean the path
	absPath, err := filepath.Abs(workDir)
	if err != nil {
		return false
	}

	// Evaluate any symlinks in the path
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Path might not exist yet, use the absolute path
		resolvedPath = absPath
	}

	// Check that the path contains .task-worktrees
	// Valid paths look like: /path/to/project/.task-worktrees/123-task-slug
	return strings.Contains(resolvedPath, string(filepath.Separator)+".task-worktrees"+string(filepath.Separator))
}

// slugify converts a string to a URL/branch-friendly slug.
func slugify(s string, maxLen int) string {
	// Convert to lowercase
	s = strings.ToLower(s)

	// Replace spaces and underscores with hyphens
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	// Remove non-alphanumeric characters (except hyphens)
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}
	s = result.String()

	// Collapse multiple hyphens
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}

	// Trim hyphens from ends
	s = strings.Trim(s, "-")

	// Truncate to maxLen
	if len(s) > maxLen {
		s = s[:maxLen]
		// Don't end with a hyphen
		s = strings.TrimRight(s, "-")
	}

	return s
}
