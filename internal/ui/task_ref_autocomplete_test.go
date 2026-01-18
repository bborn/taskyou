package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestTaskRefAutocompleteFiltering(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "Implement login feature", Project: "webapp", Status: db.StatusBacklog},
		{ID: 42, Title: "Fix bug in dashboard", Project: "webapp", Status: db.StatusProcessing},
		{ID: 123, Title: "Add unit tests", Project: "api", Status: db.StatusDone},
		{ID: 302, Title: "Task reference feature", Project: "workflow", Status: db.StatusQueued},
	}

	tests := []struct {
		name     string
		query    string
		expected int // number of expected results
	}{
		{"empty query shows all", "", 4},
		{"filter by ID prefix", "30", 1},
		{"filter by exact ID", "302", 1},
		{"filter by title word", "bug", 1},
		{"filter by title lowercase", "login", 1},
		{"filter by title partial", "ref", 1},
		{"no results", "nonexistent", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &TaskRefAutocompleteModel{
				allTasks:   tasks,
				maxVisible: 5,
				width:      60,
			}
			m.SetQuery(tt.query, 0)

			if len(m.filteredTasks) != tt.expected {
				t.Errorf("query %q: got %d results, want %d", tt.query, len(m.filteredTasks), tt.expected)
			}
		})
	}
}

func TestTaskRefAutocompleteNavigation(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "First task", Status: db.StatusBacklog},
		{ID: 2, Title: "Second task", Status: db.StatusBacklog},
		{ID: 3, Title: "Third task", Status: db.StatusBacklog},
	}

	m := &TaskRefAutocompleteModel{
		allTasks:   tasks,
		maxVisible: 5,
		width:      60,
	}
	m.SetQuery("", 0) // Show all

	// Initially at index 0
	if m.selectedIndex != 0 {
		t.Errorf("initial selectedIndex = %d, want 0", m.selectedIndex)
	}

	// Move down
	m.MoveDown()
	if m.selectedIndex != 1 {
		t.Errorf("after MoveDown selectedIndex = %d, want 1", m.selectedIndex)
	}

	// Move down again
	m.MoveDown()
	if m.selectedIndex != 2 {
		t.Errorf("after second MoveDown selectedIndex = %d, want 2", m.selectedIndex)
	}

	// Move down wraps to top
	m.MoveDown()
	if m.selectedIndex != 0 {
		t.Errorf("after wrap MoveDown selectedIndex = %d, want 0", m.selectedIndex)
	}

	// Move up wraps to bottom
	m.MoveUp()
	if m.selectedIndex != 2 {
		t.Errorf("after wrap MoveUp selectedIndex = %d, want 2", m.selectedIndex)
	}
}

func TestTaskRefAutocompleteSelection(t *testing.T) {
	tasks := []*db.Task{
		{ID: 302, Title: "Task reference feature", Status: db.StatusBacklog},
		{ID: 303, Title: "Another task", Status: db.StatusBacklog},
	}

	m := &TaskRefAutocompleteModel{
		allTasks:   tasks,
		maxVisible: 5,
		width:      60,
	}
	m.SetQuery("30", 5) // Set query and position

	// Before selection
	if m.SelectedTask() != nil {
		t.Error("expected nil SelectedTask before Select()")
	}

	// Select first item
	m.Select()
	selected := m.SelectedTask()
	if selected == nil {
		t.Fatal("expected non-nil SelectedTask after Select()")
	}
	if selected.ID != 302 {
		t.Errorf("selected task ID = %d, want 302", selected.ID)
	}

	// Check reference format (includes task title)
	ref := m.GetReference()
	expectedRef := "#302 (Task reference feature)"
	if ref != expectedRef {
		t.Errorf("GetReference() = %q, want %q", ref, expectedRef)
	}

	// Check query length (includes #)
	if m.GetQueryLength() != 3 { // "30" + "#"
		t.Errorf("GetQueryLength() = %d, want 3", m.GetQueryLength())
	}

	// Check cursor position
	if m.GetCursorPos() != 5 {
		t.Errorf("GetCursorPos() = %d, want 5", m.GetCursorPos())
	}
}

