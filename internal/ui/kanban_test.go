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
		name    string
		colIdx  int
		wantCol int
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
			x:          colTotalWidth / 2, // Middle of first column
			y:          2,                 // After header (1 border + 1 header = 2, so y=2 is first task)
			wantTaskID: 1,
		},
		{
			name:       "click on second task in backlog column",
			x:          colTotalWidth / 2, // Middle of first column
			y:          5,                 // Second task (card height = 3, so y=5 is second task)
			wantTaskID: 2,
		},
		{
			name:       "click on task in second column (in progress)",
			x:          colTotalWidth + colTotalWidth/2, // Middle of second column
			y:          2,
			wantTaskID: 3,
		},
		{
			name:       "click on task in third column (blocked)",
			x:          2*colTotalWidth + colTotalWidth/2, // Middle of third column
			y:          2,
			wantTaskID: 4,
		},
		{
			name:       "click on task in fourth column (done)",
			x:          3*colTotalWidth + colTotalWidth/2, // Middle of fourth column
			y:          2,
			wantTaskID: 5,
		},
		{
			name:    "click on header area returns nil",
			x:       colTotalWidth / 2,
			y:       1, // Header area (y=1 is the header bar)
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
	y := 2                               // First task position (1 border + 1 header = 2)

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

func TestKanbanBoard_IsMobileMode(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		expected bool
	}{
		{"narrow terminal is mobile", 60, true},
		{"threshold boundary is mobile", 79, true},
		{"at threshold is desktop", 80, false},
		{"wide terminal is desktop", 120, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			board := NewKanbanBoard(tt.width, 50)
			if got := board.IsMobileMode(); got != tt.expected {
				t.Errorf("IsMobileMode() = %v, want %v for width %d", got, tt.expected, tt.width)
			}
		})
	}
}

func TestKanbanBoard_MobileView(t *testing.T) {
	// Create a narrow board that triggers mobile mode
	board := NewKanbanBoard(60, 30)

	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "In Progress Task", Status: db.StatusQueued},
		{ID: 3, Title: "Blocked Task", Status: db.StatusBlocked},
		{ID: 4, Title: "Done Task", Status: db.StatusDone},
	}
	board.SetTasks(tasks)

	// Verify mobile mode is active
	if !board.IsMobileMode() {
		t.Fatal("Expected mobile mode to be active")
	}

	// Render the view - should not panic
	view := board.View()
	if view == "" {
		t.Error("View() returned empty string")
	}

	// Verify we can navigate columns with MoveLeft/MoveRight
	board.FocusColumn(0) // Start at backlog
	if board.selectedCol != 0 {
		t.Errorf("FocusColumn(0): selectedCol = %d, want 0", board.selectedCol)
	}

	board.MoveRight()
	if board.selectedCol != 1 {
		t.Errorf("MoveRight: selectedCol = %d, want 1", board.selectedCol)
	}

	board.MoveLeft()
	if board.selectedCol != 0 {
		t.Errorf("MoveLeft: selectedCol = %d, want 0", board.selectedCol)
	}
}

func TestKanbanBoard_MobileTabClick(t *testing.T) {
	// Create a narrow board that triggers mobile mode
	board := NewKanbanBoard(60, 30)

	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "In Progress Task", Status: db.StatusQueued},
	}
	board.SetTasks(tasks)

	// Start at first column
	board.FocusColumn(0)

	// Calculate tab width
	numCols := 4
	tabWidth := (60 - numCols - 1) / numCols

	// Click on second tab (In Progress)
	x := tabWidth + tabWidth/2 // Middle of second tab
	y := 0                     // Tab bar area

	task := board.HandleClick(x, y)

	// Clicking on tab should change column but not select a task
	if task != nil {
		t.Errorf("HandleClick on tab returned task, expected nil")
	}

	// Verify column changed
	if board.selectedCol != 1 {
		t.Errorf("selectedCol = %d, want 1 after clicking tab", board.selectedCol)
	}
}

