package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func listBoard(t *testing.T, tasks []*db.Task) *KanbanBoard {
	t.Helper()
	board := NewKanbanBoard(120, 30)
	board.SetListMode(true)
	board.SetTasks(tasks)
	return board
}

func listSampleTasks() []*db.Task {
	return []*db.Task{
		{ID: 1, Title: "Backlog item", Status: db.StatusBacklog, Project: "personal"},
		{ID: 2, Title: "Running now", Status: db.StatusProcessing, Project: "offerlab"},
		{ID: 3, Title: "Waiting on me", Status: db.StatusBlocked, Project: "offerlab"},
		{ID: 4, Title: "Shipped", Status: db.StatusDone, Project: "personal"},
		{ID: 5, Title: "Queued up", Status: db.StatusQueued, Project: "personal"},
	}
}

func listIDs(board *KanbanBoard) []int64 {
	var ids []int64
	for _, t := range board.ListTasks() {
		ids = append(ids, t.ID)
	}
	return ids
}

func TestListModeOrdersByUrgency(t *testing.T) {
	board := listBoard(t, listSampleTasks())

	got := listIDs(board)
	// Sections run blocked, In progress, backlog, done. Processing and queued
	// share the In progress section — the same pairing the kanban column makes —
	// so #2 (processing) and #5 (queued) sit together, urgency ordering them.
	want := []int64{3, 2, 5, 1, 4}
	if len(got) != len(want) {
		t.Fatalf("list has %d tasks, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("list order = %v, want %v", got, want)
		}
	}
}

func TestListModeFloatsPinnedToTop(t *testing.T) {
	tasks := listSampleTasks()
	tasks[3].Pinned = true // the done task
	board := listBoard(t, tasks)

	if got := listIDs(board)[0]; got != 4 {
		t.Errorf("pinned task should lead the list, got #%d first", got)
	}
}

func TestListModeNavigationWraps(t *testing.T) {
	board := listBoard(t, listSampleTasks())

	if got := board.SelectedTask(); got == nil || got.ID != 3 {
		t.Fatalf("list should start on the first row, got %v", got)
	}

	board.MoveDown()
	if got := board.SelectedTask(); got.ID != 2 {
		t.Errorf("down should advance one row, got #%d", got.ID)
	}

	board.MoveUp()
	board.MoveUp() // wrap past the top
	if got := board.SelectedTask(); got.ID != 4 {
		t.Errorf("up from the first row should wrap to the last, got #%d", got.ID)
	}

	board.MoveDown() // wrap past the bottom
	if got := board.SelectedTask(); got.ID != 3 {
		t.Errorf("down from the last row should wrap to the first, got #%d", got.ID)
	}
}

// Left/right are column moves; in a flat list they must do nothing rather than
// silently changing which task the next action applies to.
func TestListModeIgnoresColumnMoves(t *testing.T) {
	board := listBoard(t, listSampleTasks())
	board.MoveDown()
	before := board.SelectedTask().ID

	board.MoveLeft()
	board.MoveRight()

	if got := board.SelectedTask().ID; got != before {
		t.Errorf("column moves changed the selection: #%d -> #%d", before, got)
	}
}

// Toggling modes must land on the same task, or every action after the toggle
// applies to something the user didn't choose.
func TestToggleModesKeepsSelection(t *testing.T) {
	board := NewKanbanBoard(120, 30)
	board.SetTasks(listSampleTasks())

	board.FocusColumn(2) // Blocked
	selected := board.SelectedTask()
	if selected == nil || selected.ID != 3 {
		t.Fatalf("setup: expected the blocked task, got %v", selected)
	}

	board.SetListMode(true)
	if got := board.SelectedTask(); got == nil || got.ID != 3 {
		t.Fatalf("switching to list lost the selection: %v", got)
	}

	board.MoveDown() // move to a different task in list order
	moved := board.SelectedTask().ID

	board.SetListMode(false)
	if got := board.SelectedTask(); got == nil || got.ID != moved {
		t.Fatalf("switching back to the board lost the selection: %v, want #%d", got, moved)
	}
}

// A refresh must not move the cursor onto a different task.
func TestListModeKeepsSelectionAcrossRefresh(t *testing.T) {
	board := listBoard(t, listSampleTasks())
	board.MoveDown()
	before := board.SelectedTask().ID

	board.SetTasks(listSampleTasks())

	if got := board.SelectedTask(); got == nil || got.ID != before {
		t.Errorf("refresh moved the selection off #%d: %v", before, got)
	}
}

func TestListModeStatusJump(t *testing.T) {
	board := listBoard(t, listSampleTasks())

	board.FocusColumn(3) // Done
	if got := board.SelectedTask(); got == nil || got.ID != 4 {
		t.Errorf("D should jump to the first done task, got %v", got)
	}

	board.FocusColumn(1) // In Progress covers processing + queued
	if got := board.SelectedTask(); got == nil || got.ID != 2 {
		t.Errorf("P should jump to the running task, got %v", got)
	}
}

