package relay

import (
	"time"

	"github.com/bborn/workflow/internal/db"
)

// DBStore adapts db.DB to MessageStore interface.
type DBStore struct {
	db *db.DB
}

// NewDBStore creates a new database-backed message store.
func NewDBStore(database *db.DB) *DBStore {
	return &DBStore{db: database}
}

// SaveMessage persists a message to the database.
func (s *DBStore) SaveMessage(msg *Message) error {
	dbMsg := &db.RelayMessage{
		ID:      msg.ID,
		From:    msg.From,
		To:      msg.To,
		Content: msg.Content,
		TaskID:  msg.TaskID,
		Status:  msg.Status,
		CreatedAt: db.LocalTime{Time: msg.CreatedAt},
	}
	return s.db.SaveRelayMessage(dbMsg)
}

// GetMessage retrieves a message by ID.
func (s *DBStore) GetMessage(id string) (*Message, error) {
	dbMsg, err := s.db.GetRelayMessage(id)
	if err != nil || dbMsg == nil {
		return nil, err
	}
	return dbToMessage(dbMsg), nil
}

// GetMessagesForAgent retrieves messages for an agent.
func (s *DBStore) GetMessagesForAgent(agentName string, limit int) ([]*Message, error) {
	dbMsgs, err := s.db.GetRelayMessagesForAgent(agentName, limit)
	if err != nil {
		return nil, err
	}

	messages := make([]*Message, len(dbMsgs))
	for i, m := range dbMsgs {
		messages[i] = dbToMessage(m)
	}
	return messages, nil
}

// MarkDelivered marks a message as delivered.
func (s *DBStore) MarkDelivered(id string) error {
	return s.db.MarkRelayMessageDelivered(id)
}

// MarkRead marks a message as read.
func (s *DBStore) MarkRead(id string) error {
	return s.db.MarkRelayMessageRead(id)
}

func dbToMessage(m *db.RelayMessage) *Message {
	msg := &Message{
		ID:        m.ID,
		From:      m.From,
		To:        m.To,
		Content:   m.Content,
		TaskID:    m.TaskID,
		Status:    m.Status,
		CreatedAt: m.CreatedAt.Time,
	}
	if m.ReadAt != nil {
		t := m.ReadAt.Time
		msg.ReadAt = &t
	}
	return msg
}

// LoadPendingMessages loads pending messages from the database into the relay.
func (r *Relay) LoadPendingMessages(database *db.DB) error {
	// Get all agents and load their pending messages
	r.mu.Lock()
	agents := make([]string, 0, len(r.agents))
	for _, a := range r.agents {
		agents = append(agents, a.Name)
	}
	r.mu.Unlock()

	for _, name := range agents {
		msgs, err := database.GetPendingRelayMessages(name)
		if err != nil {
			return err
		}
		r.mu.Lock()
		for _, m := range msgs {
			r.messages = append(r.messages, &Message{
				ID:        m.ID,
				From:      m.From,
				To:        m.To,
				Content:   m.Content,
				TaskID:    m.TaskID,
				Status:    m.Status,
				CreatedAt: m.CreatedAt.Time,
			})
		}
		r.mu.Unlock()
	}
	return nil
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
