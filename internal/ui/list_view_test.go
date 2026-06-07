package ui

import (
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func lt(t time.Time) db.LocalTime { return db.LocalTime{Time: t} }

func makeListTasks() []*db.Task {
	now := time.Now()
	return []*db.Task{
		{ID: 1, Title: "Alpha", Status: db.StatusBacklog, Project: "proj-a", CreatedAt: lt(now.Add(-3 * time.Hour)), UpdatedAt: lt(now.Add(-3 * time.Hour))},
		{ID: 2, Title: "Bravo", Status: db.StatusDone, Project: "proj-b", CreatedAt: lt(now.Add(-2 * time.Hour)), UpdatedAt: lt(now.Add(-30 * time.Minute))},
		{ID: 3, Title: "Charlie", Status: db.StatusBlocked, Project: "proj-a", CreatedAt: lt(now.Add(-1 * time.Hour)), UpdatedAt: lt(now.Add(-1 * time.Minute))},
		{ID: 4, Title: "Delta", Status: db.StatusProcessing, Project: "proj-b", CreatedAt: lt(now.Add(-10 * time.Hour)), UpdatedAt: lt(now.Add(-5 * time.Hour))},
	}
}

func rowIDs(l *ListView) []int64 {
	ids := make([]int64, len(l.rows))
	for i, t := range l.rows {
		ids[i] = t.ID
	}
	return ids
}

func TestListViewDefaultSortUpdatedDesc(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(makeListTasks())

	// Default sort is Updated descending: most recently updated first.
	got := rowIDs(l)
	want := []int64{3, 2, 1, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("default sort = %v, want %v", got, want)
		}
	}
}

func TestListViewSortByIDAndReverse(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(makeListTasks())

	// Cycle to the ID column. Order in sortColumns: Updated, Created, ID, ...
	l.NextSortColumn() // Created
	l.NextSortColumn() // ID
	if sortColumns[l.sortColIdx] != SortByID {
		t.Fatalf("expected sort column ID, got %v", sortColumns[l.sortColIdx])
	}
	// ID defaults to descending.
	if got := rowIDs(l); got[0] != 4 || got[3] != 1 {
		t.Fatalf("ID desc sort = %v, want [4 3 2 1]", got)
	}
	l.ToggleSortDirection()
	if got := rowIDs(l); got[0] != 1 || got[3] != 4 {
		t.Fatalf("ID asc sort = %v, want [1 2 3 4]", got)
	}
}

func TestListViewSortByTitle(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(makeListTasks())
	// Move to Title column.
	for sortColumns[l.sortColIdx] != SortByTitle {
		l.NextSortColumn()
	}
	got := rowIDs(l)
	want := []int64{1, 2, 3, 4} // Alpha, Bravo, Charlie, Delta
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("title sort = %v, want %v", got, want)
		}
	}
}

func TestListViewStatusFilter(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(makeListTasks())

	// Cycle status filter forward to "Backlog".
	l.CycleStatusFilter(1)
	if statusFilterOptions[l.statusIdx].Label != "Backlog" {
		t.Fatalf("expected Backlog filter, got %q", statusFilterOptions[l.statusIdx].Label)
	}
	if got := rowIDs(l); len(got) != 1 || got[0] != 1 {
		t.Fatalf("backlog filter rows = %v, want [1]", got)
	}

	// "In Progress" should match both queued and processing.
	l.CycleStatusFilter(1) // In Progress
	if statusFilterOptions[l.statusIdx].Label != "In Progress" {
		t.Fatalf("expected In Progress filter, got %q", statusFilterOptions[l.statusIdx].Label)
	}
	if got := rowIDs(l); len(got) != 1 || got[0] != 4 {
		t.Fatalf("in-progress filter rows = %v, want [4]", got)
	}
}

func TestListViewProjectFilter(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(makeListTasks())

	// projects(): All, proj-a, proj-b (sorted). Cycle to proj-a.
	l.CycleProjectFilter(1)
	if got := rowIDs(l); len(got) != 2 {
		t.Fatalf("proj-a filter rows = %v, want 2 rows", got)
	}
	for _, id := range rowIDs(l) {
		if id != 1 && id != 3 {
			t.Fatalf("proj-a filter unexpectedly included task %d", id)
		}
	}
}

func TestListViewSelectionPreservedAcrossSetTasks(t *testing.T) {
	l := NewListView(120, 30)
	tasks := makeListTasks()
	l.SetTasks(tasks)
	if !l.SelectTask(4) {
		t.Fatal("expected to select task 4")
	}
	// Re-set tasks (e.g. a refresh) and ensure selection follows the task.
	l.SetTasks(tasks)
	if sel := l.SelectedTask(); sel == nil || sel.ID != 4 {
		t.Fatalf("selection not preserved, got %v", sel)
	}
}

