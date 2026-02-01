// Package events provides a centralized event system for task lifecycle events.
// It supports multiple delivery mechanisms:
// - In-process channels (for real-time UI updates)
// - Script hooks (for local automation)
// - Webhooks (for external integrations)
// - Event log (for audit trail and debugging)
package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/log"
)

// Event types for task lifecycle
const (
	EventTaskCreated       = "task.created"
	EventTaskUpdated       = "task.updated"
	EventTaskDeleted       = "task.deleted"
	EventTaskStatusChanged = "task.status.changed"
	EventTaskQueued        = "task.queued"
	EventTaskStarted       = "task.started"
	EventTaskProcessing    = "task.processing"
	EventTaskBlocked       = "task.blocked"
	EventTaskCompleted     = "task.completed"
	EventTaskFailed        = "task.failed"
	EventTaskRetried       = "task.retried"
	EventTaskInterrupted   = "task.interrupted"
	EventTaskPinned        = "task.pinned"
	EventTaskUnpinned      = "task.unpinned"
)

// Event represents a task event with its metadata.
type Event struct {
	Type      string                 `json:"type"`
	TaskID    int64                  `json:"task_id"`
	Task      *db.Task               `json:"task,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// Manager coordinates event delivery across multiple channels.
type Manager struct {
	db       *db.DB
	logger   *log.Logger
	hooksDir string
	webhooks []string // List of webhook URLs to POST events to

	// In-process subscribers
	mu   sync.RWMutex
	subs []chan Event

	// Background worker
	eventQueue chan Event
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// New creates a new event manager.
func New(database *db.DB, hooksDir string) *Manager {
	m := &Manager{
		db:         database,
		logger:     log.NewWithOptions(os.Stderr, log.Options{Prefix: "events"}),
		hooksDir:   hooksDir,
		webhooks:   []string{},
		subs:       make([]chan Event, 0),
		eventQueue: make(chan Event, 1000), // Buffered to prevent blocking
		stopCh:     make(chan struct{}),
	}

	// Load webhook URLs from database
	m.loadWebhooks()

	// Start background worker for async event delivery
	m.wg.Add(1)
	go m.worker()

	return m
}

// NewSilent creates an event manager without logging (for testing).
func NewSilent(database *db.DB, hooksDir string) *Manager {
	m := New(database, hooksDir)
	m.logger = log.NewWithOptions(io.Discard, log.Options{Level: log.FatalLevel})
	return m
}

// loadWebhooks loads configured webhook URLs from database settings.
func (m *Manager) loadWebhooks() {
	webhooksJSON, err := m.db.GetSetting("event_webhooks")
	if err != nil || webhooksJSON == "" {
		return
	}

	var urls []string
	if err := json.Unmarshal([]byte(webhooksJSON), &urls); err != nil {
		m.logger.Error("Failed to parse webhooks", "error", err)
		return
	}

	m.webhooks = urls
}

// AddWebhook adds a webhook URL and persists it to database.
func (m *Manager) AddWebhook(url string) error {
	m.webhooks = append(m.webhooks, url)
	return m.saveWebhooks()
}

// RemoveWebhook removes a webhook URL and persists to database.
func (m *Manager) RemoveWebhook(url string) error {
	for i, w := range m.webhooks {
		if w == url {
			m.webhooks = append(m.webhooks[:i], m.webhooks[i+1:]...)
			return m.saveWebhooks()
		}
	}
	return nil
}

// ListWebhooks returns configured webhook URLs.
func (m *Manager) ListWebhooks() []string {
	return append([]string{}, m.webhooks...) // Return a copy
}

// saveWebhooks persists webhook URLs to database.
func (m *Manager) saveWebhooks() error {
	data, err := json.Marshal(m.webhooks)
	if err != nil {
		return err
	}
	return m.db.SetSetting("event_webhooks", string(data))
}

// Emit sends an event to all configured delivery channels.
// This is async - it queues the event and returns immediately.
func (m *Manager) Emit(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Queue event for async delivery (non-blocking)
	select {
	case m.eventQueue <- event:
	default:
		// Queue full - log but don't block
		m.logger.Warn("Event queue full, dropping event", "type", event.Type, "task_id", event.TaskID)
	}
}

// EmitSync sends an event synchronously (for critical events where delivery must be guaranteed).
func (m *Manager) EmitSync(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	m.deliverEvent(event)
}

// worker processes events from the queue in the background.
func (m *Manager) worker() {
	defer m.wg.Done()

	for {
		select {
		case <-m.stopCh:
			// Drain remaining events
			for {
				select {
				case event := <-m.eventQueue:
					m.deliverEvent(event)
				default:
					return
				}
			}
		case event := <-m.eventQueue:
			m.deliverEvent(event)
		}
	}
}

// deliverEvent handles actual event delivery to all channels.
func (m *Manager) deliverEvent(event Event) {
	// 1. Broadcast to in-process subscribers
	m.broadcastToSubscribers(event)

	// 2. Log to database (async, don't block on errors)
	if err := m.logToDatabase(event); err != nil {
		m.logger.Debug("Failed to log event to database", "error", err)
	}

	// 3. Execute script hooks (async)
	go m.executeHook(event)

	// 4. Send webhooks (async)
	for _, url := range m.webhooks {
		go m.sendWebhook(url, event)
	}
}

// broadcastToSubscribers sends event to all in-process channel subscribers.
func (m *Manager) broadcastToSubscribers(event Event) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ch := range m.subs {
		select {
		case ch <- event:
		default:
			// Channel full, skip
		}
	}
}

// logToDatabase stores the event in the event_log table.
func (m *Manager) logToDatabase(event Event) error {
	metadataJSON := "{}"
	if len(event.Metadata) > 0 {
		data, err := json.Marshal(event.Metadata)
		if err == nil {
			metadataJSON = string(data)
		}
	}

	_, err := m.db.Exec(`
		INSERT INTO event_log (event_type, task_id, message, metadata, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, event.Type, event.TaskID, event.Message, metadataJSON, event.Timestamp)

	return err
}