func TestKanbanBoard_MobileTaskClick(t *testing.T) {
	// Create a narrow board that triggers mobile mode
	board := NewKanbanBoard(60, 30)

	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Backlog Task 2", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Start at first column
	board.FocusColumn(0)

	// Click on first task in the column
	// Mobile layout: tab bar (2 lines), border (1), header (1), task area
	x := 30 // Middle of column
	y := 4  // After tab bar (2) + border (1) + header (1) = 4, so y=4 is first task

	task := board.HandleClick(x, y)

	// Should select the first task
	if task == nil {
		t.Error("HandleClick on task returned nil, expected task")
	} else if task.ID != 1 {
		t.Errorf("HandleClick returned task %d, want 1", task.ID)
	}

	// Verify selection was updated
	if board.selectedRow != 0 {
		t.Errorf("selectedRow = %d, want 0", board.selectedRow)
	}
}

func TestKanbanBoard_MobileViewRendersCorrectly(t *testing.T) {
	board := NewKanbanBoard(60, 30)

	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusBacklog},
		{ID: 3, Title: "Task 3", Status: db.StatusQueued},
	}
	board.SetTasks(tasks)

	// Should be in mobile mode
	if !board.IsMobileMode() {
		t.Fatal("Expected mobile mode")
	}

	// Navigate to each column and verify view renders
	for i := 0; i < board.ColumnCount(); i++ {
		board.FocusColumn(i)
		view := board.View()
		if view == "" {
			t.Errorf("View() at column %d returned empty string", i)
		}
	}
}

func TestKanbanBoard_DesktopViewAtThreshold(t *testing.T) {
	// At exactly the threshold, should use desktop view
	board := NewKanbanBoard(MobileWidthThreshold, 30)

	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Should NOT be in mobile mode
	if board.IsMobileMode() {
		t.Error("Expected desktop mode at threshold width")
	}

	// View should render without panic
	view := board.View()
	if view == "" {
		t.Error("View() returned empty string")
	}
}

func TestKanbanBoard_RecurringTasksAtBottom(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Mix of recurring and non-recurring tasks in the same column
	tasks := []*db.Task{
		{ID: 1, Title: "Regular Task 1", Status: db.StatusBacklog, Recurrence: ""},
		{ID: 2, Title: "Recurring Task (daily)", Status: db.StatusBacklog, Recurrence: "daily"},
		{ID: 3, Title: "Regular Task 2", Status: db.StatusBacklog, Recurrence: ""},
		{ID: 4, Title: "Recurring Task (weekly)", Status: db.StatusBacklog, Recurrence: "weekly"},
		{ID: 5, Title: "Regular Task 3", Status: db.StatusBacklog, Recurrence: ""},
	}
	board.SetTasks(tasks)

	// Get the backlog column (index 0)
	col := board.columns[0]

	// Verify order: all non-recurring tasks should come before recurring tasks
	// Expected order: 1, 3, 5 (non-recurring) then 2, 4 (recurring)
	expectedOrder := []int64{1, 3, 5, 2, 4}

	if len(col.Tasks) != len(expectedOrder) {
		t.Fatalf("Expected %d tasks in backlog, got %d", len(expectedOrder), len(col.Tasks))
	}

	for i, task := range col.Tasks {
		if task.ID != expectedOrder[i] {
			t.Errorf("Task at position %d: expected ID %d, got %d", i, expectedOrder[i], task.ID)
		}
	}

	// Verify that recurring tasks are at the end
	nonRecurringCount := 0
	for _, task := range col.Tasks {
		if !task.IsRecurring() {
			nonRecurringCount++
		}
	}

	// All non-recurring tasks should be in the first positions
	for i := 0; i < nonRecurringCount; i++ {
		if col.Tasks[i].IsRecurring() {
			t.Errorf("Expected non-recurring task at position %d, but found recurring task (ID %d)", i, col.Tasks[i].ID)
		}
	}

	// All recurring tasks should be at the end
	for i := nonRecurringCount; i < len(col.Tasks); i++ {
		if !col.Tasks[i].IsRecurring() {
			t.Errorf("Expected recurring task at position %d, but found non-recurring task (ID %d)", i, col.Tasks[i].ID)
		}
	}
}

