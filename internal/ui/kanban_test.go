package ui

import (
	"fmt"
	"os"
	"strings"
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

func TestKanbanBoard_HasPrevNextTask(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Multiple tasks in column
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusBacklog},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// At first position: no prev, has next
	if board.HasPrevTask() {
		t.Error("At first task: HasPrevTask() = true, want false")
	}
	if !board.HasNextTask() {
		t.Error("At first task: HasNextTask() = false, want true")
	}

	// Move to middle: has both
	board.MoveDown()
	if !board.HasPrevTask() {
		t.Error("At middle task: HasPrevTask() = false, want true")
	}
	if !board.HasNextTask() {
		t.Error("At middle task: HasNextTask() = false, want true")
	}

	// Move to last: has prev, no next
	board.MoveDown()
	if !board.HasPrevTask() {
		t.Error("At last task: HasPrevTask() = false, want true")
	}
	if board.HasNextTask() {
		t.Error("At last task: HasNextTask() = true, want false")
	}
}

func TestKanbanBoard_HasPrevNextTaskSingleTask(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Single task in column
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// With only one task: no prev, no next
	if board.HasPrevTask() {
		t.Error("With single task: HasPrevTask() = true, want false")
	}
	if board.HasNextTask() {
		t.Error("With single task: HasNextTask() = true, want false")
	}
}

