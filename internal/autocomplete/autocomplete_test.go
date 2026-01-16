package autocomplete

import (
	"strings"
	"testing"
)

func TestBuildPrompt_Title(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		project      string
		extraContext string
		wantContains []string
	}{
		{
			name:         "basic title completion",
			input:        "Fix the",
			project:      "",
			extraContext: "",
			wantContains: []string{"Complete this partial task title", "Fix the"},
		},
		{
			name:         "title with project context",
			input:        "Add tests",
			project:      "myproject",
			extraContext: "",
			wantContains: []string{"Project context: myproject", "Add tests"},
		},
		{
			name:         "personal project excluded from context",
			input:        "Update docs",
			project:      "personal",
			extraContext: "",
			wantContains: []string{"Update docs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPrompt(tt.input, "title", tt.project, tt.extraContext)
			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("buildPrompt() = %q, want to contain %q", result, want)
				}
			}
		})
	}
}

func TestBuildPrompt_Body(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		project      string
		extraContext string
		wantContains []string
	}{
		{
			name:         "basic body completion",
			input:        "This task will",
			project:      "",
			extraContext: "",
			wantContains: []string{"Complete this partial task description", "This task will"},
		},
		{
			name:         "body with task title context",
			input:        "Users should be able to",
			project:      "",
			extraContext: "Add login feature",
			wantContains: []string{"Task title: Add login feature", "Users should be able to"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPrompt(tt.input, "body", tt.project, tt.extraContext)
			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("buildPrompt() = %q, want to contain %q", result, want)
				}
			}
		})
	}
}

func TestNewService(t *testing.T) {
	svc := NewService()
	if svc == nil {
		t.Fatal("NewService() returned nil")
	}
	if svc.debounceDelay == 0 {
		t.Error("debounceDelay should be set")
	}
	if svc.timeout == 0 {
		t.Error("timeout should be set")
	}
}

func TestService_Cancel(t *testing.T) {
	svc := NewService()
	// Should not panic when called with no active request
	svc.Cancel()
}

func TestService_Cache(t *testing.T) {
	svc := NewService()

	// Test adding to cache
	svc.addToCache("title:project:Fix", "Fix the bug")
	cached := svc.getFromCache("title:project:Fix")
	if cached != "Fix the bug" {
		t.Errorf("getFromCache() = %q, want %q", cached, "Fix the bug")
	}

	// Test cache miss
	cached = svc.getFromCache("title:project:NonExistent")
	if cached != "" {
		t.Errorf("getFromCache() for non-existent key = %q, want empty string", cached)
	}

	// Test cache update
	svc.addToCache("title:project:Fix", "Fix the critical bug")
	cached = svc.getFromCache("title:project:Fix")
	if cached != "Fix the critical bug" {
		t.Errorf("getFromCache() after update = %q, want %q", cached, "Fix the critical bug")
	}
}

func TestService_CacheEviction(t *testing.T) {
	svc := NewService()
	svc.cacheSize = 3 // Small cache for testing eviction

	// Fill the cache
	svc.addToCache("key1", "value1")
	svc.addToCache("key2", "value2")
	svc.addToCache("key3", "value3")

	// Add one more - should evict key1 (oldest)
	svc.addToCache("key4", "value4")

	// key1 should be evicted
	if svc.getFromCache("key1") != "" {
		t.Error("key1 should have been evicted")
	}

	// Other keys should still exist
	if svc.getFromCache("key2") != "value2" {
		t.Error("key2 should still exist")
	}
	if svc.getFromCache("key3") != "value3" {
		t.Error("key3 should still exist")
	}
	if svc.getFromCache("key4") != "value4" {
		t.Error("key4 should exist")
	}
}

func TestService_ProcessSuggestion(t *testing.T) {
	svc := NewService()

	tests := []struct {
		name       string
		suggestion string
		input      string
		wantNil    bool
		wantSuffix string
	}{
		{
			name:       "valid suggestion",
			suggestion: "Fix the bug",
			input:      "Fix",
			wantNil:    false,
			wantSuffix: " the bug",
		},
		{
			name:       "empty suggestion",
			suggestion: "",
			input:      "Fix",
			wantNil:    true,
		},
		{
			name:       "suggestion equals input",
			suggestion: "Fix",
			input:      "Fix",
			wantNil:    true,
		},
		{
			name:       "suggestion does not start with input",
			suggestion: "Update the docs",
			input:      "Fix",
			wantNil:    true,
		},
		{
			name:       "case-insensitive prefix match",
			suggestion: "fix the bug",
			input:      "Fix",
			wantNil:    false,
			wantSuffix: " the bug",
		},
		{
			name:       "removes surrounding quotes",
			suggestion: "\"Fix the bug\"",
			input:      "Fix",
			wantNil:    false,
			wantSuffix: " the bug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.processSuggestion(tt.suggestion, tt.input, 1)
			if tt.wantNil {
				if result != nil {
					t.Errorf("processSuggestion() = %+v, want nil", result)
				}
			} else {
				if result == nil {
					t.Fatal("processSuggestion() returned nil, want non-nil")
				}
				if result.Text != tt.wantSuffix {
					t.Errorf("processSuggestion().Text = %q, want %q", result.Text, tt.wantSuffix)
				}
			}
		})
	}
}

func TestService_Warmup(t *testing.T) {
	svc := NewService()

	// Should not panic
	svc.Warmup()

	// Calling warmup twice should be safe (only runs once)
	svc.Warmup()

	if !svc.warmedUp {
		t.Error("warmedUp should be true after Warmup()")
	}
}
