package db

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
	// Best-effort: a failed event write must never fail the mutation itself.
	_, _ = db.Exec(`
		INSERT INTO event_log (event_type, task_id, message, metadata)
		VALUES (?, ?, ?, '')
	`, eventType, taskID, message)
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
	db.recordEvent("task.updated", task.ID, task.Title)
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