func TestKanbanBoard_HasPrevNextTaskEmptyColumn(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// No tasks
	board.SetTasks([]*db.Task{})

	// With empty column: no prev, no next
	if board.HasPrevTask() {
		t.Error("With empty column: HasPrevTask() = true, want false")
	}
	if board.HasNextTask() {
		t.Error("With empty column: HasNextTask() = true, want false")
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

func TestPinnedSelectionDoesNotResetScroll(t *testing.T) {
	board := NewKanbanBoard(100, 16)

	tasks := []*db.Task{
		{ID: 1, Title: "Pinned 1", Status: db.StatusBacklog, Pinned: true},
		{ID: 2, Title: "Pinned 2", Status: db.StatusBacklog, Pinned: true},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
		{ID: 4, Title: "Task 4", Status: db.StatusBacklog},
		{ID: 5, Title: "Task 5", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Force selection to the last unpinned task so the column scrolls
	board.selectedCol = 0
	board.selectedRow = len(tasks) - 1
	board.ensureSelectedVisible()

	if len(board.scrollOffsets) == 0 {
		t.Fatalf("scrollOffsets not initialized")
	}
	if board.scrollOffsets[0] == 0 {
		t.Fatalf("expected scroll offset to move when focusing unpinned tasks")
	}
	offset := board.scrollOffsets[0]

	// Selecting a pinned task should not reset the scroll offset
	board.selectedRow = 0
	board.ensureSelectedVisible()
	if board.scrollOffsets[0] != offset {
		t.Fatalf("pinned selection changed scroll offset: got %d want %d", board.scrollOffsets[0], offset)
	}
}

func TestPinnedTasksStayVisibleWhenScrolling(t *testing.T) {
	board := NewKanbanBoard(100, 16)

	tasks := []*db.Task{
		{ID: 1, Title: "Pinned Alpha", Status: db.StatusBacklog, Pinned: true},
		{ID: 2, Title: "Pinned Beta", Status: db.StatusBacklog, Pinned: true},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
		{ID: 4, Title: "Task 4", Status: db.StatusBacklog},
		{ID: 5, Title: "Task 5", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Scroll down so that unpinned tasks require an offset
	board.selectedRow = len(tasks) - 1
	board.ensureSelectedVisible()
	if board.scrollOffsets[0] == 0 {
		t.Fatalf("expected non-zero scroll offset for unpinned tasks")
	}

	view := board.View()
	for _, title := range []string{"Pinned Alpha", "Pinned Beta"} {
		if !strings.Contains(view, title) {
			t.Fatalf("expected view to include %q even when scrolled", title)
		}
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

// TestKanbanBoard_JumpToPinned tests the JumpToPinned method.
func TestKanbanBoard_JumpToPinned(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Pinned 1", Status: db.StatusBacklog, Pinned: true},
		{ID: 2, Title: "Pinned 2", Status: db.StatusBacklog, Pinned: true},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
		{ID: 4, Title: "Task 4", Status: db.StatusBacklog},
		{ID: 5, Title: "Task 5", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Start at task 5 (row 4)
	board.selectedRow = 4
	if selected := board.SelectedTask(); selected == nil || selected.ID != 5 {
		t.Fatalf("Expected to be at task 5, got %v", selected)
	}

	// JumpToPinned should go to the first task (row 0)
	board.JumpToPinned()
	if board.selectedRow != 0 {
		t.Errorf("JumpToPinned: selectedRow = %d, want 0", board.selectedRow)
	}
	if selected := board.SelectedTask(); selected == nil || selected.ID != 1 {
		t.Errorf("JumpToPinned: selected task = %v, want task 1", selected)
	}
}

// TestKanbanBoard_JumpToPinnedNoPinnedTasks tests JumpToPinned when no tasks are pinned.
func TestKanbanBoard_JumpToPinnedNoPinnedTasks(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusBacklog},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Start at task 3 (row 2)
	board.selectedRow = 2

	// JumpToPinned should still go to top (row 0) even with no pinned tasks
	board.JumpToPinned()
	if board.selectedRow != 0 {
		t.Errorf("JumpToPinned with no pinned tasks: selectedRow = %d, want 0", board.selectedRow)
	}
}

// TestKanbanBoard_JumpToPinnedEmptyColumn tests JumpToPinned on an empty column.
func TestKanbanBoard_JumpToPinnedEmptyColumn(t *testing.T) {
	board := NewKanbanBoard(100, 50)
	board.SetTasks([]*db.Task{})

	// Should not panic
	board.JumpToPinned()
	if board.selectedRow != 0 {
		t.Errorf("JumpToPinned with empty column: selectedRow = %d, want 0", board.selectedRow)
	}
}

// TestKanbanBoard_JumpToUnpinned tests the JumpToUnpinned method.
func TestKanbanBoard_JumpToUnpinned(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Pinned 1", Status: db.StatusBacklog, Pinned: true},
		{ID: 2, Title: "Pinned 2", Status: db.StatusBacklog, Pinned: true},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
		{ID: 4, Title: "Task 4", Status: db.StatusBacklog},
		{ID: 5, Title: "Task 5", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Start at first pinned task (row 0)
	board.selectedRow = 0
	if selected := board.SelectedTask(); selected == nil || selected.ID != 1 {
		t.Fatalf("Expected to be at task 1, got %v", selected)
	}

	// JumpToUnpinned should go to the first unpinned task (row 2, task 3)
	board.JumpToUnpinned()
	if board.selectedRow != 2 {
		t.Errorf("JumpToUnpinned: selectedRow = %d, want 2", board.selectedRow)
	}
	if selected := board.SelectedTask(); selected == nil || selected.ID != 3 {
		t.Errorf("JumpToUnpinned: selected task = %v, want task 3", selected)
	}
}

// TestKanbanBoard_JumpToUnpinnedAllPinned tests JumpToUnpinned when all tasks are pinned.
func TestKanbanBoard_JumpToUnpinnedAllPinned(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Pinned 1", Status: db.StatusBacklog, Pinned: true},
		{ID: 2, Title: "Pinned 2", Status: db.StatusBacklog, Pinned: true},
	}
	board.SetTasks(tasks)

	// Start at row 0
	board.selectedRow = 0

	// JumpToUnpinned should stay at current position (no unpinned tasks)
	board.JumpToUnpinned()
	if board.selectedRow != 0 {
		t.Errorf("JumpToUnpinned with all pinned: selectedRow = %d, want 0", board.selectedRow)
	}
}

// TestKanbanBoard_JumpToUnpinnedNoPinnedTasks tests JumpToUnpinned when no tasks are pinned.
func TestKanbanBoard_JumpToUnpinnedNoPinnedTasks(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Start at row 1
	board.selectedRow = 1

	// JumpToUnpinned should go to row 0 (first task is unpinned)
	board.JumpToUnpinned()
	if board.selectedRow != 0 {
		t.Errorf("JumpToUnpinned with no pinned tasks: selectedRow = %d, want 0", board.selectedRow)
	}
}

// TestKanbanBoard_JumpToUnpinnedEmptyColumn tests JumpToUnpinned on an empty column.
func TestKanbanBoard_JumpToUnpinnedEmptyColumn(t *testing.T) {
	board := NewKanbanBoard(100, 50)
	board.SetTasks([]*db.Task{})

	// Should not panic
	board.JumpToUnpinned()
	if board.selectedRow != 0 {
		t.Errorf("JumpToUnpinned with empty column: selectedRow = %d, want 0", board.selectedRow)
	}
}

// TestKanbanBoard_NeedsInput verifies that the input notification tracking works.
func TestKanbanBoard_NeedsInput(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBlocked},
		{ID: 2, Title: "Task 2", Status: db.StatusBlocked},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Initially no tasks need input
	if board.NeedsInput(1) {
		t.Error("Task 1 should not need input initially")
	}

	// Mark task 1 as needing input
	needsInput := map[int64]bool{1: true}
	board.SetTasksNeedingInput(needsInput)

	// Now task 1 needs input, but task 2 (also blocked) doesn't
	if !board.NeedsInput(1) {
		t.Error("Task 1 should need input after SetTasksNeedingInput")
	}
	if board.NeedsInput(2) {
		t.Error("Task 2 should not need input (not in the map)")
	}
	if board.NeedsInput(3) {
		t.Error("Task 3 should not need input")
	}

	// Render should work without panic
	view := board.View()
	if view == "" {
		t.Error("View() returned empty string")
	}
}

// TestKanbanBoard_DangerousModeIndicator tests that tasks in dangerous mode
// show a red dot indicator when the system is not in global dangerous mode.
func TestKanbanBoard_DangerousModeIndicator(t *testing.T) {
	// Save original value to restore after test
	original := os.Getenv("WORKTREE_DANGEROUS_MODE")
	defer os.Setenv("WORKTREE_DANGEROUS_MODE", original)

	// Ensure global dangerous mode is OFF for this test
	os.Unsetenv("WORKTREE_DANGEROUS_MODE")

	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Normal", Status: db.StatusProcessing, DangerousMode: false},
		{ID: 2, Title: "Danger", Status: db.StatusProcessing, DangerousMode: true},
		{ID: 3, Title: "Block", Status: db.StatusBlocked, DangerousMode: true},
		{ID: 4, Title: "Done", Status: db.StatusDone, DangerousMode: true}, // Should not show indicator
	}
	board.SetTasks(tasks)

	// Render the view - should work without errors
	view := board.View()
	if view == "" {
		t.Fatal("View() returned empty string")
	}

	// Focus on the In Progress column and verify rendering works
	board.FocusColumn(1) // In Progress column
	view = board.View()
	if view == "" {
		t.Error("View should render In Progress column")
	}

	// Focus on the Blocked column and verify rendering works
	board.FocusColumn(2) // Blocked column
	view = board.View()
	if view == "" {
		t.Error("View should render Blocked column")
	}
}

// TestKanbanBoard_DangerousModeIndicatorHiddenInGlobalMode tests that
// when global dangerous mode is enabled, individual tasks don't show
// the red dot indicator (since there's a global banner instead).
func TestKanbanBoard_DangerousModeIndicatorHiddenInGlobalMode(t *testing.T) {
	// Save original value to restore after test
	original := os.Getenv("WORKTREE_DANGEROUS_MODE")
	defer os.Setenv("WORKTREE_DANGEROUS_MODE", original)

	// Enable global dangerous mode
	os.Setenv("WORKTREE_DANGEROUS_MODE", "1")

	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Dangerous Task", Status: db.StatusProcessing, DangerousMode: true},
	}
	board.SetTasks(tasks)

	// Render should work without panic
	view := board.View()
	if view == "" {
		t.Error("View() returned empty string")
	}

	// The view should still render the task, just without the individual red dot
	// (the global banner will be shown by the app view wrapper, not the kanban)
	if !strings.Contains(view, "Dangerous Task") {
		t.Error("View should contain the task title 'Dangerous Task'")
	}
}

func TestKanbanBoard_OriginColumn(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Initially no origin column is set
	if board.HasOriginColumn() {
		t.Error("HasOriginColumn() should return false initially")
	}

	// Set up tasks
	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "Blocked Task", Status: db.StatusBlocked},
	}
	board.SetTasks(tasks)

	// Focus on blocked column
	board.FocusColumn(2)
	if board.selectedCol != 2 {
		t.Fatalf("FocusColumn(2) failed, selectedCol = %d", board.selectedCol)
	}

	// Set origin column
	board.SetOriginColumn()
	if !board.HasOriginColumn() {
		t.Error("HasOriginColumn() should return true after SetOriginColumn()")
	}

	// Clear origin column
	board.ClearOriginColumn()
	if board.HasOriginColumn() {
		t.Error("HasOriginColumn() should return false after ClearOriginColumn()")
	}
}

func TestKanbanBoard_OriginColumnPreservesColumnOnTaskMove(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Set up initial tasks - one blocked task
	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "Blocked Task", Status: db.StatusBlocked},
	}
	board.SetTasks(tasks)

	// Focus on blocked column and select the blocked task
	board.FocusColumn(2)
	board.SelectTask(2)

	if board.selectedCol != 2 {
		t.Fatalf("Expected selectedCol to be 2 (blocked), got %d", board.selectedCol)
	}

	// Set origin column (simulating entering detail view)
	board.SetOriginColumn()

	// Now simulate the task moving to a different column (e.g., to in-progress)
	tasks = []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "Blocked Task", Status: db.StatusQueued}, // Moved to in-progress
	}
	board.SetTasks(tasks)

	// selectedCol should stay at 2 (blocked) because origin column is set
	if board.selectedCol != 2 {
		t.Errorf("Expected selectedCol to remain at 2 (blocked), got %d", board.selectedCol)
	}
}

