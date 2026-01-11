package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		name    string
		str     string
		pattern string
		want    bool
	}{
		{"empty pattern", "hello world", "", true},
		{"empty string", "", "abc", false},
		{"exact match", "hello", "hello", true},
		{"substring at start", "hello", "hel", true},
		{"substring in middle", "hello", "ell", true},
		{"non-contiguous chars", "hello world", "hwd", true},
		{"non-contiguous chars complex", "implement feature", "ipf", true},
		{"no match", "hello", "xyz", false},
		{"pattern longer than string", "hi", "hello", false},
		{"case sensitive no match", "Hello", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fuzzyMatch(tt.str, tt.pattern)
			if got != tt.want {
				t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.str, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestCommandPaletteFiltering(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "Implement login feature", Project: "webapp", Status: db.StatusBacklog},
		{ID: 2, Title: "Fix bug in dashboard", Project: "webapp", Status: db.StatusProcessing},
		{ID: 3, Title: "Add unit tests", Project: "api", Status: db.StatusDone},
		{ID: 42, Title: "Command palette", Project: "workflow", Status: db.StatusQueued},
	}

	tests := []struct {
		name     string
		query    string
		expected int // number of expected results
	}{
		{"empty query shows all", "", 4},
		{"filter by ID", "42", 1},
		{"filter by ID with hash", "#42", 1},
		{"filter by title word", "bug", 1},
		{"filter by project", "webapp", 2},
		{"filter by status", "done", 1},
		{"fuzzy match", "ilf", 1}, // "Implement login feature"
		{"no results", "nonexistent", 0},
		{"partial ID match", "4", 1}, // matches only ID 42 (contains "4")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal command palette model for testing
			m := &CommandPaletteModel{
				allTasks: tasks,
			}
			m.searchInput.SetValue(tt.query)
			m.filterTasks()

			if len(m.filteredTasks) != tt.expected {
				t.Errorf("query %q: got %d results, want %d", tt.query, len(m.filteredTasks), tt.expected)
			}
		})
	}
}

func TestMatchesQuery(t *testing.T) {
	task := &db.Task{
		ID:      123,
		Title:   "Implement search feature",
		Project: "myproject",
		Status:  db.StatusBacklog,
	}

	m := &CommandPaletteModel{}

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"match by ID", "123", true},
		{"match by ID with hash", "#123", true},
		{"partial ID", "12", true},
		{"match by title", "search", true},
		{"match by project", "myproject", true},
		{"match by status", "backlog", true},
		{"fuzzy match title", "isf", true}, // "Implement search feature"
		{"no match", "xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.matchesQuery(task, tt.query)
			if got != tt.want {
				t.Errorf("matchesQuery(%+v, %q) = %v, want %v", task, tt.query, got, tt.want)
			}
		})
	}
}
