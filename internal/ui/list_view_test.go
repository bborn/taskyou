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

	// Cycle status filter forward to "Active" (queued/processing/blocked).
	l.CycleStatusFilter(1)
	if statusFilterOptions[l.statusIdx].Label != "Active" {
		t.Fatalf("expected Active filter, got %q", statusFilterOptions[l.statusIdx].Label)
	}
	if got := rowIDs(l); len(got) != 2 {
		t.Fatalf("active filter rows = %v, want 2 rows (blocked + processing)", got)
	}
	for _, id := range rowIDs(l) {
		if id != 3 && id != 4 { // Charlie (blocked), Delta (processing)
			t.Fatalf("active filter unexpectedly included task %d", id)
		}
	}

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

func TestListViewActiveStatusFilter(t *testing.T) {
	l := NewListView(120, 30)
	// Add a queued task so Active covers all three of its statuses.
	tasks := makeListTasks()
	now := time.Now()
	tasks = append(tasks, &db.Task{ID: 5, Title: "Echo", Status: db.StatusQueued, Project: "proj-a", CreatedAt: lt(now.Add(-4 * time.Hour)), UpdatedAt: lt(now.Add(-4 * time.Hour))})
	l.SetTasks(tasks)

	// Select the Active filter directly.
	for statusFilterOptions[l.statusIdx].Label != "Active" {
		l.CycleStatusFilter(1)
	}

	// Active = queued (5) + processing (4) + blocked (3); excludes backlog (1) and done (2).
	got := rowIDs(l)
	if len(got) != 3 {
		t.Fatalf("active filter rows = %v, want 3 rows", got)
	}
	want := map[int64]bool{3: true, 4: true, 5: true}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("active filter unexpectedly included task %d (rows %v)", id, got)
		}
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