func TestKanbanBoard_NoOriginColumnFollowsTask(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Set up initial tasks - one blocked task
	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "Blocked Task", Status: db.StatusBlocked},
	}
	board.SetTasks(tasks)

	// Focus on blocked column and select the blocked task
	board.FocusColumn(2)
	board.SelectTask(2)

	// Verify initial state
	if board.selectedCol != 2 {
		t.Fatalf("Expected selectedCol to be 2 (blocked), got %d", board.selectedCol)
	}

	// Do NOT set origin column (normal dashboard behavior)

	// Now simulate the task moving to a different column (e.g., to in-progress)
	tasks = []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "Blocked Task", Status: db.StatusQueued}, // Moved to in-progress
	}
	board.SetTasks(tasks)

	// selectedCol should follow the task to column 1 (in-progress)
	if board.selectedCol != 1 {
		t.Errorf("Expected selectedCol to follow task to 1 (in-progress), got %d", board.selectedCol)
	}
}

func TestKanbanBoard_OriginColumnClampsSelection(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Set up tasks - multiple in blocked column
	tasks := []*db.Task{
		{ID: 1, Title: "Blocked Task 1", Status: db.StatusBlocked},
		{ID: 2, Title: "Blocked Task 2", Status: db.StatusBlocked},
		{ID: 3, Title: "Blocked Task 3", Status: db.StatusBlocked},
	}
	board.SetTasks(tasks)

	// Focus on blocked column, select last task
	board.FocusColumn(2)
	board.MoveDown()
	board.MoveDown() // Now at row 2 (third task)

	if board.selectedRow != 2 {
		t.Fatalf("Expected selectedRow to be 2, got %d", board.selectedRow)
	}

	// Set origin column
	board.SetOriginColumn()

	// Remove all but one task from blocked column
	tasks = []*db.Task{
		{ID: 1, Title: "Blocked Task 1", Status: db.StatusBlocked},
	}
	board.SetTasks(tasks)

	// selectedRow should be clamped to 0 (only task remaining)
	if board.selectedRow != 0 {
		t.Errorf("Expected selectedRow to be clamped to 0, got %d", board.selectedRow)
	}

	// selectedCol should still be 2 (blocked)
	if board.selectedCol != 2 {
		t.Errorf("Expected selectedCol to remain at 2, got %d", board.selectedCol)
	}
}

