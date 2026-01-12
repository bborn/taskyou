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

func TestExtractPRNumber(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"full GitHub URL", "https://github.com/offerlab/offerlab/pull/2382", "2382"},
		{"URL without https", "github.com/offerlab/offerlab/pull/2382", "2382"},
		{"just PR number", "2382", "2382"},
		{"small PR number", "42", "42"},
		{"single digit", "5", "5"},
		{"not a PR URL", "some random text", ""},
		{"task ID format with hash", "#123", ""},
		{"too long to be PR number", "1234567", ""},
		{"URL with extra path", "github.com/owner/repo/pull/123/files", "123"},
		{"mixed case", "GitHub.com/Owner/Repo/pull/456", ""},
		{"partial URL", "owner/repo/pull/789", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPRNumber(tt.query)
			if got != tt.want {
				t.Errorf("extractPRNumber(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestMatchesPRNumber(t *testing.T) {
	tests := []struct {
		name     string
		task     *db.Task
		prNumber string
		want     bool
	}{
		{
			name: "PR URL in body",
			task: &db.Task{
				ID:    1,
				Title: "Fix bug",
				Body:  "See https://github.com/offerlab/offerlab/pull/2382 for details",
			},
			prNumber: "2382",
			want:     true,
		},
		{
			name: "PR reference in body",
			task: &db.Task{
				ID:    2,
				Title: "Fix bug",
				Body:  "Fixes #2382",
			},
			prNumber: "2382",
			want:     true,
		},
		{
			name: "PR URL in title",
			task: &db.Task{
				ID:    3,
				Title: "Review PR github.com/offerlab/offerlab/pull/2382",
				Body:  "",
			},
			prNumber: "2382",
			want:     true,
		},
		{
			name: "PR reference in title",
			task: &db.Task{
				ID:    4,
				Title: "Implement feature from PR #2382",
				Body:  "",
			},
			prNumber: "2382",
			want:     true,
		},
		{
			name: "no match",
			task: &db.Task{
				ID:    5,
				Title: "Some other task",
				Body:  "No PR references here",
			},
			prNumber: "2382",
			want:     false,
		},
		{
			name: "different PR number",
			task: &db.Task{
				ID:    6,
				Title: "Task for PR #1234",
				Body:  "",
			},
			prNumber: "2382",
			want:     false,
		},
		{
			name: "partial number match should not match",
			task: &db.Task{
				ID:    7,
				Title: "PR #238",
				Body:  "",
			},
			prNumber: "2382",
			want:     false,
		},
		{
			name: "PR number as part of larger number should not match",
			task: &db.Task{
				ID:    8,
				Title: "Issue #23821",
				Body:  "",
			},
			prNumber: "2382",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesPRNumber(tt.task, tt.prNumber)
			if got != tt.want {
				t.Errorf("matchesPRNumber(%+v, %q) = %v, want %v", tt.task, tt.prNumber, got, tt.want)
			}
		})
	}
}

func TestCommandPaletteFilteringWithPRs(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "Implement login", Body: "See https://github.com/offerlab/offerlab/pull/2382", Project: "webapp"},
		{ID: 2, Title: "Fix dashboard", Body: "Fixes #2382", Project: "webapp"},
		{ID: 3, Title: "Review PR #1234", Body: "", Project: "api"},
		{ID: 4, Title: "Another task", Body: "No PR here", Project: "api"},
	}

	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{"search by full PR URL", "https://github.com/offerlab/offerlab/pull/2382", 2},
		{"search by PR number", "2382", 2},
		{"search different PR", "1234", 1},
		{"no match for non-existent PR", "9999", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
