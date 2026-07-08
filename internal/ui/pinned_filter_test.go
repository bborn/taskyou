package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestFilteredPinnedTasksRespectsFilter is the core ticket requirement: the
// detail view's pinned quick-nav must honour the board's active filter, so a
// board filtered to "[projectX]" surfaces only projectX's pinned tasks, not
// every pin across the board.
func TestFilteredPinnedTasksRespectsFilter(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	m := &AppModel{db: database}
	m.tasks = []*db.Task{
		{ID: 1, Title: "alpha", Project: "projectx", Status: db.StatusProcessing, Pinned: true},
		{ID: 2, Title: "beta", Project: "projectx", Status: db.StatusBacklog, Pinned: false}, // not pinned
		{ID: 3, Title: "gamma", Project: "other", Status: db.StatusBlocked, Pinned: true},    // pinned, wrong project
		{ID: 4, Title: "delta", Project: "projectx", Status: db.StatusBlocked, Pinned: true},
	}

	// No filter: every pinned task, ordered by status column then ID.
	// Statuses: #1 processing(2); #3 & #4 both blocked(3), so ID-ascending -> [1,3,4]
	got := ids(m.filteredPinnedTasks())
	want := []int64{1, 3, 4}
	if !equalIDs(got, want) {
		t.Errorf("no filter: got %v, want %v", got, want)
	}

	// Filter to projectx via bracket syntax: only pinned projectx tasks.
	m.filterText = "[projectx]"
	got = ids(m.filteredPinnedTasks())
	want = []int64{1, 4}
	if !equalIDs(got, want) {
		t.Errorf("[projectx] filter: got %v, want %v (should exclude pinned task from 'other')", got, want)
	}

	// Filter to a project with no pinned tasks: empty result.
	m.filterText = "[other] gamma"
	got = ids(m.filteredPinnedTasks())
	if len(got) != 1 || got[0] != 3 {
		t.Errorf("[other] gamma filter: got %v, want [3]", got)
	}
}

func ids(tasks []*db.Task) []int64 {
	out := make([]int64, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