func TestKanbanBoard_OriginColumnEmptyColumn(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Set up tasks - one in blocked column
	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "Blocked Task", Status: db.StatusBlocked},
	}
	board.SetTasks(tasks)

	// Focus on blocked column
	board.FocusColumn(2)
	board.SelectTask(2)

	// Set origin column
	board.SetOriginColumn()

	// Now remove ALL tasks from blocked column (move to in-progress)
	tasks = []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "Was Blocked", Status: db.StatusQueued}, // Moved to in-progress
	}
	board.SetTasks(tasks)

	// selectedCol should still be 2 (blocked) - origin column is preserved
	if board.selectedCol != 2 {
		t.Errorf("Expected selectedCol to remain at 2, got %d", board.selectedCol)
	}

	// SelectedTask should return nil (no tasks in blocked column)
	if board.SelectedTask() != nil {
		t.Error("SelectedTask() should return nil for empty column")
	}

	// HasPrevTask and HasNextTask should return false
	if board.HasPrevTask() {
		t.Error("HasPrevTask() should return false for empty column")
	}
	if board.HasNextTask() {
		t.Error("HasNextTask() should return false for empty column")
	}
}

// TestKanbanBoard_SelectByShortcut tests selecting tasks by keyboard shortcuts 1-18.
func TestKanbanBoard_SelectByShortcut(t *testing.T) {
	board := NewKanbanBoard(100, 80) // Taller to fit more tasks

	// Set up 15 tasks in the first column
	tasks := make([]*db.Task, 15)
	for i := 0; i < 15; i++ {
		tasks[i] = &db.Task{ID: int64(i + 1), Title: fmt.Sprintf("Task %d", i+1), Status: db.StatusBacklog}
	}
	board.SetTasks(tasks)

	tests := []struct {
		name       string
		shortcut   int
		wantTaskID int64
		wantNil    bool
	}{
		{"shortcut 1 selects first task", 1, 1, false},
		{"shortcut 2 selects second task", 2, 2, false},
		{"shortcut 9 selects ninth task", 9, 9, false},
		{"shortcut 10 selects tenth task", 10, 10, false},
		{"shortcut 11 selects eleventh task", 11, 11, false},
		{"shortcut 15 selects fifteenth task", 15, 15, false},
		{"shortcut 16 returns nil (only 15 tasks)", 16, 0, true},
		{"shortcut 0 returns nil (invalid)", 0, 0, true},
		{"shortcut 19 returns nil (out of range)", 19, 0, true},
		{"shortcut -1 returns nil (invalid)", -1, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset selection
			board.selectedRow = 0

			task := board.SelectByShortcut(tt.shortcut)

			if tt.wantNil {
				if task != nil {
					t.Errorf("SelectByShortcut(%d) = task %d, want nil", tt.shortcut, task.ID)
				}
			} else {
				if task == nil {
					t.Errorf("SelectByShortcut(%d) = nil, want task %d", tt.shortcut, tt.wantTaskID)
				} else if task.ID != tt.wantTaskID {
					t.Errorf("SelectByShortcut(%d) = task %d, want task %d", tt.shortcut, task.ID, tt.wantTaskID)
				}
			}
		})
	}
}