func TestTaskRefAutocompleteDismiss(t *testing.T) {
	m := &TaskRefAutocompleteModel{
		allTasks:   []*db.Task{{ID: 1, Title: "Test", Status: db.StatusBacklog}},
		maxVisible: 5,
		width:      60,
	}
	m.SetQuery("", 0)

	if m.IsDismissed() {
		t.Error("expected IsDismissed() = false before Dismiss()")
	}

	m.Dismiss()

	if !m.IsDismissed() {
		t.Error("expected IsDismissed() = true after Dismiss()")
	}
}

func TestTaskRefAutocompleteReset(t *testing.T) {
	tasks := []*db.Task{
		{ID: 302, Title: "Task reference feature", Status: db.StatusBacklog},
	}

	m := &TaskRefAutocompleteModel{
		allTasks:   tasks,
		maxVisible: 5,
		width:      60,
	}
	m.SetQuery("302", 5)
	m.Select()

	// Verify state before reset
	if m.SelectedTask() == nil {
		t.Error("expected SelectedTask to be set before Reset()")
	}

	// Reset
	m.Reset()

	// Verify state after reset
	if m.query != "" {
		t.Errorf("after Reset() query = %q, want empty", m.query)
	}
	if m.cursorPos != 0 {
		t.Errorf("after Reset() cursorPos = %d, want 0", m.cursorPos)
	}
	if m.selectedIndex != 0 {
		t.Errorf("after Reset() selectedIndex = %d, want 0", m.selectedIndex)
	}
	if m.SelectedTask() != nil {
		t.Error("after Reset() expected SelectedTask() = nil")
	}
	if m.IsDismissed() {
		t.Error("after Reset() expected IsDismissed() = false")
	}
}

func TestTaskRefAutocompleteHasResults(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "Test task", Status: db.StatusBacklog},
	}

	m := &TaskRefAutocompleteModel{
		allTasks:   tasks,
		maxVisible: 5,
		width:      60,
	}

	// No query yet
	if m.HasResults() {
		t.Error("expected HasResults() = false before SetQuery")
	}

	// Set query that matches
	m.SetQuery("", 0)
	if !m.HasResults() {
		t.Error("expected HasResults() = true after matching query")
	}

	// Set query with no matches
	m.SetQuery("nonexistent", 0)
	if m.HasResults() {
		t.Error("expected HasResults() = false for non-matching query")
	}
}

func TestTaskRefAutocompleteIDPrioritization(t *testing.T) {
	// When searching by ID, exact and prefix matches should be prioritized
	tasks := []*db.Task{
		{ID: 100, Title: "Hundred task", Status: db.StatusBacklog},
		{ID: 1, Title: "First task", Status: db.StatusBacklog},
		{ID: 10, Title: "Ten task", Status: db.StatusBacklog},
	}

	m := &TaskRefAutocompleteModel{
		allTasks:   tasks,
		maxVisible: 5,
		width:      60,
	}

	// Query "1" should find tasks with IDs containing "1"
	m.SetQuery("1", 0)

	if len(m.filteredTasks) < 1 {
		t.Fatal("expected at least 1 filtered task")
	}

	// Exact ID match (1) should be first
	if m.filteredTasks[0].ID != 1 {
		t.Errorf("first result ID = %d, want 1 (exact match)", m.filteredTasks[0].ID)
	}
}

func TestTaskRefAutocompleteView(t *testing.T) {
	tasks := []*db.Task{
		{ID: 302, Title: "Task reference feature", Status: db.StatusBacklog},
		{ID: 303, Title: "Another task", Status: db.StatusDone},
	}

	m := &TaskRefAutocompleteModel{
		allTasks:   tasks,
		maxVisible: 5,
		width:      60,
	}
	m.SetQuery("30", 0)

	view := m.View()

	// View should not be empty when there are results
	if view == "" {
		t.Error("expected non-empty View() when there are results")
	}

	// View should contain task IDs
	if !contains(view, "#302") {
		t.Error("expected View() to contain #302")
	}
	if !contains(view, "#303") {
		t.Error("expected View() to contain #303")
	}
}

func TestTaskRefAutocompleteEmptyView(t *testing.T) {
	m := &TaskRefAutocompleteModel{
		allTasks:   []*db.Task{},
		maxVisible: 5,
		width:      60,
	}

	view := m.View()
	if view != "" {
		t.Errorf("expected empty View() for no results, got %q", view)
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
