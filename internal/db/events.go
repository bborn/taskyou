package db

// EventEmitter is an interface for emitting task events.
// This allows the DB to emit events without depending on the events package.
type EventEmitter interface {
	EmitTaskCreated(task *Task)
	EmitTaskUpdated(task *Task, changes map[string]interface{})
	EmitTaskDeleted(taskID int64, title string)
	EmitTaskPinned(task *Task)
	EmitTaskUnpinned(task *Task)
}

// SetEventEmitter sets the event emitter for this database.
// This is called by the executor to enable event emission.
func (db *DB) SetEventEmitter(emitter EventEmitter) {
	db.eventEmitter = emitter
}

// emitTaskCreated emits a task created event if an emitter is configured.
func (db *DB) emitTaskCreated(task *Task) {
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskCreated(task)
	}
}

// emitTaskUpdated emits a task updated event if an emitter is configured.
func (db *DB) emitTaskUpdated(task *Task, changes map[string]interface{}) {
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskUpdated(task, changes)
	}
}

// emitTaskDeleted emits a task deleted event if an emitter is configured.
func (db *DB) emitTaskDeleted(taskID int64, title string) {
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskDeleted(taskID, title)
	}
}

// emitTaskPinned emits a task pinned event if an emitter is configured.
func (db *DB) emitTaskPinned(task *Task) {
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskPinned(task)
	}
}

// emitTaskUnpinned emits a task unpinned event if an emitter is configured.
func (db *DB) emitTaskUnpinned(task *Task) {
	if db.eventEmitter != nil {
		db.eventEmitter.EmitTaskUnpinned(task)
	}
}
