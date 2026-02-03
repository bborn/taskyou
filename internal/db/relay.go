package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// RelayMessage represents a stored relay message.
type RelayMessage struct {
	ID          string
	From        string
	To          string
	Content     string
	TaskID      int64
	Status      string // pending, delivered, read
	CreatedAt   LocalTime
	DeliveredAt *LocalTime
	ReadAt      *LocalTime
}

// SaveRelayMessage stores a relay message.
func (db *DB) SaveRelayMessage(msg *RelayMessage) error {
	_, err := db.Exec(`
		INSERT INTO relay_messages (id, from_agent, to_agent, content, task_id, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, msg.ID, msg.From, msg.To, msg.Content, msg.TaskID, msg.Status, msg.CreatedAt.Time)
	if err != nil {
		return fmt.Errorf("insert relay message: %w", err)
	}
	return nil
}

// GetRelayMessage retrieves a message by ID.
func (db *DB) GetRelayMessage(id string) (*RelayMessage, error) {
	msg := &RelayMessage{}
	err := db.QueryRow(`
		SELECT id, from_agent, to_agent, content, COALESCE(task_id, 0), status,
		       created_at, delivered_at, read_at
		FROM relay_messages WHERE id = ?
	`, id).Scan(
		&msg.ID, &msg.From, &msg.To, &msg.Content, &msg.TaskID, &msg.Status,
		&msg.CreatedAt, &msg.DeliveredAt, &msg.ReadAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query relay message: %w", err)
	}
	return msg, nil
}

// GetRelayMessagesForAgent retrieves messages for an agent (direct or broadcast).
func (db *DB) GetRelayMessagesForAgent(agentName string, limit int) ([]*RelayMessage, error) {
	normalized := strings.ToLower(strings.TrimSpace(agentName))
	rows, err := db.Query(`
		SELECT id, from_agent, to_agent, content, COALESCE(task_id, 0), status,
		       created_at, delivered_at, read_at
		FROM relay_messages
		WHERE LOWER(to_agent) = ? OR to_agent = '*'
		ORDER BY created_at DESC
		LIMIT ?
	`, normalized, limit)
	if err != nil {
		return nil, fmt.Errorf("query relay messages: %w", err)
	}
	defer rows.Close()

	var messages []*RelayMessage
	for rows.Next() {
		msg := &RelayMessage{}
		if err := rows.Scan(
			&msg.ID, &msg.From, &msg.To, &msg.Content, &msg.TaskID, &msg.Status,
			&msg.CreatedAt, &msg.DeliveredAt, &msg.ReadAt,
		); err != nil {
			return nil, fmt.Errorf("scan relay message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// GetPendingRelayMessages retrieves pending messages for an agent.
func (db *DB) GetPendingRelayMessages(agentName string) ([]*RelayMessage, error) {
	normalized := strings.ToLower(strings.TrimSpace(agentName))
	rows, err := db.Query(`
		SELECT id, from_agent, to_agent, content, COALESCE(task_id, 0), status,
		       created_at, delivered_at, read_at
		FROM relay_messages
		WHERE (LOWER(to_agent) = ? OR to_agent = '*') AND status = 'pending'
		ORDER BY created_at ASC
	`, normalized)
	if err != nil {
		return nil, fmt.Errorf("query pending relay messages: %w", err)
	}
	defer rows.Close()

	var messages []*RelayMessage
	for rows.Next() {
		msg := &RelayMessage{}
		if err := rows.Scan(
			&msg.ID, &msg.From, &msg.To, &msg.Content, &msg.TaskID, &msg.Status,
			&msg.CreatedAt, &msg.DeliveredAt, &msg.ReadAt,
		); err != nil {
			return nil, fmt.Errorf("scan relay message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// MarkRelayMessageDelivered marks a message as delivered.
func (db *DB) MarkRelayMessageDelivered(id string) error {
	_, err := db.Exec(`
		UPDATE relay_messages SET status = 'delivered', delivered_at = ? WHERE id = ?
	`, time.Now(), id)
	return err
}

// MarkRelayMessageRead marks a message as read.
func (db *DB) MarkRelayMessageRead(id string) error {
	_, err := db.Exec(`
		UPDATE relay_messages SET status = 'read', read_at = ? WHERE id = ?
	`, time.Now(), id)
	return err
}