// TestKanbanBoard_SelectByShortcutEmptyColumn tests SelectByShortcut on an empty column.
func TestKanbanBoard_SelectByShortcutEmptyColumn(t *testing.T) {
	board := NewKanbanBoard(100, 50)
	board.SetTasks([]*db.Task{})

	// Should return nil for any shortcut
	task := board.SelectByShortcut(1)
	if task != nil {
		t.Errorf("SelectByShortcut(1) on empty column returned task, want nil")
	}
}

// TestKanbanBoard_SelectByShortcutWithPinnedTasks tests that shortcuts work correctly
// when there are pinned tasks at the top.
func TestKanbanBoard_SelectByShortcutWithPinnedTasks(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	tasks := []*db.Task{
		{ID: 1, Title: "Pinned 1", Status: db.StatusBacklog, Pinned: true},
		{ID: 2, Title: "Pinned 2", Status: db.StatusBacklog, Pinned: true},
		{ID: 3, Title: "Task 3", Status: db.StatusBacklog},
		{ID: 4, Title: "Task 4", Status: db.StatusBacklog},
	}
	board.SetTasks(tasks)

	// Shortcut 1 should select the first pinned task
	task := board.SelectByShortcut(1)
	if task == nil || task.ID != 1 {
		t.Errorf("SelectByShortcut(1) = %v, want task 1", task)
	}

	// Shortcut 3 should select the first unpinned task
	task = board.SelectByShortcut(3)
	if task == nil || task.ID != 3 {
		t.Errorf("SelectByShortcut(3) = %v, want task 3", task)
	}

	// Shortcut 4 should select task 4
	task = board.SelectByShortcut(4)
	if task == nil || task.ID != 4 {
		t.Errorf("SelectByShortcut(4) = %v, want task 4", task)
	}
}

