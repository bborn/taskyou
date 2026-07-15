package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// kanbanHasTask reports whether the board's task set contains the given id.
func kanbanHasTask(k *KanbanBoard, id int64) bool {
	for _, t := range k.allTasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

// TestApplyFilterFindsUnloadedDoneTask is the core #4705 regression: the board
// only loads active tasks plus the most recent done tasks, so a keyword filter
// used to miss older done tasks even though "Go to Task" (which hits the DB)
// found them. applyFilter must now supplement m.tasks with a DB search.
func TestApplyFilterFindsUnloadedDoneTask(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	// A done task that lives only in the database — never loaded onto the board.
	doneTask := &db.Task{Title: "Build demo functionality", Project: "personal", Status: db.StatusDone}
	if err := database.CreateTask(doneTask); err != nil {
		t.Fatalf("create done task: %v", err)
	}
	// An active task that IS loaded onto the board.
	activeTask := &db.Task{Title: "Something unrelated", Project: "personal", Status: db.StatusProcessing}
	if err := database.CreateTask(activeTask); err != nil {
		t.Fatalf("create active task: %v", err)
	}

	m := &AppModel{db: database, kanban: NewKanbanBoard(0, 0)}
	// Simulate the board's loaded set: only the active task, not the done one.
	m.tasks = []*db.Task{activeTask}

	m.filterText = "demo functionality"
	m.applyFilter()

	if !kanbanHasTask(m.kanban, doneTask.ID) {
		t.Errorf("filter %q did not surface unloaded done task #%d", m.filterText, doneTask.ID)
	}
	if kanbanHasTask(m.kanban, activeTask.ID) {
		t.Errorf("filter %q should exclude non-matching active task #%d", m.filterText, activeTask.ID)
	}
}

// TestApplyFilterNoKeywordSkipsDBSearch verifies a project-only filter still
// works and doesn't error when there is no keyword to search the DB with.
func TestApplyFilterNoKeywordSkipsDBSearch(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	loaded := &db.Task{Title: "Loaded task", Project: "personal", Status: db.StatusProcessing}
	if err := database.CreateTask(loaded); err != nil {
		t.Fatalf("create task: %v", err)
	}

	m := &AppModel{db: database, kanban: NewKanbanBoard(0, 0)}
	m.tasks = []*db.Task{loaded}

	m.filterText = "[personal]"
	m.applyFilter()

	if !kanbanHasTask(m.kanban, loaded.ID) {
		t.Errorf("project-only filter dropped loaded task #%d", loaded.ID)
	}
}