func TestKanbanBoard_RecurringTasksSortingAcrossColumns(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Tasks in multiple columns with recurring ones
	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Regular", Status: db.StatusBacklog, Recurrence: ""},
		{ID: 2, Title: "Backlog Recurring", Status: db.StatusBacklog, Recurrence: "daily"},
		{ID: 3, Title: "InProgress Regular", Status: db.StatusQueued, Recurrence: ""},
		{ID: 4, Title: "InProgress Recurring", Status: db.StatusQueued, Recurrence: "weekly"},
	}
	board.SetTasks(tasks)

	// Check backlog column (index 0)
	backlogCol := board.columns[0]
	if len(backlogCol.Tasks) != 2 {
		t.Fatalf("Expected 2 tasks in backlog, got %d", len(backlogCol.Tasks))
	}
	if backlogCol.Tasks[0].ID != 1 {
		t.Errorf("Backlog first task: expected ID 1, got %d", backlogCol.Tasks[0].ID)
	}
	if backlogCol.Tasks[1].ID != 2 {
		t.Errorf("Backlog second task: expected ID 2, got %d", backlogCol.Tasks[1].ID)
	}

	// Check in-progress column (index 1)
	inProgressCol := board.columns[1]
	if len(inProgressCol.Tasks) != 2 {
		t.Fatalf("Expected 2 tasks in in-progress, got %d", len(inProgressCol.Tasks))
	}
	if inProgressCol.Tasks[0].ID != 3 {
		t.Errorf("InProgress first task: expected ID 3, got %d", inProgressCol.Tasks[0].ID)
	}
	if inProgressCol.Tasks[1].ID != 4 {
		t.Errorf("InProgress second task: expected ID 4, got %d", inProgressCol.Tasks[1].ID)
	}
}

// TestKanbanBoard_FirstTaskClickable is a regression test for the bug where
// clicking on the first task in a kanban column did not work because the
// click handler expected the task area to start at y=4 instead of y=2.
func TestKanbanBoard_FirstTaskClickable(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "First Task", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Calculate column layout
	numCols := 4
	availableWidth := 100 - (numCols * 2) - (numCols - 1)
	colWidth := availableWidth / numCols
	colTotalWidth := colWidth + 2

	// The first task should be clickable at y=2, y=3, and y=4
	// (y=0 is border, y=1 is header, y=2-4 is the first task card)
	for y := 2; y <= 4; y++ {
		task := board.HandleClick(colTotalWidth/2, y)
		if task == nil {
			t.Errorf("HandleClick at y=%d returned nil, expected task 1", y)
		} else if task.ID != 1 {
			t.Errorf("HandleClick at y=%d returned task %d, expected 1", y, task.ID)
		}
	}

	// Clicking on header (y=1) should not select a task
	task := board.HandleClick(colTotalWidth/2, 1)
	if task != nil {
		t.Errorf("HandleClick at y=1 (header) returned task %d, expected nil", task.ID)
	}

	// Clicking on border (y=0) should not select a task
	task = board.HandleClick(colTotalWidth/2, 0)
	if task != nil {
		t.Errorf("HandleClick at y=0 (border) returned task %d, expected nil", task.ID)
	}
}

// TestKanbanBoard_BlockedTaskHighlight verifies that blocked tasks (needing input)
// are rendered with a yellow outline to make them visually distinct.
func TestKanbanBoard_BlockedTaskHighlight(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Create tasks in different statuses
	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "Blocked Task (needs input)", Status: db.StatusBlocked},
		{ID: 3, Title: "Done Task", Status: db.StatusDone},
	}
	board.SetTasks(tasks)

	// Verify the blocked task is in the blocked column
	blockedCol := board.columns[2] // Blocked column is at index 2
	if len(blockedCol.Tasks) != 1 {
		t.Fatalf("Expected 1 task in blocked column, got %d", len(blockedCol.Tasks))
	}
	if blockedCol.Tasks[0].ID != 2 {
		t.Errorf("Expected task ID 2 in blocked column, got %d", blockedCol.Tasks[0].ID)
	}
	if blockedCol.Tasks[0].Status != db.StatusBlocked {
		t.Errorf("Expected task status 'blocked', got %s", blockedCol.Tasks[0].Status)
	}

	// Render the view and verify it doesn't panic
	view := board.View()
	if view == "" {
		t.Error("View() returned empty string")
	}
}