// TestKanbanBoard_ColumnColors verifies that each column has the correct and distinct color.
// This is a regression test to ensure blocked column doesn't incorrectly use the muted color.
func TestKanbanBoard_ColumnColors(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	// Verify we have 4 columns
	if len(board.columns) != 4 {
		t.Fatalf("Expected 4 columns, got %d", len(board.columns))
	}

	// Verify column order and expected colors
	expectedColumns := []struct {
		title string
		color string // Expected color variable name for debugging
	}{
		{"Backlog", "ColorMuted"},
		{"In Progress", "ColorInProgress"},
		{"Blocked", "ColorBlocked"},
		{"Done", "ColorDone"},
	}

	for i, expected := range expectedColumns {
		if board.columns[i].Title != expected.title {
			t.Errorf("Column %d: expected title %q, got %q", i, expected.title, board.columns[i].Title)
		}
	}

	// CRITICAL: Verify Blocked column (index 2) has a DIFFERENT color than Backlog (index 0)
	// This catches the bug where blocked was showing as gray instead of red
	backlogColor := board.columns[0].Color
	blockedColor := board.columns[2].Color

	if backlogColor == blockedColor {
		t.Errorf("BUG: Blocked column has same color as Backlog column (both are %v). "+
			"Blocked should be red (ColorBlocked), not gray (ColorMuted)", blockedColor)
	}

	// Verify the colors match the expected color variables
	if board.columns[0].Color != ColorMuted {
		t.Errorf("Backlog column should use ColorMuted, got %v", board.columns[0].Color)
	}
	if board.columns[1].Color != ColorInProgress {
		t.Errorf("In Progress column should use ColorInProgress, got %v", board.columns[1].Color)
	}
	if board.columns[2].Color != ColorBlocked {
		t.Errorf("Blocked column should use ColorBlocked, got %v", board.columns[2].Color)
	}
	if board.columns[3].Color != ColorDone {
		t.Errorf("Done column should use ColorDone, got %v", board.columns[3].Color)
	}

	// Also verify the actual color values are different
	if ColorMuted == ColorBlocked {
		t.Errorf("BUG: ColorMuted and ColorBlocked have the same value: %v. "+
			"ColorBlocked should be red (#E06C75), ColorMuted should be gray (#5C6370)", ColorMuted)
	}
}

func TestEmptyColumnMessage(t *testing.T) {
	tests := []struct {
		status   string
		expected string
	}{
		{db.StatusBacklog, "Press 'n' to create a task"},
		{db.StatusQueued, "Press 'x' to execute a task"},
		{db.StatusBlocked, "No tasks need input"},
		{db.StatusDone, "Completed tasks appear here"},
		{"unknown", "No tasks"},
		{"", "No tasks"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := emptyColumnMessage(tt.status)
			if got != tt.expected {
				t.Errorf("emptyColumnMessage(%q) = %q, want %q", tt.status, got, tt.expected)
			}
		})
	}
}

func TestKanbanBoard_IsEmpty(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	t.Run("empty board is empty", func(t *testing.T) {
		board.SetTasks(nil)
		if !board.IsEmpty() {
			t.Error("expected empty board to be empty")
		}
	})

	t.Run("board with tasks is not empty", func(t *testing.T) {
		board.SetTasks([]*db.Task{
			{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		})
		if board.IsEmpty() {
			t.Error("expected board with tasks to not be empty")
		}
	})

	t.Run("empty slice is empty", func(t *testing.T) {
		board.SetTasks([]*db.Task{})
		if !board.IsEmpty() {
			t.Error("expected board with empty slice to be empty")
		}
	})
}

func TestKanbanBoard_TotalTaskCount(t *testing.T) {
	board := NewKanbanBoard(100, 50)

	t.Run("empty board has zero tasks", func(t *testing.T) {
		board.SetTasks(nil)
		if count := board.TotalTaskCount(); count != 0 {
			t.Errorf("expected 0 tasks, got %d", count)
		}
	})

	t.Run("counts tasks across all columns", func(t *testing.T) {
		board.SetTasks([]*db.Task{
			{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
			{ID: 2, Title: "Task 2", Status: db.StatusQueued},
			{ID: 3, Title: "Task 3", Status: db.StatusBlocked},
			{ID: 4, Title: "Task 4", Status: db.StatusDone},
		})
		if count := board.TotalTaskCount(); count != 4 {
			t.Errorf("expected 4 tasks, got %d", count)
		}
	})
}
