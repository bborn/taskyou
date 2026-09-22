package main

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// `ty views show <name>` resolves a saved view the same way the board does,
// so a view targeting rows outside the 100 most-recent must list them: a
// status:backlog view against a board whose 100 most-recent rows are all
// done returns the 5 backlog rows, not the pre-fix "No tasks match this
// view".
func TestTasksForViewReturnsMatchingTasksWhenRecentDoneCrowdsOutBacklog(t *testing.T) {
	database := viewsTestDB(t)
	for i := 0; i < 5; i++ {
		if err := database.CreateTask(&db.Task{Title: "backlog", Project: "personal", Status: db.StatusBacklog}); err != nil {
			t.Fatalf("create backlog task: %v", err)
		}
	}
	for i := 0; i < 105; i++ {
		if err := database.CreateTask(&db.Task{Title: "done", Project: "personal", Status: db.StatusDone}); err != nil {
			t.Fatalf("create done task: %v", err)
		}
	}
	view, err := database.SaveView("BacklogView", "status:backlog")
	if err != nil {
		t.Fatalf("save view: %v", err)
	}

	tasks, err := tasksForView(database, view)
	if err != nil {
		t.Fatalf("tasks for view: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("len(tasks) = %d, want 5 (the 5 backlog rows outside the 100-row SQL window)", len(tasks))
	}
	for _, task := range tasks {
		if task.Status != db.StatusBacklog {
			t.Errorf("view matched a %q task under a status:backlog view — wrong match", task.Status)
		}
	}
}
