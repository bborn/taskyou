// Package executor runs Claude Code tasks in the background.
package executor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
	"github.com/charmbracelet/log"
)

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

	// Subscribers for real-time updates
	subsMu sync.RWMutex
	subs   map[int64][]chan *db.TaskLog

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
	// Mark as interrupted in database (for cross-process communication)
	e.db.UpdateTaskStatus(taskID, db.StatusInterrupted)
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

func (e *Executor) worker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.processNextTask(ctx)
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

	// Update status to processing
	if err := e.db.UpdateTaskStatus(task.ID, db.StatusProcessing); err != nil {
		e.logger.Error("Failed to update status", "error", err)
		return
	}

	// Log start and trigger hook
	startMsg := fmt.Sprintf("Starting task #%d: %s", task.ID, task.Title)
	e.logLine(task.ID, "system", startMsg)
	e.hooks.OnStatusChange(task, db.StatusProcessing, startMsg)

	// Determine project directory
	projectDir := e.getProjectDir(task.Project)

	// Build prompt based on task type
	prompt := e.buildPrompt(task)

	// Run Crush
	result := e.runCrush(taskCtx, task.ID, projectDir, prompt)

	// Update final status and trigger hooks
	if result.Interrupted {
		// Status already set by Interrupt(), just run hook
		e.hooks.OnStatusChange(task, db.StatusInterrupted, "Task interrupted by user")
	} else if result.Success {
		e.db.UpdateTaskStatus(task.ID, db.StatusReady)
		e.logLine(task.ID, "system", "Task completed successfully")
		e.hooks.OnStatusChange(task, db.StatusReady, "Task completed successfully")
	} else if result.NeedsInput {
		e.db.UpdateTaskStatus(task.ID, db.StatusBlocked)
		// Log the question with special type so UI can display it
		e.logLine(task.ID, "question", result.Message)
		e.logLine(task.ID, "system", "Task needs input - use 'r' to retry with your answer")
		e.hooks.OnStatusChange(task, db.StatusBlocked, result.Message)
	} else {
		e.db.UpdateTaskStatus(task.ID, db.StatusBlocked)
		msg := fmt.Sprintf("Task failed: %s", result.Message)
		e.logLine(task.ID, "error", msg)
		e.hooks.OnStatusChange(task, db.StatusBlocked, msg)
	}

	e.logger.Info("Task finished", "id", task.ID, "success", result.Success)
}

func (e *Executor) getProjectDir(project string) string {
	return e.config.GetProjectDir(project)
}

func (e *Executor) buildPrompt(task *db.Task) string {
	var prompt strings.Builder

	// Add project memories if available
	memories := e.getProjectMemoriesSection(task.Project)

	switch task.Type {
	case db.TypeCode:
		prompt.WriteString(fmt.Sprintf("You are working on: %s\n\n", task.Project))
		if memories != "" {
			prompt.WriteString(memories)
		}
		prompt.WriteString(fmt.Sprintf("Task: %s\n\n", task.Title))
		if task.Body != "" {
			prompt.WriteString(fmt.Sprintf("%s\n\n", task.Body))
		}
		prompt.WriteString(`Instructions:
- Explore the codebase to understand the context
- Implement the solution
- Write tests if applicable
- Commit your changes with clear messages

When finished successfully, output: TASK_COMPLETE
If you need input from me: output NEEDS_INPUT: followed by your question`)

	case db.TypeWriting:
		prompt.WriteString("You are a skilled writer. Please complete this task:\n\n")
		if memories != "" {
			prompt.WriteString(memories)
		}
		prompt.WriteString(fmt.Sprintf("Task: %s\n\n", task.Title))
		if task.Body != "" {
			prompt.WriteString(fmt.Sprintf("Details: %s\n\n", task.Body))
		}
		prompt.WriteString("Write the requested content. Be professional, clear, and match the appropriate tone.\n")
		prompt.WriteString("Output only the final content, no meta-commentary.\n")
		prompt.WriteString("When finished, output: TASK_COMPLETE")

	case db.TypeThinking:
		prompt.WriteString("You are a strategic advisor. Analyze this thoroughly:\n\n")
		if memories != "" {
			prompt.WriteString(memories)
		}
		prompt.WriteString(fmt.Sprintf("Question: %s\n\n", task.Title))
		if task.Body != "" {
			prompt.WriteString(fmt.Sprintf("Context: %s\n\n", task.Body))
		}
		prompt.WriteString(`Provide:
1. Clear analysis of the question/problem
2. Key considerations and tradeoffs
3. Recommended approach
4. Concrete next steps

Think deeply but be actionable.
When finished, output: TASK_COMPLETE`)

	default:
		// Generic task
		if memories != "" {
			prompt.WriteString(memories)
		}
		prompt.WriteString(fmt.Sprintf("Task: %s\n\n", task.Title))
		if task.Body != "" {
			prompt.WriteString(fmt.Sprintf("%s\n\n", task.Body))
		}
		prompt.WriteString("When finished, output: TASK_COMPLETE\n")
		prompt.WriteString("If you need input, output: NEEDS_INPUT: followed by your question")
	}

	return prompt.String()
}

type execResult struct {
	Success     bool
	NeedsInput  bool
	Interrupted bool
	Message     string
}

// runCrush runs a task using Crush CLI
func (e *Executor) runCrush(ctx context.Context, taskID int64, workDir, prompt string) execResult {
	args := []string{
		"run",
		"-c", workDir,
		"-q", // quiet mode (hide spinner)
		prompt,
	}

	cmd := exec.CommandContext(ctx, "crush", args...)
	cmd.Dir = workDir

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

	// Monitor for DB-based interrupt (cross-process)
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
				if err == nil && task != nil && task.Status == db.StatusInterrupted {
					// Kill the process
					if cmd.Process != nil {
						cmd.Process.Kill()
					}
					close(interruptCh)
					return
				}
			}
		}
	}()

	// Track output for marker detection
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
			// Check for markers in each line as they come in
			if strings.Contains(line, "TASK_COMPLETE") {
				foundComplete = true
			}
			if strings.Contains(line, "NEEDS_INPUT:") {
				// Extract the question - could be on same line or following lines
				if idx := strings.Index(line, "NEEDS_INPUT:"); idx >= 0 {
					needsInputMsg = strings.TrimSpace(line[idx+len("NEEDS_INPUT:"):])
				}
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

	// Check if interrupted (via context or DB status)
	select {
	case <-interruptCh:
		return execResult{Interrupted: true}
	default:
	}
	if ctx.Err() == context.Canceled {
		return execResult{Interrupted: true}
	}

	// Check for completion markers
	mu.Lock()
	defer mu.Unlock()

	if foundComplete {
		return execResult{Success: true}
	}
	if needsInputMsg != "" {
		return execResult{NeedsInput: true, Message: needsInputMsg}
	}

	// Also check last few lines in case markers were output without prefix detection
	for i := len(allOutput) - 1; i >= 0 && i >= len(allOutput)-5; i-- {
		line := allOutput[i]
		if strings.Contains(line, "TASK_COMPLETE") {
			return execResult{Success: true}
		}
		if strings.Contains(line, "NEEDS_INPUT") {
			msg := line
			if idx := strings.Index(line, "NEEDS_INPUT:"); idx >= 0 {
				msg = strings.TrimSpace(line[idx+len("NEEDS_INPUT:"):])
			}
			return execResult{NeedsInput: true, Message: msg}
		}
	}

	if err != nil {
		return execResult{Message: fmt.Sprintf("crush exited: %v", err)}
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
