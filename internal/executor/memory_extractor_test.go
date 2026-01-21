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

func TestTruncateSummary(t *testing.T) {
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
			result := truncateSummary(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncateSummary(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}
