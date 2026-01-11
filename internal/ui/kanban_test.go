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

func TestKanbanBoard_HandleClick(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Set up some tasks in different columns
	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Backlog Task 2", Status: db.StatusBacklog},
		{ID: 3, Title: "In Progress Task", Status: db.StatusQueued},
		{ID: 4, Title: "Blocked Task", Status: db.StatusBlocked},
		{ID: 5, Title: "Done Task", Status: db.StatusDone},
	}
	board.SetTasks(tasks)

	// Calculate column width for test assertions
	// 4 columns, width 100, accounting for borders and gaps
	numCols := 4
	availableWidth := 100 - (numCols * 2) - (numCols - 1)
	colWidth := availableWidth / numCols
	colTotalWidth := colWidth + 2

	tests := []struct {
		name       string
		x          int
		y          int
		wantTaskID int64
		wantNil    bool
	}{
		{
			name:       "click on first task in backlog column",
			x:          colTotalWidth/2,           // Middle of first column
			y:          5,                          // After header (1 border + 3 header = 4, so y=5 is first task)
			wantTaskID: 1,
		},
		{
			name:       "click on second task in backlog column",
			x:          colTotalWidth/2,           // Middle of first column
			y:          8,                          // Second task (card height = 3)
			wantTaskID: 2,
		},
		{
			name:       "click on task in second column (in progress)",
			x:          colTotalWidth + colTotalWidth/2, // Middle of second column
			y:          5,
			wantTaskID: 3,
		},
		{
			name:       "click on task in third column (blocked)",
			x:          2*colTotalWidth + colTotalWidth/2, // Middle of third column
			y:          5,
			wantTaskID: 4,
		},
		{
			name:       "click on task in fourth column (done)",
			x:          3*colTotalWidth + colTotalWidth/2, // Middle of fourth column
			y:          5,
			wantTaskID: 5,
		},
		{
			name:    "click on header area returns nil",
			x:       colTotalWidth / 2,
			y:       2, // Header area
			wantNil: true,
		},
		{
			name:    "click below tasks returns nil",
			x:       colTotalWidth / 2,
			y:       45, // Way below tasks
			wantNil: true,
		},
		{
			name:    "terminal too small returns nil",
			x:       5,
			y:       5,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset board size for most tests
			if tt.name == "terminal too small returns nil" {
				board.SetSize(30, 5) // Too small
			} else {
				board.SetSize(100, 50)
			}

			task := board.HandleClick(tt.x, tt.y)

			if tt.wantNil {
				if task != nil {
					t.Errorf("HandleClick(%d, %d) = task %d, want nil", tt.x, tt.y, task.ID)
				}
			} else {
				if task == nil {
					t.Errorf("HandleClick(%d, %d) = nil, want task %d", tt.x, tt.y, tt.wantTaskID)
				} else if task.ID != tt.wantTaskID {
					t.Errorf("HandleClick(%d, %d) = task %d, want task %d", tt.x, tt.y, task.ID, tt.wantTaskID)
				}
			}
		})
	}
}

func TestKanbanBoard_MoveUpWrapsAround(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Set up tasks in the first column
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusBacklog},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Verify we start at row 0
	if board.selectedRow != 0 {
		t.Fatalf("Expected to start at row 0, got %d", board.selectedRow)
	}

	// MoveUp at top should wrap to bottom
	board.MoveUp()
	if board.selectedRow != 2 {
		t.Errorf("MoveUp at top: selectedRow = %d, want 2", board.selectedRow)
	}

	// MoveUp again should go to row 1
	board.MoveUp()
	if board.selectedRow != 1 {
		t.Errorf("MoveUp: selectedRow = %d, want 1", board.selectedRow)
	}
}

func TestKanbanBoard_MoveDownWrapsAround(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Set up tasks in the first column
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusBacklog},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Move to last task
	board.MoveDown() // row 1
	board.MoveDown() // row 2

	if board.selectedRow != 2 {
		t.Fatalf("Expected to be at row 2, got %d", board.selectedRow)
	}

	// MoveDown at bottom should wrap to top
	board.MoveDown()
	if board.selectedRow != 0 {
		t.Errorf("MoveDown at bottom: selectedRow = %d, want 0", board.selectedRow)
	}
}

func TestKanbanBoard_MoveUpDownEmptyColumn(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// No tasks - all columns empty
	board.SetTasks([]*db.Task{})

	// These should not panic
	board.MoveUp()
	board.MoveDown()

	// Selection should stay at 0
	if board.selectedRow != 0 {
		t.Errorf("selectedRow = %d, want 0", board.selectedRow)
	}
}

func TestKanbanBoard_MoveUpDownSingleTask(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Single task in column
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// MoveUp should wrap around to same position (row 0)
	board.MoveUp()
	if board.selectedRow != 0 {
		t.Errorf("MoveUp with single task: selectedRow = %d, want 0", board.selectedRow)
	}

	// MoveDown should also wrap around to same position
	board.MoveDown()
	if board.selectedRow != 0 {
		t.Errorf("MoveDown with single task: selectedRow = %d, want 0", board.selectedRow)
	}
}

func TestKanbanBoard_HandleClickUpdatesSelection(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusQueued},
	}
	board.SetTasks(tasks)

	// Click on task in second column
	numCols := 4
	availableWidth := 100 - (numCols * 2) - (numCols - 1)
	colWidth := availableWidth / numCols
	colTotalWidth := colWidth + 2

	x := colTotalWidth + colTotalWidth/2 // Middle of second column
	y := 5                                // First task position

	task := board.HandleClick(x, y)

	if task == nil {
		t.Fatal("HandleClick returned nil, expected task")
	}

	// Verify selection was updated
	if board.selectedCol != 1 {
		t.Errorf("selectedCol = %d, want 1", board.selectedCol)
	}
	if board.selectedRow != 0 {
		t.Errorf("selectedRow = %d, want 0", board.selectedRow)
	}

	// Verify SelectedTask returns the same task
	selectedTask := board.SelectedTask()
	if selectedTask == nil || selectedTask.ID != task.ID {
		t.Errorf("SelectedTask() = %v, want task %d", selectedTask, task.ID)
	}
}
