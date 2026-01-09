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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
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
	db     *db.DB
	config *config.Config
	logger *log.Logger
	hooks  *hooks.Runner

	mu           sync.RWMutex
	runningTasks map[int64]bool               // tracks which tasks are currently executing
	cancelFuncs  map[int64]context.CancelFunc // cancel functions for running tasks
	running      bool
	stopCh       chan struct{}

	// Subscribers for real-time log updates (per-task)
	subsMu sync.RWMutex
	subs   map[int64][]chan *db.TaskLog

	// Subscribers for task events (global)
	taskSubsMu sync.RWMutex
	taskSubs   []chan TaskEvent

	// Silent mode suppresses log output (for TUI embedding)
	silent bool
}

// New creates a new executor.
func New(database *db.DB, cfg *config.Config) *Executor {
	return &Executor{
		db:           database,
		config:       cfg,
		logger:       log.NewWithOptions(io.Discard, log.Options{Prefix: "executor"}),
		hooks:        hooks.NewSilent(hooks.DefaultHooksDir()),
		stopCh:       make(chan struct{}),
		subs:         make(map[int64][]chan *db.TaskLog),
		taskSubs:     make([]chan TaskEvent, 0),
		runningTasks: make(map[int64]bool),
		cancelFuncs:  make(map[int64]context.CancelFunc),
		silent:       true,
	}
}

// NewWithLogging creates an executor that logs to stderr (for daemon mode).
func NewWithLogging(database *db.DB, cfg *config.Config, w io.Writer) *Executor {
	return &Executor{
		db:           database,
		config:       cfg,
		logger:       log.NewWithOptions(w, log.Options{Prefix: "executor"}),
		hooks:        hooks.New(hooks.DefaultHooksDir()),
		stopCh:       make(chan struct{}),
		subs:         make(map[int64][]chan *db.TaskLog),
		taskSubs:     make([]chan TaskEvent, 0),
		runningTasks: make(map[int64]bool),
		cancelFuncs:  make(map[int64]context.CancelFunc),
		silent:       false,
	}
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
	// Mark as backlog in database (for cross-process communication)
	e.updateStatus(taskID, db.StatusBacklog)
	e.logLine(taskID, "system", "Task interrupted by user")

	// If running locally, cancel the context
	e.mu.RLock()
	cancel, ok := e.cancelFuncs[taskID]
	e.mu.RUnlock()
	if ok {
		cancel()
	}
	return true
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
	}
	return nil
}

