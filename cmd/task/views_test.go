package main

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func viewsTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// A saved view must select the same tasks on the CLI as it does on the board.
func TestTasksForViewMatchesStatusTokens(t *testing.T) {
	database := viewsTestDB(t)

	blocked := &db.Task{Title: "Waiting on review", Project: "personal", Status: db.StatusBlocked}
	running := &db.Task{Title: "Running", Project: "personal", Status: db.StatusProcessing}
	done := &db.Task{Title: "Shipped", Project: "personal", Status: db.StatusDone}
	for _, task := range []*db.Task{blocked, running, done} {
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}

	view, err := database.SaveView("Active", "status:in-progress status:blocked")
	if err != nil {
		t.Fatalf("save view: %v", err)
	}

	tasks, err := tasksForView(database, view)
	if err != nil {
		t.Fatalf("tasks for view: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	for _, task := range tasks {
		if task.ID == done.ID {
			t.Error("a done task leaked into an in-progress/blocked view")
		}
	}
}

// A view written with a project alias ("[ol]") must resolve to the real project,
// or it silently matches nothing.
func TestParseViewQueryResolvesProjectAliases(t *testing.T) {
	database := viewsTestDB(t)
	if err := database.CreateProject(&db.Project{Name: "offerlab", Path: t.TempDir(), Aliases: "ol"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	task := &db.Task{Title: "Bump deps", Project: "offerlab", Status: db.StatusBacklog}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	other := &db.Task{Title: "Unrelated", Project: "personal", Status: db.StatusBacklog}
	if err := database.CreateTask(other); err != nil {
		t.Fatalf("create task: %v", err)
	}

	query := parseViewQuery(database, "[ol]")
	if !query.MatchAll(task) {
		t.Error("[ol] should match the offerlab task")
	}
	if query.MatchAll(other) {
		t.Error("[ol] should not match a task in another project")
	}
}

// `ty views save` over an existing name replaces its query rather than erroring
// or creating a near-duplicate row.
func TestSaveViewReplacesByName(t *testing.T) {
	database := viewsTestDB(t)

	first, err := database.SaveView("Mine", "is:pinned")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	second, err := database.SaveView("mine", "status:blocked")
	if err != nil {
		t.Fatalf("resave: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("resave created a new row (%d -> %d)", first.ID, second.ID)
	}
	if second.Query != "status:blocked" {
		t.Errorf("query = %q, want the new one", second.Query)
	}
}
