package ui

import (
	"testing"
	"time"
)

func TestUndoStack_BasicUndoRedo(t *testing.T) {
	u := newUndoStack()
	// Override merge window to avoid test timing issues
	u.mergeWindow = 0

	// Type "hello" one character at a time
	u.Save("", 0)      // empty -> h
	u.Save("h", 1)     // h -> he
	u.Save("he", 2)    // he -> hel
	u.Save("hel", 3)   // hel -> hell
	u.Save("hell", 4)  // hell -> hello

	// Current state is "hello" at position 5

	// Undo should restore "hell"
	val, pos, ok := u.Undo("hello", 5)
	if !ok || val != "hell" || pos != 4 {
		t.Fatalf("expected (hell, 4, true), got (%s, %d, %t)", val, pos, ok)
	}

	// Redo should restore "hello"
	val, pos, ok = u.Redo("hell", 4)
	if !ok || val != "hello" || pos != 5 {
		t.Fatalf("expected (hello, 5, true), got (%s, %d, %t)", val, pos, ok)
	}
}

func TestUndoStack_MergeRapidChanges(t *testing.T) {
	u := newUndoStack()
	u.mergeWindow = 2 * time.Second // Very large window to ensure merging

	// Simulate rapid typing - all within merge window
	u.Save("", 0)
	u.Save("h", 1)
	u.Save("he", 2)
	u.Save("hel", 3)
	u.Save("hell", 4)

	// All rapid changes should be merged into one entry
	if len(u.history) != 1 {
		t.Fatalf("expected 1 merged entry, got %d", len(u.history))
	}

	// The merged entry should have the last saved state
	val, _, ok := u.Undo("hello", 5)
	if !ok || val != "hell" {
		t.Fatalf("expected (hell, true), got (%s, %t)", val, ok)
	}

	// No more history
	_, _, ok = u.Undo("hell", 4)
	if ok {
		t.Fatal("expected no more undo history")
	}
}

func TestUndoStack_RedoClearedOnNewChange(t *testing.T) {
	u := newUndoStack()
	u.mergeWindow = 0

	u.Save("", 0)
	u.Save("a", 1)

	// Undo
	u.Undo("ab", 2)
	if !u.CanRedo() {
		t.Fatal("expected redo to be available")
	}

	// New change should clear redo
	u.Save("x", 1)
	if u.CanRedo() {
		t.Fatal("expected redo to be cleared after new change")
	}
}

func TestUndoStack_EmptyStack(t *testing.T) {
	u := newUndoStack()

	_, _, ok := u.Undo("test", 4)
	if ok {
		t.Fatal("expected undo to fail on empty stack")
	}

	_, _, ok = u.Redo("test", 4)
	if ok {
		t.Fatal("expected redo to fail on empty stack")
	}
}

func TestUndoStack_CanUndoRedo(t *testing.T) {
	u := newUndoStack()
	u.mergeWindow = 0

	if u.CanUndo() {
		t.Fatal("expected CanUndo false on empty stack")
	}
	if u.CanRedo() {
		t.Fatal("expected CanRedo false on empty stack")
	}

	u.Save("", 0)
	if !u.CanUndo() {
		t.Fatal("expected CanUndo true after save")
	}

	u.Undo("a", 1)
	if !u.CanRedo() {
		t.Fatal("expected CanRedo true after undo")
	}
}
