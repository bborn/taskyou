package ui

import "time"

// undoEntry represents a snapshot of a text field's state.
type undoEntry struct {
	value     string
	cursorPos int // For textinput: character position; for textarea: we store a combined value
	timestamp time.Time
}

// undoStack provides undo/redo functionality for a text field.
// It merges rapid consecutive changes (within mergeWindow) into a single undo entry
// to avoid creating one entry per keystroke.
type undoStack struct {
	history     []undoEntry
	future      []undoEntry
	mergeWindow time.Duration
}

// newUndoStack creates a new undo stack with a default merge window.
func newUndoStack() *undoStack {
	return &undoStack{
		mergeWindow: 800 * time.Millisecond,
	}
}

// Save records the current state. Rapid changes are merged into the most recent entry.
func (u *undoStack) Save(value string, cursorPos int) {
	now := time.Now()

	entry := undoEntry{
		value:     value,
		cursorPos: cursorPos,
		timestamp: now,
	}

	// Merge with the last entry if the change was very recent (typing stream)
	if len(u.history) > 0 {
		last := u.history[len(u.history)-1]
		if now.Sub(last.timestamp) < u.mergeWindow {
			// Update the last entry in-place instead of pushing a new one
			u.history[len(u.history)-1] = entry
			// Still clear future on new input
			u.future = u.future[:0]
			return
		}
	}

	u.history = append(u.history, entry)
	// Any new change invalidates the redo stack
	u.future = u.future[:0]

	// Cap history at 100 entries to bound memory
	if len(u.history) > 100 {
		u.history = u.history[len(u.history)-100:]
	}
}

// Undo reverts to the previous state. Returns the restored state and true if successful.
func (u *undoStack) Undo(currentValue string, currentCursor int) (string, int, bool) {
	if len(u.history) == 0 {
		return "", 0, false
	}

	// Push current state to future (redo) stack
	u.future = append(u.future, undoEntry{
		value:     currentValue,
		cursorPos: currentCursor,
		timestamp: time.Now(),
	})

	// Pop from history
	entry := u.history[len(u.history)-1]
	u.history = u.history[:len(u.history)-1]

	return entry.value, entry.cursorPos, true
}

// Redo re-applies a previously undone change. Returns the restored state and true if successful.
func (u *undoStack) Redo(currentValue string, currentCursor int) (string, int, bool) {
	if len(u.future) == 0 {
		return "", 0, false
	}

	// Push current state to history
	u.history = append(u.history, undoEntry{
		value:     currentValue,
		cursorPos: currentCursor,
		timestamp: time.Now(),
	})

	// Pop from future
	entry := u.future[len(u.future)-1]
	u.future = u.future[:len(u.future)-1]

	return entry.value, entry.cursorPos, true
}

// CanUndo returns true if there are entries to undo.
func (u *undoStack) CanUndo() bool {
	return len(u.history) > 0
}

// CanRedo returns true if there are entries to redo.
func (u *undoStack) CanRedo() bool {
	return len(u.future) > 0
}
