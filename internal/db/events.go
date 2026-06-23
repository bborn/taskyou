package db

import "encoding/json"

// EventEmitter is an interface for emitting task events.
// This allows the DB to emit events without depending on the events package.
type EventEmitter interface {
	EmitTaskCreated(task *Task)
	EmitTaskUpdated(task *Task, changes map[string]interface{})
	EmitTaskDeleted(taskID int64, title string)
	EmitTaskPinned(task *Task)
	EmitTaskUnpinned(task *Task)
	EmitTaskBlocked(task *Task, reason string)
	EmitTaskCompleted(task *Task)
}

// recordEvent persists a task event to the event_log table. This is the
// change feed other processes poll (e.g. the HTTP API's SSE board stream
// reads MAX(id) to detect that the board changed), so it runs on every task
// mutation regardless of whether a hook emitter is configured.
func (db *DB) recordEvent(eventType string, taskID int64, message string) {
	db.recordEventMeta(eventType, taskID, message, "{}")
}

// recordEventMeta is like recordEvent but persists a JSON metadata blob
// alongside the event. The metadata is what the activity timeline reads to
// render rich labels (e.g. a status transition's old/new values). An empty
// string is normalized to "{}" so the column always holds valid JSON.
func (db *DB) recordEventMeta(eventType string, taskID int64, message, metadata string) {
	if metadata == "" {
		metadata = "{}"
	}
	// Best-effort: a failed event write must never fail the mutation itself.
	_, _ = db.Exec(`
		INSERT INTO event_log (event_type, task_id, message, metadata)
		VALUES (?, ?, ?, ?)
	`, eventType, taskID, message, metadata)
}

// marshalEventMetadata serializes a changes map for storage in event_log. It
// never returns an error: on failure it falls back to "{}" so event recording
// (which is best-effort) is never blocked by a bad value.
func marshalEventMetadata(changes map[string]interface{}) string {
	if len(changes) == 0 {
		return "{}"
	}
	b, err := json.Marshal(changes)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// SetEventEmitter sets the event emitter for this database.
// This is called by the executor to enable event emission.
func (db *DB) SetEventEmitter(emitter EventEmitter) {
	db.eventEmitter = emitter
}

// emitTaskCreated emits a task created event if an emitter is configured.
func (db *DB) emitTaskCreated(task *Task) {
	db.recordEvent("task.created", task.ID, task.Title)
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskCreated(task)
	}
}

// emitTaskUpdated emits a task updated event if an emitter is configured.
func (db *DB) emitTaskUpdated(task *Task, changes map[string]interface{}) {
	db.recordEventMeta("task.updated", task.ID, task.Title, marshalEventMetadata(changes))
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskUpdated(task, changes)
	}
}

// emitTaskDeleted emits a task deleted event if an emitter is configured.
func (db *DB) emitTaskDeleted(taskID int64, title string) {
	db.recordEvent("task.deleted", taskID, title)
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskDeleted(taskID, title)
	}
}

// emitTaskPinned emits a task pinned event if an emitter is configured.
func (db *DB) emitTaskPinned(task *Task) {
	db.recordEvent("task.updated", task.ID, "pinned")
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskPinned(task)
	}
}

// emitTaskUnpinned emits a task unpinned event if an emitter is configured.
func (db *DB) emitTaskUnpinned(task *Task) {
	db.recordEvent("task.updated", task.ID, "unpinned")
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskUnpinned(task)
	}
}

// emitTaskBlocked emits a task blocked event if an emitter is configured.
func (db *DB) emitTaskBlocked(task *Task, reason string) {
	db.recordEvent("task.blocked", task.ID, reason)
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskBlocked(task, reason)
	}
}

// emitTaskCompleted emits a task completed event if an emitter is configured.
func (db *DB) emitTaskCompleted(task *Task) {
	db.recordEvent("task.completed", task.ID, task.Title)
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskCompleted(task)
	}
}