// executeHook runs the appropriate script hook if it exists.
func (m *Manager) executeHook(event Event) {
	if m.hooksDir == "" {
		return
	}

	// Map event types to hook script names
	hookName := event.Type // e.g., "task.created"

	hookPath := filepath.Join(m.hooksDir, hookName)
	if _, err := os.Stat(hookPath); os.IsNotExist(err) {
		return
	}

	// Build environment variables
	env := append(os.Environ(),
		fmt.Sprintf("TASK_ID=%d", event.TaskID),
		fmt.Sprintf("TASK_EVENT=%s", event.Type),
		fmt.Sprintf("TASK_MESSAGE=%s", event.Message),
		fmt.Sprintf("TASK_TIMESTAMP=%s", event.Timestamp.Format(time.RFC3339)),
	)

	// Add task details if available
	if event.Task != nil {
		env = append(env,
			fmt.Sprintf("TASK_TITLE=%s", event.Task.Title),
			fmt.Sprintf("TASK_STATUS=%s", event.Task.Status),
			fmt.Sprintf("TASK_PROJECT=%s", event.Task.Project),
			fmt.Sprintf("TASK_TYPE=%s", event.Task.Type),
			fmt.Sprintf("TASK_EXECUTOR=%s", event.Task.Executor),
		)
	}

	// Add metadata as JSON
	if len(event.Metadata) > 0 {
		if data, err := json.Marshal(event.Metadata); err == nil {
			env = append(env, fmt.Sprintf("TASK_METADATA=%s", string(data)))
		}
	}

	// Execute hook with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, hookPath)
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("Hook failed", "event", event.Type, "error", err, "output", string(output))
	} else {
		m.logger.Debug("Hook executed", "event", event.Type)
	}
}

