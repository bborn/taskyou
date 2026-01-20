package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestNewTiledModel_FilterActiveTasks(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "Backlog Task", Status: db.StatusBacklog},
		{ID: 2, Title: "Queued Task", Status: db.StatusQueued},
		{ID: 3, Title: "Processing Task", Status: db.StatusProcessing},
		{ID: 4, Title: "Blocked Task", Status: db.StatusBlocked},
		{ID: 5, Title: "Done Task", Status: db.StatusDone},
	}

	model := NewTiledModel(tasks, nil, 100, 50)

	// Should only include queued and processing tasks
	if model.TaskCount() != 2 {
		t.Errorf("expected 2 active tasks, got %d", model.TaskCount())
	}

	// Check the tasks are correct
	activeTasks := model.Tasks()
	if len(activeTasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(activeTasks))
	}

	// Should be sorted by ID
	if activeTasks[0].ID != 2 {
		t.Errorf("expected first task ID 2, got %d", activeTasks[0].ID)
	}
	if activeTasks[1].ID != 3 {
		t.Errorf("expected second task ID 3, got %d", activeTasks[1].ID)
	}
}

func TestTiledModel_GridLayout(t *testing.T) {
	tests := []struct {
		name     string
		numTasks int
		wantCols int
		wantRows int
	}{
		{"no tasks", 0, 0, 0},
		{"one task", 1, 1, 1},
		{"two tasks", 2, 2, 1},
		{"three tasks", 3, 3, 1},
		{"four tasks", 4, 2, 2},
		{"five tasks", 5, 3, 2},
		{"six tasks", 6, 3, 2},
		{"nine tasks", 9, 3, 3},
		{"twelve tasks", 12, 4, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tasks := make([]*db.Task, tt.numTasks)
			for i := 0; i < tt.numTasks; i++ {
				tasks[i] = &db.Task{
					ID:     int64(i + 1),
					Title:  "Task",
					Status: db.StatusProcessing,
				}
			}

			model := NewTiledModel(tasks, nil, 100, 50)
			cols, rows := model.GridDimensions()

			if cols != tt.wantCols {
				t.Errorf("expected %d cols, got %d", tt.wantCols, cols)
			}
			if rows != tt.wantRows {
				t.Errorf("expected %d rows, got %d", tt.wantRows, rows)
			}
		})
	}
}

func TestTiledModel_Selection(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusProcessing},
		{ID: 2, Title: "Task 2", Status: db.StatusProcessing},
		{ID: 3, Title: "Task 3", Status: db.StatusProcessing},
		{ID: 4, Title: "Task 4", Status: db.StatusProcessing},
	}

	model := NewTiledModel(tasks, nil, 100, 50)

	// Initial selection should be 0
	if model.SelectedIndex() != 0 {
		t.Errorf("expected initial selection 0, got %d", model.SelectedIndex())
	}

	// Select by number
	model.SelectByNumber(3)
	if model.SelectedIndex() != 2 {
		t.Errorf("expected selection 2, got %d", model.SelectedIndex())
	}

	// Select by number out of range
	model.SelectByNumber(10)
	if model.SelectedIndex() != 2 {
		t.Errorf("expected selection to stay at 2, got %d", model.SelectedIndex())
	}

	// Get selected task
	task := model.SelectedTask()
	if task == nil {
		t.Fatal("expected selected task to be non-nil")
	}
	if task.ID != 3 {
		t.Errorf("expected selected task ID 3, got %d", task.ID)
	}
}

func TestTiledModel_HasActiveTasks(t *testing.T) {
	// Empty list
	model := NewTiledModel([]*db.Task{}, nil, 100, 50)
	if model.HasActiveTasks() {
		t.Error("expected HasActiveTasks to be false for empty list")
	}

	// Only backlog tasks
	backlogOnly := []*db.Task{
		{ID: 1, Title: "Backlog", Status: db.StatusBacklog},
	}
	model = NewTiledModel(backlogOnly, nil, 100, 50)
	if model.HasActiveTasks() {
		t.Error("expected HasActiveTasks to be false for backlog-only list")
	}

	// With active tasks
	withActive := []*db.Task{
		{ID: 1, Title: "Active", Status: db.StatusProcessing},
	}
	model = NewTiledModel(withActive, nil, 100, 50)
	if !model.HasActiveTasks() {
		t.Error("expected HasActiveTasks to be true when processing task exists")
	}
}

func TestTiledModel_RefreshTasks(t *testing.T) {
	initial := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusProcessing},
		{ID: 2, Title: "Task 2", Status: db.StatusProcessing},
	}

	model := NewTiledModel(initial, nil, 100, 50)
	model.SelectByNumber(2) // Select second task

	// Refresh with updated list
	updated := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusProcessing},
		// Task 2 is now done
		{ID: 3, Title: "Task 3", Status: db.StatusQueued},
	}

	model.RefreshTasks(updated)

	if model.TaskCount() != 2 {
		t.Errorf("expected 2 tasks after refresh, got %d", model.TaskCount())
	}

	// Selection should be clamped
	if model.SelectedIndex() != 1 {
		t.Errorf("expected selection clamped to 1, got %d", model.SelectedIndex())
	}
}

func TestTiledModel_View(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "My Processing Task", Status: db.StatusProcessing, Project: "myproject"},
		{ID: 2, Title: "My Queued Task", Status: db.StatusQueued},
	}

	model := NewTiledModel(tasks, nil, 80, 24)

	view := model.View()

	// Check that the view contains expected elements
	if view == "" {
		t.Error("expected non-empty view")
	}

	// View should contain task count
	if !strContains(view, "2 Active Tasks") {
		t.Error("expected view to contain '2 Active Tasks'")
	}
}

func TestTiledModel_EmptyView(t *testing.T) {
	model := NewTiledModel([]*db.Task{}, nil, 80, 24)

	view := model.View()

	if !strContains(view, "No active tasks") {
		t.Error("expected view to contain 'No active tasks'")
	}
}

// strContains checks if s contains substr
func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestTruncateTitle(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly ten chars", 10, "exactly t…"},
		{"longer than max", 10, "longer th…"},
		{"a", 5, "a"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := truncateTitle(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateTitle(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
