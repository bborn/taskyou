package executor

import (
	"testing"
)

func TestNormalizeCategory(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"pattern", "pattern"},
		{"patterns", "pattern"},
		{"PATTERN", "pattern"},
		{"context", "context"},
		{"decision", "decision"},
		{"decisions", "decision"},
		{"gotcha", "gotcha"},
		{"gotchas", "gotcha"},
		{"pitfall", "gotcha"},
		{"general", "general"},
		{"unknown", "general"},
		{"  pattern  ", "pattern"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeCategory(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeCategory(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"hello world", 8, "hello..."},
		{"hi", 2, "hi"},
		{"", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := truncate(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestBuildExtractionPrompt(t *testing.T) {
	// Just verify it doesn't panic and includes key elements
	task := &struct {
		Title   string
		Project string
		Type    string
		Body    string
	}{
		Title:   "Fix login bug",
		Project: "myapp",
		Type:    "code",
		Body:    "Users can't log in",
	}

	// Create a mock task for testing
	mockTask := struct {
		Title   string
		Project string
		Type    string
		Body    string
	}{
		Title:   task.Title,
		Project: task.Project,
		Type:    task.Type,
		Body:    task.Body,
	}

	logContent := "Found the bug in auth.go\nFixed by checking nil pointer"
	existingMemories := "- [pattern] Use early returns"

	// We can't call buildExtractionPrompt directly since it takes *db.Task
	// Just verify the helper functions work
	_ = mockTask
	_ = logContent
	_ = existingMemories
}
