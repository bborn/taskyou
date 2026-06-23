package db

import (
	"encoding/json"
	"fmt"
)

// TaskTimelineEntry is a single chronological entry in a task's activity
// timeline, derived from a row in the event_log table. It carries a short,
// human-readable Label (e.g. "queued → processing") plus the raw event type
// and timestamp so callers can render a compact lifecycle view.
type TaskTimelineEntry struct {
	ID        int64     `json:"id"`
	EventType string    `json:"event_type"`
	Label     string    `json:"label"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt LocalTime `json:"created_at"`
}

// GetTaskTimeline returns a task's activity timeline in chronological order
// (oldest first), built from the event_log. limit caps the number of rows; a
// non-positive limit defaults to 200. The newest `limit` events are selected
// but returned oldest-first so the timeline reads top-to-bottom.
func (db *DB) GetTaskTimeline(taskID int64, limit int) ([]TaskTimelineEntry, error) {
	if limit <= 0 {
		limit = 200
	}

	rows, err := db.Query(`
		SELECT id, event_type, COALESCE(message, ''), COALESCE(metadata, '{}'), created_at
		FROM (
			SELECT id, event_type, message, metadata, created_at
			FROM event_log
			WHERE task_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		)
		ORDER BY created_at ASC, id ASC
	`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("query task timeline: %w", err)
	}
	defer rows.Close()

	var entries []TaskTimelineEntry
	for rows.Next() {
		var (
			id        int64
			eventType string
			message   string
			metadata  string
			createdAt LocalTime
		)
		if err := rows.Scan(&id, &eventType, &message, &metadata, &createdAt); err != nil {
			return nil, fmt.Errorf("scan timeline row: %w", err)
		}
		label, detail := timelineLabel(eventType, message, metadata)
		entries = append(entries, TaskTimelineEntry{
			ID:        id,
			EventType: eventType,
			Label:     label,
			Detail:    detail,
			CreatedAt: createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate timeline rows: %w", err)
	}
	return entries, nil
}

// timelineLabel maps a raw event_log row into a short label and optional detail
// suitable for the activity timeline. It understands the status-transition
// metadata that emitTaskUpdated records so a re-queue/processing/blocked/done
// change renders as e.g. "queued → processing".
func timelineLabel(eventType, message, metadata string) (label, detail string) {
	switch eventType {
	case "task.created":
		return "Created", message
	case "task.completed":
		return "Completed", ""
	case "task.blocked":
		// reason is carried in the message; "status change" is the generic
		// transition reason and adds nothing, so suppress it as detail.
		if message != "" && message != "status change" {
			return "Blocked", message
		}
		return "Blocked", ""
	case "task.retry":
		return "Retried", message
	case "task.deleted":
		return "Deleted", ""
	case "task.updated":
		if from, to, ok := statusTransition(metadata); ok {
			return fmt.Sprintf("%s → %s", from, to), ""
		}
		// A non-status update (title/body/etc). Name the changed fields if we
		// can so the entry is meaningful rather than a bare "Updated".
		if fields := changedFields(metadata); fields != "" {
			return "Updated", fields
		}
		return "Updated", ""
	default:
		// Unknown event type: fall back to the raw type as the label.
		return eventType, message
	}
}

// statusTransition extracts old/new status values from an event_log metadata
// blob of the form {"status":{"old":"queued","new":"processing"}}.
func statusTransition(metadata string) (from, to string, ok bool) {
	if metadata == "" || metadata == "{}" {
		return "", "", false
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadata), &parsed); err != nil {
		return "", "", false
	}
	raw, exists := parsed["status"]
	if !exists {
		return "", "", false
	}
	var change struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := json.Unmarshal(raw, &change); err != nil {
		return "", "", false
	}
	if change.Old == "" && change.New == "" {
		return "", "", false
	}
	return change.Old, change.New, true
}

// changedFields returns a comma-separated list of the top-level keys present in
// a changes metadata blob (excluding status, which is handled separately).
func changedFields(metadata string) string {
	if metadata == "" || metadata == "{}" {
		return ""
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadata), &parsed); err != nil {
		return ""
	}
	var fields string
	for k := range parsed {
		if k == "status" {
			continue
		}
		if fields != "" {
			fields += ", "
		}
		fields += k
	}
	return fields
}