func (e *Executor) worker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Check merged branches every 30 seconds (15 ticks)
	tickCount := 0
	const mergeCheckInterval = 15

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.processNextTask(ctx)

			// Periodically check for merged branches
			tickCount++
			if tickCount >= mergeCheckInterval {
				tickCount = 0
				e.checkMergedBranches()
			}
		}
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
	workDir, err := e.setupWorktree(task)
	if err != nil {
		e.logger.Error("Failed to setup worktree", "error", err)
		// Fall back to project directory
		workDir = e.getProjectDir(task.Project)
		if workDir == "" {
			workDir, _ = os.Getwd()
		}
	}

	// Prepare attachments (write to temp files)
	attachmentPaths, cleanupAttachments := e.prepareAttachments(task.ID)
	defer cleanupAttachments()
	if len(attachmentPaths) > 0 {
		e.logLine(task.ID, "system", fmt.Sprintf("Task has %d attachment(s)", len(attachmentPaths)))
	}

	// Check if this is a retry (has previous session to resume)
	retryFeedback, _ := e.db.GetRetryFeedback(task.ID)
	isRetry := retryFeedback != ""

	// Build prompt based on task type
	prompt := e.buildPrompt(task, attachmentPaths)

	// Run Claude
	var result execResult
	if isRetry {
		e.logLine(task.ID, "system", "Resuming previous session with feedback")
		result = e.runClaudeResume(taskCtx, task.ID, workDir, prompt, retryFeedback)
	} else {
		result = e.runClaude(taskCtx, task.ID, workDir, prompt)
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
	} else if currentStatus == db.StatusBlocked {
		// Hooks already marked as blocked - respect that
		e.logLine(task.ID, "system", "Task waiting for input")
		e.hooks.OnStatusChange(task, db.StatusBlocked, "Task waiting for input")
	} else if currentStatus == db.StatusDone {
		// Hooks/MCP already marked as done - respect that
		e.logLine(task.ID, "system", "Task completed")
		e.hooks.OnStatusChange(task, db.StatusDone, "Task completed")
	} else if result.Success {
		e.updateStatus(task.ID, db.StatusDone)
		e.logLine(task.ID, "system", "Task completed successfully")
		e.hooks.OnStatusChange(task, db.StatusDone, "Task completed successfully")

		// Extract memories from successful task
		go func() {
			if err := e.ExtractMemories(context.Background(), task); err != nil {
				e.logger.Error("Memory extraction failed", "task", task.ID, "error", err)
			}
		}()
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

func (e *Executor) getProjectDir(project string) string {
	return e.config.GetProjectDir(project)
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

// prepareAttachments writes task attachments to temp files and returns paths.
// Returns a list of file paths and a cleanup function.
func (e *Executor) prepareAttachments(taskID int64) ([]string, func()) {
	attachments, err := e.db.ListAttachmentsWithData(taskID)
	if err != nil || len(attachments) == 0 {
		return nil, func() {}
	}

	var paths []string
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("task-%d-attachments-", taskID))
	if err != nil {
		e.logger.Error("Failed to create temp dir for attachments", "error", err)
		return nil, func() {}
	}

	for _, a := range attachments {
		path := filepath.Join(tempDir, a.Filename)
		if err := os.WriteFile(path, a.Data, 0644); err != nil {
			e.logger.Error("Failed to write attachment", "file", a.Filename, "error", err)
			continue
		}
		paths = append(paths, path)
	}

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return paths, cleanup
}

// getAttachmentsSection returns a prompt section describing attachments.
func (e *Executor) getAttachmentsSection(taskID int64, paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	var section strings.Builder
	section.WriteString("\n## Attachments\n\n")
	section.WriteString("The following files are attached to this task:\n")
	for _, p := range paths {
		section.WriteString(fmt.Sprintf("- %s\n", p))
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

	// Add project memories if available
	memories := e.getProjectMemoriesSection(task.Project)

	// Get project-specific instructions
	projectInstructions := e.getProjectInstructions(task.Project)

	// Check for conversation history (from previous runs/retries)
	conversationHistory := e.getConversationHistory(task.ID)

	// Get attachments section
	attachments := e.getAttachmentsSection(task.ID, attachmentPaths)

	switch task.Type {
	case db.TypeCode:
		prompt.WriteString(fmt.Sprintf("You are working on: %s\n\n", task.Project))
		if projectInstructions != "" {
			prompt.WriteString(fmt.Sprintf("## Project Instructions\n\n%s\n\n", projectInstructions))
		}
		if memories != "" {
			prompt.WriteString(memories)
		}
		prompt.WriteString(fmt.Sprintf("Task: %s\n\n", task.Title))
		if task.Body != "" {
			prompt.WriteString(fmt.Sprintf("%s\n\n", task.Body))
		}
		if attachments != "" {
			prompt.WriteString(attachments)
		}
		if conversationHistory != "" {
			prompt.WriteString(conversationHistory)
		}
		prompt.WriteString(`Instructions:
- Explore the codebase to understand the context
- Implement the solution
- Write tests if applicable
- Commit your changes with clear messages
- Submit a pull request when your work is complete

IMPORTANT: Your objective is to submit a PR to complete this task. Always remember to create and submit a pull request as the final step of your work. This is how you signal that the implementation is ready for review and merging.

When finished, provide a summary of what you did:
- List files changed/created
- Describe the key changes made
- Include any relevant links (PRs, commits, etc.)
- Note any follow-up items or concerns
`)

	case db.TypeWriting:
		prompt.WriteString("You are a skilled writer. Please complete this task:\n\n")
		if projectInstructions != "" {
			prompt.WriteString(fmt.Sprintf("## Project Instructions\n\n%s\n\n", projectInstructions))
		}
		if memories != "" {
			prompt.WriteString(memories)
		}
		prompt.WriteString(fmt.Sprintf("Task: %s\n\n", task.Title))
		if task.Body != "" {
			prompt.WriteString(fmt.Sprintf("Details: %s\n\n", task.Body))
		}
		if attachments != "" {
			prompt.WriteString(attachments)
		}
		if conversationHistory != "" {
			prompt.WriteString(conversationHistory)
		}
		prompt.WriteString("Write the requested content. Be professional, clear, and match the appropriate tone.\n")
		prompt.WriteString("Output the final content, then summarize what you created.\n")

	case db.TypeThinking:
		prompt.WriteString("You are a strategic advisor. Analyze this thoroughly:\n\n")
		if projectInstructions != "" {
			prompt.WriteString(fmt.Sprintf("## Project Instructions\n\n%s\n\n", projectInstructions))
		}
		if memories != "" {
			prompt.WriteString(memories)
		}
		prompt.WriteString(fmt.Sprintf("Question: %s\n\n", task.Title))
		if task.Body != "" {
			prompt.WriteString(fmt.Sprintf("Context: %s\n\n", task.Body))
		}
		if attachments != "" {
			prompt.WriteString(attachments)
		}
		if conversationHistory != "" {
			prompt.WriteString(conversationHistory)
		}
		prompt.WriteString(`Provide:
1. Clear analysis of the question/problem
2. Key considerations and tradeoffs
3. Recommended approach
4. Concrete next steps

Think deeply but be actionable. Summarize your conclusions clearly.
`)

	default:
		// Generic task
		if projectInstructions != "" {
			prompt.WriteString(fmt.Sprintf("## Project Instructions\n\n%s\n\n", projectInstructions))
		}
		if memories != "" {
			prompt.WriteString(memories)
		}
		prompt.WriteString(fmt.Sprintf("Task: %s\n\n", task.Title))
		if task.Body != "" {
			prompt.WriteString(fmt.Sprintf("%s\n\n", task.Body))
		}
		if attachments != "" {
			prompt.WriteString(attachments)
		}
		if conversationHistory != "" {
			prompt.WriteString(conversationHistory)
		}
		prompt.WriteString("Complete this task and summarize what you did.\n")
	}

	// Add response guidance to ALL task types
	// Note: Task status is now managed automatically via Claude hooks
	prompt.WriteString(`
═══════════════════════════════════════════════════════════════
                      TASK GUIDANCE
═══════════════════════════════════════════════════════════════

Work on this task until completion. When you're done or need input:

✓ WHEN TASK IS COMPLETE:
  Provide a clear summary of what was accomplished

✓ WHEN YOU NEED INPUT/CLARIFICATION:
  Ask your question clearly and wait for a response

The task system will automatically detect your status.
═══════════════════════════════════════════════════════════════
`)

	return prompt.String()
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
		sb.WriteString("Task types: code, writing, thinking\n")
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

// getDaemonSessionName returns the task-daemon session name for this instance.
func getDaemonSessionName() string {
	// Check if SESSION_ID is set (for child processes)
	if sid := os.Getenv("TASK_SESSION_ID"); sid != "" {
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

// ensureTmuxDaemon ensures the task-daemon session exists.
func ensureTmuxDaemon() error {
	daemonSession := getDaemonSessionName()
	// Check if session exists
	if exec.Command("tmux", "has-session", "-t", daemonSession).Run() == nil {
		return nil
	}
	// Create it with a placeholder window that we'll kill later
	return exec.Command("tmux", "new-session", "-d", "-s", daemonSession, "-n", "_placeholder").Run()
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
	// The TASK_ID env var is set when launching Claude
	// We use multiple hook types to ensure accurate task state tracking:
	// - PreToolUse: Fires before tool execution - ensures task is "processing"
	// - PostToolUse: Fires after tool completes - ensures task stays "processing"
	// - Notification: Fires when Claude is idle or needs permission - marks task "blocked"
	// - Stop: Fires when Claude finishes responding - marks task "blocked" when waiting for input
	hooksConfig := map[string]interface{}{
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
		// Merge our hooks with existing settings
		if json.Unmarshal(existingData, &finalConfig) != nil {
			finalConfig = hooksConfig
		} else {
			// Merge hooks into existing config
			finalConfig["hooks"] = hooksConfig["hooks"]
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
func (e *Executor) runClaude(ctx context.Context, taskID int64, workDir, prompt string) execResult {
	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		return e.runClaudeDirect(ctx, taskID, workDir, prompt)
	}

	// Ensure task-daemon session exists
	if err := ensureTmuxDaemon(); err != nil {
		e.logger.Warn("could not create task-daemon session", "error", err)
		return e.runClaudeDirect(ctx, taskID, workDir, prompt)
	}

	windowName := TmuxWindowName(taskID)
	windowTarget := TmuxSessionName(taskID)

	// Kill any existing window with this name
	exec.Command("tmux", "kill-window", "-t", windowTarget).Run()

	// Setup Claude hooks for status updates
	cleanupHooks, err := e.setupClaudeHooks(workDir, taskID)
	if err != nil {
		e.logger.Warn("could not setup Claude hooks", "error", err)
	}
	// Note: we don't clean up hooks config immediately - it needs to persist for the session

	// Create a temp file for the prompt (avoids quoting issues)
	promptFile, err := os.CreateTemp("", "task-prompt-*.txt")
	if err != nil {
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return e.runClaudeDirect(ctx, taskID, workDir, prompt)
	}
	promptFile.WriteString(prompt)
	promptFile.Close()
	defer os.Remove(promptFile.Name())

	// Script that runs claude interactively with TASK_ID and TASK_SESSION_ID env vars
	// Note: tmux starts in workDir (-c flag), so claude inherits proper permissions and hooks config
	// Run interactively (no -p) so user can attach and see/interact in real-time
	// TASK_ID is passed so hooks know which task to update
	// TASK_SESSION_ID ensures consistent session naming across all processes
	sessionID := os.Getenv("TASK_SESSION_ID")
	if sessionID == "" {
		sessionID = fmt.Sprintf("%d", os.Getpid())
	}
	script := fmt.Sprintf(`TASK_ID=%d TASK_SESSION_ID=%s claude --dangerously-skip-permissions "$(cat %q)"`, taskID, sessionID, promptFile.Name())

	// Create new window in task-daemon session
	tmuxCmd := exec.Command("tmux", "new-window", "-d", "-t", getDaemonSessionName(), "-n", windowName, "-c", workDir, "sh", "-c", script)
	if err := tmuxCmd.Run(); err != nil {
		e.logger.Warn("tmux failed, falling back to direct", "error", err)
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return e.runClaudeDirect(ctx, taskID, workDir, prompt)
	}

	// Configure tmux window with helpful status bar
	e.configureTmuxWindow(windowTarget)

	// Poll for output and completion
	result := e.pollTmuxSession(ctx, taskID, windowTarget)

	// Clean up hooks config after session ends
	if cleanupHooks != nil {
		cleanupHooks()
	}

	return result
}

// runClaudeResume resumes a previous Claude session with feedback.
// If no previous session exists, starts fresh with the full prompt + feedback.
func (e *Executor) runClaudeResume(ctx context.Context, taskID int64, workDir, prompt, feedback string) execResult {
	// Find the most recent claude session ID for this workDir
	sessionID := e.findClaudeSessionID(workDir)
	if sessionID == "" {
		e.logLine(taskID, "system", "No previous session found, starting fresh")
		// Build a combined prompt with the feedback included
		fullPrompt := prompt + "\n\n## User Feedback\n\n" + feedback
		return e.runClaude(ctx, taskID, workDir, fullPrompt)
	}

	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		return e.runClaudeDirect(ctx, taskID, workDir, feedback)
	}

	// Ensure task-daemon session exists
	if err := ensureTmuxDaemon(); err != nil {
		e.logger.Warn("could not create task-daemon session", "error", err)
		return e.runClaudeDirect(ctx, taskID, workDir, feedback)
	}

	windowName := TmuxWindowName(taskID)
	windowTarget := TmuxSessionName(taskID)

	// Kill any existing window with this name
	exec.Command("tmux", "kill-window", "-t", windowTarget).Run()

	// Setup Claude hooks for status updates
	cleanupHooks, err := e.setupClaudeHooks(workDir, taskID)
	if err != nil {
		e.logger.Warn("could not setup Claude hooks", "error", err)
	}

	// Create a temp file for the feedback (avoids quoting issues)
	feedbackFile, err := os.CreateTemp("", "task-feedback-*.txt")
	if err != nil {
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return e.runClaudeDirect(ctx, taskID, workDir, feedback)
	}
	feedbackFile.WriteString(feedback)
	feedbackFile.Close()
	defer os.Remove(feedbackFile.Name())

	// Script that resumes claude with session ID (interactive mode)
	// TASK_ID is passed so hooks know which task to update
	// TASK_SESSION_ID ensures consistent session naming across all processes
	taskSessionID := os.Getenv("TASK_SESSION_ID")
	if taskSessionID == "" {
		taskSessionID = fmt.Sprintf("%d", os.Getpid())
	}
	script := fmt.Sprintf(`TASK_ID=%d TASK_SESSION_ID=%s claude --dangerously-skip-permissions --resume %s "$(cat %q)"`, taskID, taskSessionID, sessionID, feedbackFile.Name())

	// Create new window in task-daemon session
	tmuxCmd := exec.Command("tmux", "new-window", "-d", "-t", getDaemonSessionName(), "-n", windowName, "-c", workDir, "sh", "-c", script)
	if err := tmuxCmd.Run(); err != nil {
		e.logger.Warn("tmux failed, falling back to direct", "error", err)
		if cleanupHooks != nil {
			cleanupHooks()
		}
		return e.runClaudeDirect(ctx, taskID, workDir, feedback)
	}

	// Configure tmux window with helpful status bar
	e.configureTmuxWindow(windowTarget)

	// Poll for output and completion
	result := e.pollTmuxSession(ctx, taskID, windowTarget)

	// Clean up hooks config after session ends
	if cleanupHooks != nil {
		cleanupHooks()
	}

	return result
}

// findClaudeSessionID finds the most recent claude session ID for a workDir
func (e *Executor) findClaudeSessionID(workDir string) string {
	// Claude stores sessions in ~/.claude/projects/<escaped-path>/
	// The path is escaped: /Users/bruno/foo -> -Users-bruno-foo
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Escape the workDir path (replace / with -)
	escapedPath := strings.ReplaceAll(workDir, "/", "-")
	if strings.HasPrefix(escapedPath, "-") {
		escapedPath = escapedPath[1:] // Remove leading dash
	}

	projectDir := filepath.Join(home, ".claude", "projects", escapedPath)

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

// pollTmuxSession waits for the tmux session to end.
// Status is managed entirely by Claude hooks - we just wait and check the result.
// Task only goes to "done" if user/MCP explicitly marks it done.
func (e *Executor) pollTmuxSession(ctx context.Context, taskID int64, sessionName string) execResult {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.killTmuxSession(sessionName)
			return execResult{Interrupted: true}

		case <-ticker.C:
			// Check DB status (set by hooks, user, or MCP)
			task, err := e.db.GetTask(taskID)
			if err == nil && task != nil {
				if task.Status == db.StatusBacklog {
					e.killTmuxSession(sessionName)
					return execResult{Interrupted: true}
				}
				if task.Status == db.StatusDone {
					e.killTmuxSession(sessionName)
					return execResult{Success: true}
				}
			}

			// Check if tmux window still exists
			windowExists := exec.Command("tmux", "list-panes", "-t", sessionName).Run() == nil

			// Also check task-ui (pane might be joined there)
			if !windowExists {
				checkCmd := exec.Command("tmux", "list-panes", "-t", "task-ui", "-F", "#{pane_current_command}")
				if out, err := checkCmd.Output(); err == nil {
					if strings.Contains(string(out), "claude") {
						windowExists = true
					}
				}
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

// killTmuxSession kills a tmux session if it exists.
func (e *Executor) killTmuxSession(sessionName string) {
	// Kill the window (we use windows in task-daemon, not separate sessions)
	exec.Command("tmux", "kill-window", "-t", sessionName).Run()
}

// configureTmuxWindow sets up helpful UI elements for a task window.
func (e *Executor) configureTmuxWindow(windowTarget string) {
	// Window-specific options are limited; most styling is session-wide
	// Just ensure the daemon session has good defaults
	daemonSession := getDaemonSessionName()
	exec.Command("tmux", "set-option", "-t", daemonSession, "status", "on").Run()
	exec.Command("tmux", "set-option", "-t", daemonSession, "status-style", "bg=#f59e0b,fg=black").Run()
	exec.Command("tmux", "set-option", "-t", daemonSession, "status-left", " TASK DAEMON ").Run()
	exec.Command("tmux", "set-option", "-t", daemonSession, "status-right", " Ctrl+C kills Claude ").Run()
	exec.Command("tmux", "set-option", "-t", daemonSession, "status-right-length", "30").Run()
}

// isClaudeIdle checks if claude appears to be idle (showing prompt with no activity).
// This detects when claude has finished but didn't output TASK_COMPLETE.
func (e *Executor) isClaudeIdle(output string) bool {
	// Get the last few lines of output
	lines := strings.Split(output, "\n")
	var recentLines []string
	start := len(lines) - 10
	if start < 0 {
		start = 0
	}
	for i := start; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			recentLines = append(recentLines, line)
		}
	}

	if len(recentLines) == 0 {
		return false
	}

	// Check for Claude Code prompt indicator (❯) in recent lines
	// The prompt typically looks like: "❯" or contains the chevron
	lastLine := recentLines[len(recentLines)-1]

	// Claude Code shows ❯ when waiting for input at its prompt
	if strings.Contains(lastLine, "❯") {
		return true
	}

	// Also check for common shell prompts that might indicate claude exited
	// and we're back at a shell prompt
	if strings.HasSuffix(lastLine, "$") || strings.HasSuffix(lastLine, "%") {
		return true
	}

	return false
}

// isWaitingForInput checks if claude appears to be waiting for user input.
func (e *Executor) isWaitingForInput(output string, lastOutputTime time.Time) bool {
	// Only consider waiting if no new output for a while
	if time.Since(lastOutputTime) < 3*time.Second {
		return false
	}

	// Get the last few lines of output (the prompt area)
	lines := strings.Split(output, "\n")
	var recentLines string
	start := len(lines) - 15
	if start < 0 {
		start = 0
	}
	recentLines = strings.Join(lines[start:], "\n")
	recentLower := strings.ToLower(recentLines)

	// Permission prompts
	if strings.Contains(recentLower, "allow") && strings.Contains(recentLower, "?") {
		if strings.Contains(recentLower, "[y/n") || strings.Contains(recentLower, "(y/n") ||
			strings.Contains(recentLower, "yes/no") {
			return true
		}
	}

	// Claude Code permission prompts (numbered options with "do you want to proceed")
	if strings.Contains(recentLower, "do you want to proceed") ||
		(strings.Contains(recentLower, "esc to cancel") && strings.Contains(recentLower, "1.")) {
		return true
	}

	// Yes/no prompts
	if strings.Contains(recentLower, "[y/n]") || strings.Contains(recentLower, "(yes/no)") {
		return true
	}

	// Press enter prompts
	if strings.Contains(recentLower, "press enter") || strings.Contains(recentLower, "press any key") {
		return true
	}

	// Common input prompts
	if strings.Contains(recentLower, "enter your") || strings.Contains(recentLower, "type your") ||
		strings.Contains(recentLower, "please provide") || strings.Contains(recentLower, "please enter") {
		return true
	}

	return false
}

// hasCompletionMarker checks if any line in the output is the completion marker.
// This avoids false positives from the prompt text which contains "TASK_COMPLETE" in instructions.
func hasCompletionMarker(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Check for exact match or line starting with TASK_COMPLETE
		// (Claude might add punctuation or emoji after it)
		if trimmed == "TASK_COMPLETE" || strings.HasPrefix(trimmed, "TASK_COMPLETE") {
			// Make sure it's not part of the instruction text (which has quotes around it)
			if !strings.Contains(line, `"TASK_COMPLETE"`) && !strings.Contains(line, "output") {
				return true
			}
		}
	}
	return false
}

// parseOutputMarkers checks output for completion markers
func (e *Executor) parseOutputMarkers(output string) execResult {
	if strings.Contains(output, "TASK_COMPLETE") {
		return execResult{Success: true}
	}
	if idx := strings.Index(output, "NEEDS_INPUT:"); idx >= 0 {
		rest := output[idx+len("NEEDS_INPUT:"):]
		if newline := strings.Index(rest, "\n"); newline >= 0 {
			rest = rest[:newline]
		}
		return execResult{NeedsInput: true, Message: strings.TrimSpace(rest)}
	}
	return execResult{Success: true}
}

// runClaudeDirect runs claude directly without tmux (fallback)
func (e *Executor) runClaudeDirect(ctx context.Context, taskID int64, workDir, prompt string) execResult {
	cmd := exec.CommandContext(ctx, "claude", "--dangerously-skip-permissions", "-p", prompt)
	cmd.Dir = workDir
	// Pass TASK_ID so hooks know which task to update
	cmd.Env = append(os.Environ(), fmt.Sprintf("TASK_ID=%d", taskID))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return execResult{Message: fmt.Sprintf("stdout pipe: %v", err)}
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return execResult{Message: fmt.Sprintf("stderr pipe: %v", err)}
	}

	if err := cmd.Start(); err != nil {
		return execResult{Message: fmt.Sprintf("start: %v", err)}
	}

	// Monitor for DB-based interrupt
	interruptCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				task, err := e.db.GetTask(taskID)
				if err == nil && task != nil && task.Status == db.StatusBacklog {
					if cmd.Process != nil {
						cmd.Process.Kill()
					}
					close(interruptCh)
					return
				}
			}
		}
	}()

	var mu sync.Mutex
	var allOutput []string
	var foundComplete bool
	var needsInputMsg string

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			e.logLine(taskID, "output", line)

			mu.Lock()
			allOutput = append(allOutput, line)
			if strings.Contains(line, "TASK_COMPLETE") {
				foundComplete = true
			}
			if idx := strings.Index(line, "NEEDS_INPUT:"); idx >= 0 {
				needsInputMsg = strings.TrimSpace(line[idx+len("NEEDS_INPUT:"):])
			}
			mu.Unlock()
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			e.logLine(taskID, "error", scanner.Text())
		}
	}()

	err = cmd.Wait()

	select {
	case <-interruptCh:
		return execResult{Interrupted: true}
	default:
	}
	if ctx.Err() == context.Canceled {
		return execResult{Interrupted: true}
	}

	mu.Lock()
	defer mu.Unlock()

	if foundComplete {
		return execResult{Success: true}
	}
	if needsInputMsg != "" {
		return execResult{NeedsInput: true, Message: needsInputMsg}
	}

	if err != nil {
		return execResult{Message: fmt.Sprintf("claude exited: %v", err)}
	}

	// Check if task was marked as blocked via MCP (workflow_needs_input)
	task, dbErr := e.db.GetTask(taskID)
	if dbErr == nil && task != nil && task.Status == db.StatusBlocked {
		logs, _ := e.db.GetTaskLogs(taskID, 5)
		var question string
		for _, l := range logs {
			if l.LineType == "question" {
				question = l.Content
				break
			}
		}
		return execResult{NeedsInput: true, Message: question}
	}

	return execResult{Success: true}
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

// getProjectMemoriesSection builds a context section from stored project memories.
func (e *Executor) getProjectMemoriesSection(project string) string {
	if project == "" {
		return ""
	}

	memories, err := e.db.GetProjectMemories(project, 15)
	if err != nil || len(memories) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Project Context (from previous tasks)\n\n")

	// Group by category for better organization
	byCategory := make(map[string][]*db.ProjectMemory)
	for _, m := range memories {
		byCategory[m.Category] = append(byCategory[m.Category], m)
	}

	categoryOrder := []string{
		db.MemoryCategoryPattern,
		db.MemoryCategoryContext,
		db.MemoryCategoryDecision,
		db.MemoryCategoryGotcha,
		db.MemoryCategoryGeneral,
	}
	categoryLabels := map[string]string{
		db.MemoryCategoryPattern:  "Patterns & Conventions",
		db.MemoryCategoryContext:  "Project Context",
		db.MemoryCategoryDecision: "Key Decisions",
		db.MemoryCategoryGotcha:   "Known Gotchas",
		db.MemoryCategoryGeneral:  "General Notes",
	}

	for _, cat := range categoryOrder {
		mems := byCategory[cat]
		if len(mems) == 0 {
			continue
		}
		label := categoryLabels[cat]
		if label == "" {
			label = cat
		}
		sb.WriteString(fmt.Sprintf("### %s\n", label))
		for _, m := range mems {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
		}
		sb.WriteString("\n")
	}

	return sb.String()
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

// checkMergedBranches checks for tasks whose branches have been merged into the default branch.
// If a task's branch is merged, it automatically closes the task.
func (e *Executor) checkMergedBranches() {
	// Get all tasks that have branches and aren't done
	tasks, err := e.db.GetTasksWithBranches()
	if err != nil {
		e.logger.Debug("Failed to get tasks with branches", "error", err)
		return
	}

	for _, task := range tasks {
		// Skip tasks currently being processed
		e.mu.RLock()
		isRunning := e.runningTasks[task.ID]
		e.mu.RUnlock()
		if isRunning {
			continue
		}

		// Check if the branch has been merged
		if e.isBranchMerged(task) {
			e.logger.Info("Branch merged, closing task", "id", task.ID, "branch", task.BranchName)
			e.logLine(task.ID, "system", fmt.Sprintf("Branch %s has been merged - automatically closing task", task.BranchName))
			e.updateStatus(task.ID, db.StatusDone)
			e.hooks.OnStatusChange(task, db.StatusDone, "PR merged")
		}
	}
}

// isBranchMerged checks if a task's branch has been merged into the default branch.
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

	// Get the default branch
	defaultBranch := e.getDefaultBranch(projectDir)

	// Fetch from remote to get latest state (ignore errors - might be offline)
	fetchCmd := exec.Command("git", "fetch", "--quiet")
	fetchCmd.Dir = projectDir
	fetchCmd.Run()

	// Check if the branch has been merged into the default branch
	// Use git branch --merged to see which branches have been merged
	cmd := exec.Command("git", "branch", "-r", "--merged", defaultBranch)
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
	lsRemoteCmd := exec.Command("git", "ls-remote", "--heads", "origin", task.BranchName)
	lsRemoteCmd.Dir = projectDir
	lsOutput, err := lsRemoteCmd.Output()
	if err == nil && len(strings.TrimSpace(string(lsOutput))) == 0 {
		// Branch doesn't exist on remote - check if it ever had commits
		// that are now part of the default branch
		logCmd := exec.Command("git", "log", "--oneline", "-1", "origin/"+defaultBranch, "--grep="+task.BranchName)
		logCmd.Dir = projectDir
		logOutput, err := logCmd.Output()
		if err == nil && len(strings.TrimSpace(string(logOutput))) > 0 {
			return true
		}

		// Alternative: check if local branch exists and its tip is reachable from default
		localLogCmd := exec.Command("git", "branch", "--list", task.BranchName)
		localLogCmd.Dir = projectDir
		localOutput, _ := localLogCmd.Output()
		if len(strings.TrimSpace(string(localOutput))) > 0 {
			// Local branch exists - check if its commits are in default branch
			mergeBaseCmd := exec.Command("git", "merge-base", "--is-ancestor", task.BranchName, defaultBranch)
			mergeBaseCmd.Dir = projectDir
			if mergeBaseCmd.Run() == nil {
				return true
			}
		}
	}

	return false
}

// setupWorktree creates a git worktree for the task if the project is a git repo.
// Returns the working directory to use (worktree path or project path).
func (e *Executor) setupWorktree(task *db.Task) (string, error) {
	// Get project directory
	projectDir := e.getProjectDir(task.Project)
	
	home, _ := os.UserHomeDir()
	
	if projectDir == "" {
		// No project - use default tasks directory with per-task subdirs
		tasksDir := filepath.Join(home, ".local", "share", "task", "tasks")
		slug := slugify(task.Title, 40)
		taskDir := filepath.Join(tasksDir, fmt.Sprintf("%d-%s", task.ID, slug))
		if err := os.MkdirAll(taskDir, 0755); err != nil {
			return "", fmt.Errorf("create task dir: %w", err)
		}
		return taskDir, nil
	}

	// Check if project is a git repo
	gitDir := filepath.Join(projectDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		// Not a git repo, use project dir directly
		return projectDir, nil
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
		e.db.UpdateTask(task)
		trustMiseConfig(worktreePath)
		copyMCPConfig(projectDir, worktreePath)
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
					e.db.UpdateTask(task)
					trustMiseConfig(worktreePath)
					copyMCPConfig(projectDir, worktreePath)
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
	e.db.UpdateTask(task)

	e.logLine(task.ID, "system", fmt.Sprintf("Created worktree at %s (branch: %s)", worktreePath, branchName))

	trustMiseConfig(worktreePath)
	copyMCPConfig(projectDir, worktreePath)

	return worktreePath, nil
}

// trustMiseConfig trusts mise config files in a directory (no-op if mise not installed).
func trustMiseConfig(dir string) {
	if _, err := exec.LookPath("mise"); err == nil {
		exec.Command("mise", "trust", dir).Run()
	}
}

// copyMCPConfig copies the MCP server configuration from the source project to the worktree
// in ~/.claude.json so that Claude Code in the worktree has the same MCP servers available.
func copyMCPConfig(srcDir, dstDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	claudeConfigPath := filepath.Join(home, ".claude.json")

	// Read existing config
	data, err := os.ReadFile(claudeConfigPath)
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

	if err := os.WriteFile(claudeConfigPath, newData, 0644); err != nil {
		return fmt.Errorf("write claude config: %w", err)
	}

	return nil
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

	// Clear worktree info from task
	task.WorktreePath = ""
	task.BranchName = ""
	e.db.UpdateTask(task)

	return nil
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
