package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestKanbanBoard_FocusColumn(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Set up some tasks
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusQueued},
		{ID: 3, Title: "Task 3", Status: db.StatusBlocked},
		{ID: 4, Title: "Task 4", Status: db.StatusDone},
	}
	board.SetTasks(tasks)

	tests := []struct {
		name     string
		colIdx   int
		wantCol  int
	}{
		{"focus backlog", 0, 0},
		{"focus in progress", 1, 1},
		{"focus blocked", 2, 2},
		{"focus done", 3, 3},
		{"negative index ignored", -1, 3}, // stays at previous
		{"out of bounds ignored", 10, 3},  // stays at previous
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			board.FocusColumn(tt.colIdx)
			if board.selectedCol != tt.wantCol {
				t.Errorf("FocusColumn(%d) = %d, want %d", tt.colIdx, board.selectedCol, tt.wantCol)
			}
		})
	}
}

func TestKanbanBoard_ColumnCount(t *testing.T) {
	board := NewKanbanBoard(100, 50)
	if got := board.ColumnCount(); got != 4 {
		t.Errorf("ColumnCount() = %d, want 4", got)
	}
}