func TestListModeRendersOneLinePerTask(t *testing.T) {
	board := listBoard(t, listSampleTasks())
	board.SetListTitle("Active")

	out := board.View()
	if !strings.Contains(out, "Active") {
		t.Error("header should name the active view")
	}
	if !strings.Contains(out, "5 tasks") {
		t.Errorf("header should count the tasks:\n%s", out)
	}
	for _, want := range []string{"#1", "#2", "#3", "#4", "#5", "Running now", "Waiting on me"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output is missing %q:\n%s", want, out)
		}
	}
	// Project labels are abbreviated the same way the kanban card does it.
	if !strings.Contains(out, "[ol]") {
		t.Errorf("expected the abbreviated project tag:\n%s", out)
	}
}

func TestListModeScrollsToKeepSelectionVisible(t *testing.T) {
	var tasks []*db.Task
	for i := int64(1); i <= 40; i++ {
		tasks = append(tasks, &db.Task{ID: i, Title: "Task", Status: db.StatusBacklog})
	}
	board := NewKanbanBoard(100, 14)
	board.SetListMode(true)
	board.SetTasks(tasks)

	capacity := board.listCapacity()
	for i := 0; i < capacity+3; i++ {
		board.MoveDown()
	}
	if board.listRow < board.listScroll || board.listRow >= board.listScroll+capacity {
		t.Errorf("selected row %d outside viewport [%d,%d)", board.listRow, board.listScroll, board.listScroll+capacity)
	}

	// Whatever the scroll position, the selected task must actually be drawn —
	// that is the invariant the scroll maths exists to keep.
	if sel := board.SelectedTask(); sel == nil || !strings.Contains(board.View(), fmt.Sprintf("#%d", sel.ID)) {
		t.Errorf("selected task is not on screen:\n%s", board.View())
	}
}

func TestListModeClickSelectsRow(t *testing.T) {
	board := listBoard(t, listSampleTasks())

	// y=0 border, y=1 header bar, y=2 arrangement widget, y=3 the first section
	// header, y=4 the first task.
	first := board.ListTasks()[0]
	if got := board.HandleClick(10, 4); got == nil || got.ID != first.ID {
		t.Errorf("click on the first row should select #%d, got %v", first.ID, got)
	}
	if got := board.HandleClick(10, 3); got != nil {
		t.Errorf("click on a section header should select nothing, got %v", got)
	}
	if got := board.HandleClick(10, 99); got != nil {
		t.Errorf("click past the last row should select nothing, got %v", got)
	}
}

func TestListModeEmptyStateExplainsTheFilter(t *testing.T) {
	board := NewKanbanBoard(100, 20)
	board.SetListMode(true)
	board.SetTasks(nil)

	if !board.IsEmpty() {
		t.Error("a list with no tasks is empty")
	}
	if !strings.Contains(board.View(), "press 'n'") {
		t.Error("an unfiltered empty list should suggest creating a task")
	}

	board.SetListTitle("Active")
	if out := board.View(); !strings.Contains(out, "Active") || !strings.Contains(out, "'/'") {
		t.Errorf("a filtered empty list should name the filter and offer to change it:\n%s", out)
	}
}

// The render cache keys off a signature; if list state were missing from it the
// list would freeze on its first frame.
func TestListModeInvalidatesRenderCache(t *testing.T) {
	board := listBoard(t, listSampleTasks())
	before := board.View()

	board.MoveDown()
	if after := board.View(); after == before {
		t.Error("moving the list cursor must change the rendered output")
	}

	moved := board.View()
	board.SetListTitle("Something else")
	if after := board.View(); after == moved {
		t.Error("changing the list title must change the rendered output")
	}
}

func TestTaskPositionReportsListIndex(t *testing.T) {
	board := listBoard(t, listSampleTasks())
	board.MoveDown()

	pos, total := board.GetTaskPosition()
	if pos != 2 || total != 5 {
		t.Errorf("GetTaskPosition() = (%d,%d), want (2,5)", pos, total)
	}
	if !board.HasPrevTask() || !board.HasNextTask() {
		t.Error("a middle row has both neighbours")
	}
	if board.TotalTaskCount() != 5 {
		t.Errorf("TotalTaskCount() = %d, want 5", board.TotalTaskCount())
	}
}

func TestListModeJumpToPinned(t *testing.T) {
	tasks := listSampleTasks()
	tasks[0].Pinned = true // backlog item
	board := listBoard(t, tasks)

	board.MoveDown()
	board.MoveDown()
	board.JumpToPinned()
	if got := board.SelectedTask(); got == nil || !got.Pinned {
		t.Errorf("JumpToPinned should land on a pinned task, got %v", got)
	}

	board.JumpToUnpinned()
	if got := board.SelectedTask(); got == nil || got.Pinned {
		t.Errorf("JumpToUnpinned should land on an unpinned task, got %v", got)
	}
}
