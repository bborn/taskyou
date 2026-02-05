package executor

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/relay"
)

// RelayManager handles agent-to-agent messaging for the executor.
type RelayManager struct {
	mu       sync.RWMutex
	relay    *relay.Relay
	executor *Executor
	store    *relay.DBStore

	// Track last activity per task for idle detection
	lastActivity map[int64]time.Time
}

// NewRelayManager creates a relay manager for the executor.
func NewRelayManager(e *Executor) *RelayManager {
	store := relay.NewDBStore(e.db)
	return &RelayManager{
		relay:        relay.New(store),
		executor:     e,
		store:        store,
		lastActivity: make(map[int64]time.Time),
	}
}

// RegisterAgent registers a task as an agent.
// Uses task title as the agent name.
func (rm *RelayManager) RegisterAgent(task *db.Task) {
	name := agentName(task)
	rm.relay.Register(name, task.ID)
	rm.executor.logger.Info("Registered relay agent", "name", name, "task", task.ID)
}

// UnregisterAgent removes a task from the agent registry.
func (rm *RelayManager) UnregisterAgent(taskID int64) {
	task, _ := rm.executor.db.GetTask(taskID)
	if task != nil {
		name := agentName(task)
		rm.relay.Unregister(name)
		rm.executor.logger.Info("Unregistered relay agent", "name", name, "task", task.ID)
	}
}

// agentName derives agent name from task using the shared relay.CleanAgentName function.
func agentName(task *db.Task) string {
	fallback := fmt.Sprintf("task-%d", task.ID)
	return relay.CleanAgentName(task.Title, fallback)
}

// Send sends a message from one agent to another.
func (rm *RelayManager) Send(fromTaskID int64, to, content string) (string, error) {
	task, err := rm.executor.db.GetTask(fromTaskID)
	if err != nil || task == nil {
		return "", fmt.Errorf("sender task not found")
	}

	from := agentName(task)
	msgID, err := rm.relay.Send(from, to, content, fromTaskID)
	if err != nil {
		return "", err
	}

	rm.executor.logLine(fromTaskID, "relay", fmt.Sprintf("Sent to %s: %s", to, truncate(content, 100)))
	return msgID, nil
}

// SendFromCLI sends a message from the CLI (not from a task).
func (rm *RelayManager) SendFromCLI(from, to, content string) (string, error) {
	return rm.relay.Send(from, to, content, 0)
}

// RecordActivity records that a task had output activity.
func (rm *RelayManager) RecordActivity(taskID int64) {
	rm.mu.Lock()
	rm.lastActivity[taskID] = time.Now()
	rm.mu.Unlock()
}

// ClearActivity removes activity tracking for a task (for cleanup).
func (rm *RelayManager) ClearActivity(taskID int64) {
	rm.mu.Lock()
	delete(rm.lastActivity, taskID)
	rm.mu.Unlock()
}

// IsIdle checks if a task has been idle for the given duration.
func (rm *RelayManager) IsIdle(taskID int64, idleDuration time.Duration) bool {
	rm.mu.RLock()
	lastActive, ok := rm.lastActivity[taskID]
	rm.mu.RUnlock()

	if !ok {
		return true // No activity recorded means idle
	}
	return time.Since(lastActive) >= idleDuration
}

// DeliverPendingMessages checks for pending messages and delivers them to idle agents.
func (rm *RelayManager) DeliverPendingMessages(ctx context.Context) {
	agents := rm.relay.ListAgents()

	for _, agent := range agents {
		// Check if agent's task is idle (no output for 1.5 seconds)
		if !rm.IsIdle(agent.TaskID, 1500*time.Millisecond) {
			continue
		}

		// Get pending messages
		messages := rm.relay.GetPendingMessages(agent.Name)
		if len(messages) == 0 {
			continue
		}

		// Deliver each message
		for _, msg := range messages {
			if err := rm.injectMessage(ctx, agent.TaskID, msg); err != nil {
				rm.executor.logger.Error("Failed to inject relay message", "error", err, "task", agent.TaskID)
				continue
			}
			rm.relay.MarkDelivered(msg.ID)
			rm.executor.logLine(agent.TaskID, "relay", fmt.Sprintf("Received from %s: %s", msg.From, truncate(msg.Content, 100)))
		}
	}
}

// injectMessage injects a relay message into the task's tmux pane.
func (rm *RelayManager) injectMessage(ctx context.Context, taskID int64, msg *relay.Message) error {
	task, err := rm.executor.db.GetTask(taskID)
	if err != nil || task == nil {
		return fmt.Errorf("task not found")
	}

	// Get the Claude pane ID
	paneID := task.ClaudePaneID
	if paneID == "" {
		return fmt.Errorf("no pane ID for task %d", taskID)
	}

	// Format the message for injection
	formatted := msg.FormatForInjection()

	// Use tmux send-keys to inject the message
	cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", paneID, formatted, "")
	return cmd.Run()
}

// GetMessage retrieves a message by ID.
func (rm *RelayManager) GetMessage(id string) *relay.Message {
	return rm.relay.GetMessage(id)
}

// ListAgents returns all registered agents.
func (rm *RelayManager) ListAgents() []*relay.Agent {
	return rm.relay.ListAgents()
}

// GetAgentByName returns an agent by name.
func (rm *RelayManager) GetAgentByName(name string) *relay.Agent {
	return rm.relay.GetAgent(name)
}

// GetMessagesForAgent retrieves messages for an agent.
func (rm *RelayManager) GetMessagesForAgent(agentName string, limit int) ([]*relay.Message, error) {
	return rm.store.GetMessagesForAgent(agentName, limit)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
