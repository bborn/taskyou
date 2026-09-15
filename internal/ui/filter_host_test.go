package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func hostTask(id int64, title, host string) *db.Task {
	return &db.Task{ID: id, Title: title, Status: db.StatusProcessing, PlacementTarget: host}
}

// The whole point: typing "@mona" on the board leaves only mona's tasks.
func TestApplyFilterByHost(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	mona := hostTask(0, "Fix the flaky suite", "mona")
	bruce := hostTask(0, "Fix the flaky suite too", "bruce")
	for _, task := range []*db.Task{mona, bruce} {
		task.Project = "personal"
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		if err := database.SetTaskPlacement(task.ID, task.PlacementTarget, "test"); err != nil {
			t.Fatalf("set placement: %v", err)
		}
	}

	m := &AppModel{db: database, kanban: NewKanbanBoard(0, 0)}
	m.tasks = []*db.Task{mona, bruce}

	m.filterText = "@mona"
	// A bare @host is purely structural now, so applyFilter resolves it inline
	// and returns no command; only a keyword query goes async.
	if cmd := m.applyFilter(); cmd != nil {
		m.finishBoardFilter(cmd().(boardFilterMsg))
	}
	if !kanbanHasTask(m.kanban, mona.ID) || kanbanHasTask(m.kanban, bruce.ID) {
		t.Errorf("@mona should show only mona's task (mona=%v bruce=%v)",
			kanbanHasTask(m.kanban, mona.ID), kanbanHasTask(m.kanban, bruce.ID))
	}

	// The host token must not pollute the keyword search: "flaky" still matches
	// both tasks, and the host is what narrows them to one.
	m.filterText = "@bruce flaky"
	if cmd := m.applyFilter(); cmd != nil {
		m.finishBoardFilter(cmd().(boardFilterMsg))
	}
	if !kanbanHasTask(m.kanban, bruce.ID) || kanbanHasTask(m.kanban, mona.ID) {
		t.Errorf("@bruce flaky should show only bruce's task (bruce=%v mona=%v)",
			kanbanHasTask(m.kanban, bruce.ID), kanbanHasTask(m.kanban, mona.ID))
	}
}

func TestOpenHostToken(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", -1},
		{"@", 0},
		{"@mo", 0},
		{"flaky @mo", 6},
		// Finished chip: a space closed it, so there is nothing to complete.
		{"@mona ", -1},
		{"@mona flaky", -1},
		// Not a chip at all.
		{"user@example.com", -1},
	}
	for _, c := range cases {
		if got := openHostToken(c.text); got != c.want {
			t.Errorf("openHostToken(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}