func TestListViewPinnedFloatToTop(t *testing.T) {
	l := NewListView(120, 30)
	tasks := makeListTasks()
	tasks[3].Pinned = true // Delta (ID 4) pinned
	l.SetTasks(tasks)
	// Even under Updated-desc sort (where 4 would be last), pinned floats up.
	if got := rowIDs(l); got[0] != 4 {
		t.Fatalf("pinned task not floated to top: %v", got)
	}
}

func TestListViewNavigationWraps(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(makeListTasks())
	l.selectedRow = 0
	l.MoveUp() // wrap to bottom
	if l.selectedRow != len(l.rows)-1 {
		t.Fatalf("MoveUp from top should wrap, got row %d", l.selectedRow)
	}
	l.MoveDown() // wrap to top
	if l.selectedRow != 0 {
		t.Fatalf("MoveDown from bottom should wrap, got row %d", l.selectedRow)
	}
}

func TestListViewJumpToPinned(t *testing.T) {
	l := NewListView(120, 30)
	tasks := makeListTasks()
	tasks[1].Pinned = true // Bravo (ID 2) pinned -> floats to top
	l.SetTasks(tasks)

	// Move selection away from the top, then jump back to the pinned prefix.
	l.selectedRow = 2
	l.JumpToPinned()
	if l.selectedRow != 0 {
		t.Fatalf("JumpToPinned: selectedRow = %d, want 0", l.selectedRow)
	}
	if sel := l.SelectedTask(); sel == nil || sel.ID != 2 {
		t.Fatalf("JumpToPinned: selected task = %v, want pinned task 2", sel)
	}
}

func TestListViewJumpToPinnedNoPinnedTasks(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(makeListTasks())
	l.selectedRow = 3
	// With no pinned tasks, jumping to pinned still lands on the top row.
	l.JumpToPinned()
	if l.selectedRow != 0 {
		t.Fatalf("JumpToPinned with no pinned tasks: selectedRow = %d, want 0", l.selectedRow)
	}
}

func TestListViewJumpToPinnedEmpty(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(nil)
	l.JumpToPinned() // must not panic
	if l.selectedRow != 0 {
		t.Fatalf("JumpToPinned on empty list: selectedRow = %d, want 0", l.selectedRow)
	}
}

func TestListViewJumpToUnpinned(t *testing.T) {
	l := NewListView(120, 30)
	tasks := makeListTasks()
	tasks[0].Pinned = true // Alpha (ID 1)
	tasks[1].Pinned = true // Bravo (ID 2)
	l.SetTasks(tasks)

	// Two pinned tasks float to the top, so the first unpinned row is index 2.
	l.selectedRow = 0
	l.JumpToUnpinned()
	if l.selectedRow != 2 {
		t.Fatalf("JumpToUnpinned: selectedRow = %d, want 2", l.selectedRow)
	}
	if sel := l.SelectedTask(); sel == nil || sel.Pinned {
		t.Fatalf("JumpToUnpinned: selected task = %v, want first unpinned task", sel)
	}
}

func TestListViewJumpToUnpinnedAllPinned(t *testing.T) {
	l := NewListView(120, 30)
	tasks := makeListTasks()
	for _, task := range tasks {
		task.Pinned = true
	}
	l.SetTasks(tasks)
	l.selectedRow = 1
	// No unpinned tasks: selection stays put.
	l.JumpToUnpinned()
	if l.selectedRow != 1 {
		t.Fatalf("JumpToUnpinned with all pinned: selectedRow = %d, want 1", l.selectedRow)
	}
}

func TestListViewJumpToUnpinnedNoPinnedTasks(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(makeListTasks())
	l.selectedRow = 3
	// First row is already unpinned, so jump lands on row 0.
	l.JumpToUnpinned()
	if l.selectedRow != 0 {
		t.Fatalf("JumpToUnpinned with no pinned tasks: selectedRow = %d, want 0", l.selectedRow)
	}
}

func TestListViewJumpToUnpinnedEmpty(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(nil)
	l.JumpToUnpinned() // must not panic
	if l.selectedRow != 0 {
		t.Fatalf("JumpToUnpinned on empty list: selectedRow = %d, want 0", l.selectedRow)
	}
}

func TestListViewRendersWithoutPanic(t *testing.T) {
	for _, w := range []int{40, 80, 120, 200} {
		l := NewListView(w, 24)
		l.SetTasks(makeListTasks())
		if out := l.View(); out == "" {
			t.Fatalf("View() returned empty at width %d", w)
		}
	}
}

func TestListViewEmptyState(t *testing.T) {
	l := NewListView(120, 30)
	l.SetTasks(nil)
	if !l.IsEmpty() {
		t.Fatal("expected empty list view")
	}
	if out := l.View(); out == "" {
		t.Fatal("empty View() should still render filter chips")
	}
}