// sendWebhook sends an HTTP POST request to a webhook URL.
func (m *Manager) sendWebhook(url string, event Event) {
	payload, err := json.Marshal(event)
	if err != nil {
		m.logger.Error("Failed to marshal event for webhook", "url", url, "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		m.logger.Error("Failed to create webhook request", "url", url, "error", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TaskYou/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.logger.Error("Webhook delivery failed", "url", url, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		m.logger.Warn("Webhook returned error status", "url", url, "status", resp.StatusCode, "body", string(body))
	} else {
		m.logger.Debug("Webhook delivered", "url", url, "status", resp.StatusCode)
	}
}

// Subscribe returns a channel that receives all events.
func (m *Manager) Subscribe() chan Event {
	ch := make(chan Event, 100)
	m.mu.Lock()
	m.subs = append(m.subs, ch)
	m.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (m *Manager) Unsubscribe(ch chan Event) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, sub := range m.subs {
		if sub == ch {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

// Stop gracefully shuts down the event manager.
func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// Helper methods for common events

// EmitTaskCreated emits a task.created event.
func (m *Manager) EmitTaskCreated(task *db.Task) {
	m.Emit(Event{
		Type:    EventTaskCreated,
		TaskID:  task.ID,
		Task:    task,
		Message: fmt.Sprintf("Task created: %s", task.Title),
	})
}

// EmitTaskUpdated emits a task.updated event.
func (m *Manager) EmitTaskUpdated(task *db.Task, changes map[string]interface{}) {
	m.Emit(Event{
		Type:     EventTaskUpdated,
		TaskID:   task.ID,
		Task:     task,
		Message:  "Task updated",
		Metadata: changes,
	})
}

// EmitTaskDeleted emits a task.deleted event.
func (m *Manager) EmitTaskDeleted(taskID int64, title string) {
	m.Emit(Event{
		Type:    EventTaskDeleted,
		TaskID:  taskID,
		Message: fmt.Sprintf("Task deleted: %s", title),
	})
}

// EmitTaskStatusChanged emits a task.status.changed event.
func (m *Manager) EmitTaskStatusChanged(task *db.Task, oldStatus, newStatus string) {
	m.Emit(Event{
		Type:   EventTaskStatusChanged,
		TaskID: task.ID,
		Task:   task,
		Message: fmt.Sprintf("Status changed: %s → %s", oldStatus, newStatus),
		Metadata: map[string]interface{}{
			"old_status": oldStatus,
			"new_status": newStatus,
		},
	})
}

// EmitTaskQueued emits a task.queued event.
func (m *Manager) EmitTaskQueued(task *db.Task) {
	m.Emit(Event{
		Type:    EventTaskQueued,
		TaskID:  task.ID,
		Task:    task,
		Message: "Task queued for execution",
	})
}

// EmitTaskStarted emits a task.started event.
func (m *Manager) EmitTaskStarted(task *db.Task) {
	m.Emit(Event{
		Type:    EventTaskStarted,
		TaskID:  task.ID,
		Task:    task,
		Message: fmt.Sprintf("Task started: %s", task.Title),
	})
}

// EmitTaskProcessing emits a task.processing event.
func (m *Manager) EmitTaskProcessing(task *db.Task) {
	m.Emit(Event{
		Type:    EventTaskProcessing,
		TaskID:  task.ID,
		Task:    task,
		Message: "Task is being processed",
	})
}

// EmitTaskBlocked emits a task.blocked event.
func (m *Manager) EmitTaskBlocked(task *db.Task, reason string) {
	m.Emit(Event{
		Type:    EventTaskBlocked,
		TaskID:  task.ID,
		Task:    task,
		Message: reason,
	})
}

// EmitTaskCompleted emits a task.completed event.
func (m *Manager) EmitTaskCompleted(task *db.Task) {
	m.Emit(Event{
		Type:    EventTaskCompleted,
		TaskID:  task.ID,
		Task:    task,
		Message: "Task completed successfully",
	})
}

// EmitTaskFailed emits a task.failed event.
func (m *Manager) EmitTaskFailed(task *db.Task, reason string) {
	m.Emit(Event{
		Type:    EventTaskFailed,
		TaskID:  task.ID,
		Task:    task,
		Message: reason,
	})
}

// EmitTaskRetried emits a task.retried event.
func (m *Manager) EmitTaskRetried(task *db.Task, feedback string) {
	m.Emit(Event{
		Type:    EventTaskRetried,
		TaskID:  task.ID,
		Task:    task,
		Message: "Task retried with feedback",
		Metadata: map[string]interface{}{
			"feedback": feedback,
		},
	})
}

// EmitTaskInterrupted emits a task.interrupted event.
func (m *Manager) EmitTaskInterrupted(task *db.Task) {
	m.Emit(Event{
		Type:    EventTaskInterrupted,
		TaskID:  task.ID,
		Task:    task,
		Message: "Task interrupted by user",
	})
}

// EmitTaskPinned emits a task.pinned event.
func (m *Manager) EmitTaskPinned(task *db.Task) {
	m.Emit(Event{
		Type:    EventTaskPinned,
		TaskID:  task.ID,
		Task:    task,
		Message: "Task pinned",
	})
}

// EmitTaskUnpinned emits a task.unpinned event.
func (m *Manager) EmitTaskUnpinned(task *db.Task) {
	m.Emit(Event{
		Type:    EventTaskUnpinned,
		TaskID:  task.ID,
		Task:    task,
		Message: "Task unpinned",
	})
}
