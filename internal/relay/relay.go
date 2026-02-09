// Package relay provides simple agent-to-agent messaging.
package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Message represents a relay message.
type Message struct {
	ID        string     `json:"id"`
	From      string     `json:"from"`
	To        string     `json:"to"`        // agent name, #channel, or * for broadcast
	Content   string     `json:"content"`
	TaskID    int64      `json:"task_id"`   // sender's task
	Status    string     `json:"status"`    // pending, delivered, read
	CreatedAt time.Time  `json:"created_at"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
}

// Agent represents a connected agent.
type Agent struct {
	Name      string    `json:"name"`
	TaskID    int64     `json:"task_id"`
	Status    string    `json:"status"` // active, idle
	LastSeen  time.Time `json:"last_seen"`
}

// Relay manages agent messaging.
type Relay struct {
	mu       sync.RWMutex
	agents   map[string]*Agent  // normalized name -> agent
	messages []*Message         // in-memory queue (also persisted to DB)
	store    MessageStore       // persistence layer
}

// MessageStore is the interface for message persistence.
type MessageStore interface {
	SaveMessage(msg *Message) error
	GetMessage(id string) (*Message, error)
	GetMessagesForAgent(agentName string, limit int) ([]*Message, error)
	GetPendingMessages(agentName string) ([]*Message, error)
	MarkDelivered(id string) error
	MarkRead(id string) error
}

// New creates a new Relay.
func New(store MessageStore) *Relay {
	return &Relay{
		agents: make(map[string]*Agent),
		store:  store,
	}
}

// Register registers an agent.
func (r *Relay) Register(name string, taskID int64) *Agent {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := normalize(name)
	agent := &Agent{
		Name:     name,
		TaskID:   taskID,
		Status:   "active",
		LastSeen: time.Now(),
	}
	r.agents[key] = agent
	return agent
}

// Unregister removes an agent.
func (r *Relay) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, normalize(name))
}

// UpdateStatus updates an agent's status.
func (r *Relay) UpdateStatus(name, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if agent, ok := r.agents[normalize(name)]; ok {
		agent.Status = status
		agent.LastSeen = time.Now()
	}
}

// GetAgent returns an agent by name.
func (r *Relay) GetAgent(name string) *Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[normalize(name)]
}

// ListAgents returns all registered agents.
func (r *Relay) ListAgents() []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]*Agent, 0, len(r.agents))
	for _, a := range r.agents {
		agents = append(agents, a)
	}
	return agents
}

// Send sends a message. Returns the message ID.
func (r *Relay) Send(from, to, content string, fromTaskID int64) (string, error) {
	msg := &Message{
		ID:        uuid.New().String()[:8], // short ID for easy reference
		From:      from,
		To:        to,
		Content:   content,
		TaskID:    fromTaskID,
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	if r.store != nil {
		if err := r.store.SaveMessage(msg); err != nil {
			return "", fmt.Errorf("save message: %w", err)
		}
	}

	r.mu.Lock()
	r.messages = append(r.messages, msg)
	r.mu.Unlock()

	return msg.ID, nil
}

// GetPendingMessages returns pending messages for an agent.
func (r *Relay) GetPendingMessages(agentName string) []*Message {
	// If store is available, use it for single source of truth (handles multi-process)
	if r.store != nil {
		if msgs, err := r.store.GetPendingMessages(agentName); err == nil {
			return msgs
		}
		// Fallback to memory if store fails? Or just return empty?
		// For now fall back to memory which might contain unpersisted messages
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	key := normalize(agentName)
	var pending []*Message

	for _, msg := range r.messages {
		if msg.Status != "pending" {
			continue
		}
		// Match direct, broadcast, or channel
		if msg.To == "*" || normalize(msg.To) == key {
			pending = append(pending, msg)
		}
	}
	return pending
}

// MarkDelivered marks a message as delivered.
func (r *Relay) MarkDelivered(msgID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, msg := range r.messages {
		if msg.ID == msgID {
			msg.Status = "delivered"
			if r.store != nil {
				r.store.MarkDelivered(msgID)
			}
			break
		}
	}
}

// GetMessage retrieves a message by ID.
func (r *Relay) GetMessage(id string) *Message {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, msg := range r.messages {
		if msg.ID == id {
			return msg
		}
	}

	// Try store if not in memory
	if r.store != nil {
		msg, _ := r.store.GetMessage(id)
		return msg
	}
	return nil
}

// ToJSON converts a message to JSON for logging.
func (m *Message) ToJSON() string {
	b, _ := json.Marshal(m)
	return string(b)
}

// FromJSON parses a message from JSON.
func FromJSON(data string) (*Message, error) {
	var m Message
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// FormatForInjection formats a message for terminal injection.
func (m *Message) FormatForInjection() string {
	content := m.Content
	truncated := ""
	if len(content) > 500 {
		content = content[:500] + "..."
		truncated = fmt.Sprintf(" (truncated, full: ty relay read %s)", m.ID)
	}
	return fmt.Sprintf("\n[RELAY from %s%s]\n%s\n[/RELAY]\n", m.From, truncated, content)
}

// ParseRelayCommand parses "->relay:target message" format.
// Returns (target, message, ok).
func ParseRelayCommand(input string) (string, string, bool) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "->relay:") {
		return "", "", false
	}

	rest := strings.TrimPrefix(input, "->relay:")
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) < 2 {
		return "", "", false
	}

	target := strings.TrimSpace(parts[0])
	message := strings.TrimSpace(parts[1])
	return target, message, target != "" && message != ""
}

func normalize(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// CleanAgentName cleans a raw name (like task title) for use as an agent name.
// It removes special characters, replaces spaces with hyphens, limits length,
// and normalizes to lowercase.
func CleanAgentName(rawName string, fallback string) string {
	name := rawName
	if name == "" {
		name = fallback
	}
	// Clean up for use as agent name (keep alphanumeric, hyphen, underscore)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		if r == ' ' {
			return '-'
		}
		return -1
	}, name)
	// Limit length
	if len(name) > 32 {
		name = name[:32]
	}
	// Normalize to lowercase for consistent matching
	return strings.ToLower(strings.TrimSpace(name))
}

// GetAgentByTaskID finds an agent by their task ID.
func (r *Relay) GetAgentByTaskID(taskID int64) *Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, a := range r.agents {
		if a.TaskID == taskID {
			return a
		}
	}
	return nil
}

// Heartbeat updates an agent's last seen time.
func (r *Relay) Heartbeat(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if agent, ok := r.agents[normalize(name)]; ok {
		agent.LastSeen = time.Now()
	}
}
