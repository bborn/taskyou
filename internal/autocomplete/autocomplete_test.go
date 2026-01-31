package autocomplete

import (
	"context"
	"strings"
	"testing"
)

func TestBuildPrompt_Title(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		project      string
		extraContext string
		recentTasks  []string
		wantContains []string
	}{
		{
			name:         "basic title completion",
			input:        "Fix the",
			project:      "",
			extraContext: "",
			recentTasks:  nil,
			wantContains: []string{"Complete", "title", "Fix the"},
		},
		{
			name:         "title with project context",
			input:        "Add tests",
			project:      "myproject",
			extraContext: "",
			recentTasks:  nil,
			wantContains: []string{"Project: myproject", "Add tests"},
		},
		{
			name:         "personal project excluded from context",
			input:        "Update docs",
			project:      "personal",
			extraContext: "",
			recentTasks:  nil,
			wantContains: []string{"Update docs"},
		},
		{
			name:         "title with recent tasks",
			input:        "Fix",
			project:      "",
			extraContext: "",
			recentTasks:  []string{"Fix authentication bug", "Add user tests"},
			wantContains: []string{"Recent tasks", "Fix authentication bug", "Add user tests"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPrompt(tt.input, "title", tt.project, tt.extraContext, tt.recentTasks)
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
			wantContains: []string{"Continue", "This task will"},
		},
		{
			name:         "body with task title context",
			input:        "Users should be able to",
			project:      "",
			extraContext: "Add login feature",
			wantContains: []string{"Task: Add login feature", "Users should be able to"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPrompt(tt.input, "body", tt.project, tt.extraContext, nil)
			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("buildPrompt() = %q, want to contain %q", result, want)
				}
			}
		})
	}
}

func TestBuildPrompt_BodySuggest(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		extraContext string
		wantContains []string
	}{
		{
			name:         "body_suggest uses title from extraContext",
			input:        "",
			extraContext: "Fix authentication bug",
			wantContains: []string{"Task title: Fix authentication bug", "Description:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPrompt(tt.input, "body_suggest", "", tt.extraContext, nil)
			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("buildPrompt() = %q, want to contain %q", result, want)
				}
			}
		})
	}
}

func TestNewService(t *testing.T) {
	svc := NewService("")
	if svc == nil {
		t.Fatal("NewService() returned nil")
	}
	if svc.cache == nil {
		t.Error("cache should be initialized")
	}
	if svc.cacheSize == 0 {
		t.Error("cacheSize should be set")
	}
}

func TestNewService_WithAPIKey(t *testing.T) {
	svc := NewService("test-api-key")
	if svc == nil {
		t.Fatal("NewService() returned nil")
	}
	if svc.apiKey != "test-api-key" {
		t.Errorf("apiKey = %q, want %q", svc.apiKey, "test-api-key")
	}
}

func TestService_IsAvailable(t *testing.T) {
	svc := NewService("")
	if svc.IsAvailable() {
		t.Error("IsAvailable() should return false without API key")
	}

	svc = NewService("test-key")
	if !svc.IsAvailable() {
		t.Error("IsAvailable() should return true with API key")
	}
}

func TestService_Cancel(t *testing.T) {
	svc := NewService("")
	// Should not panic when called with no active request
	svc.Cancel()
}

func TestService_Cache(t *testing.T) {
	svc := NewService("")

	// Test adding to cache
	svc.addToCache("title:project:fix", "Fix the bug")
	cached := svc.getFromCache("title:project:fix", "Fix", "title")
	if cached != "Fix the bug" {
		t.Errorf("getFromCache() = %q, want %q", cached, "Fix the bug")
	}

	// Test cache miss
	cached = svc.getFromCache("title:project:nonexistent", "NonExistent", "title")
	if cached != "" {
		t.Errorf("getFromCache() for non-existent key = %q, want empty string", cached)
	}

	// Test cache update
	svc.addToCache("title:project:fix", "Fix the critical bug")
	cached = svc.getFromCache("title:project:fix", "Fix", "title")
	if cached != "Fix the critical bug" {
		t.Errorf("getFromCache() after update = %q, want %q", cached, "Fix the critical bug")
	}
}

func TestService_CacheEviction(t *testing.T) {
	svc := NewService("")
	svc.cacheSize = 3 // Small cache for testing eviction

	// Fill the cache
	svc.addToCache("key1", "value1")
	svc.addToCache("key2", "value2")
	svc.addToCache("key3", "value3")

	// Add one more - should evict key1 (oldest)
	svc.addToCache("key4", "value4")

	// key1 should be evicted
	if svc.getFromCache("key1", "input1", "body") != "" {
		t.Error("key1 should have been evicted")
	}

	// Other keys should still exist
	if svc.getFromCache("key2", "input2", "body") != "value2" {
		t.Error("key2 should still exist")
	}
	if svc.getFromCache("key3", "input3", "body") != "value3" {
		t.Error("key3 should still exist")
	}
	if svc.getFromCache("key4", "input4", "body") != "value4" {
		t.Error("key4 should exist")
	}
}

func TestService_CachePrefixMatching(t *testing.T) {
	svc := NewService("")

	// Add a longer suggestion to cache
	svc.addToCache("title:project:fix the bug", "Fix the bug in authentication")

	// Should match when input is a prefix of the cached suggestion (title field only)
	cached := svc.getFromCache("title:project:fix", "Fix", "title")
	if cached != "Fix the bug in authentication" {
		t.Errorf("prefix matching returned %q, want %q", cached, "Fix the bug in authentication")
	}

	// Should still work with different casing
	cached = svc.getFromCache("title:project:FIX", "FIX", "title")
	if cached != "Fix the bug in authentication" {
		t.Errorf("case-insensitive prefix matching returned %q, want %q", cached, "Fix the bug in authentication")
	}

	// Body field should NOT do prefix matching (only title)
	cached = svc.getFromCache("body:project:fix", "Fix", "body")
	if cached != "" {
		t.Errorf("body field should not do prefix matching, got %q", cached)
	}
}

func TestNormalizeInput(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Fix the bug", "fix the bug"},
		{"Fix the bug ", "fix the bug"},
		{"Fix the bug  ", "fix the bug"},
		{"Fix the bug\t", "fix the bug"},
		{"  Fix  ", "  fix"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeInput(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeInput(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestService_ProcessSuggestion_Title(t *testing.T) {
	svc := NewService("")

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
			result := svc.processSuggestion(tt.suggestion, tt.input, "title", 1)
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

func TestService_ProcessSuggestion_Body(t *testing.T) {
	svc := NewService("")

	tests := []struct {
		name       string
		suggestion string
		input      string
		fieldType  string
		wantNil    bool
		wantSuffix string
	}{
		{
			name:       "body suggestion is used directly as suffix",
			suggestion: "and fix the authentication flow",
			input:      "Update the login page",
			fieldType:  "body",
			wantNil:    false,
			wantSuffix: "and fix the authentication flow",
		},
		{
			name:       "body_suggest suggestion is used directly",
			suggestion: "Fix the bug that causes login failures",
			input:      "",
			fieldType:  "body_suggest",
			wantNil:    false,
			wantSuffix: "Fix the bug that causes login failures",
		},
		{
			name:       "empty body suggestion returns nil",
			suggestion: "",
			input:      "Some input",
			fieldType:  "body",
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.processSuggestion(tt.suggestion, tt.input, tt.fieldType, 1)
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
	svc := NewService("")

	// Should not panic - Warmup is now a no-op for direct API calls
	svc.Warmup()

	// Calling warmup twice should be safe
	svc.Warmup()
}

func TestBuildTitleGenerationPrompt(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		project      string
		wantContains []string
	}{
		{
			name:         "basic title generation",
			body:         "The login button is broken on mobile devices",
			project:      "",
			wantContains: []string{"Generate a concise task title", "login button is broken", "imperative form"},
		},
		{
			name:         "title generation with project",
			body:         "Fix the authentication bug",
			project:      "myapp",
			wantContains: []string{"Project: myapp", "authentication bug"},
		},
		{
			name:         "personal project excluded",
			body:         "Update documentation",
			project:      "personal",
			wantContains: []string{"Update documentation"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildTitleGenerationPrompt(tt.body, tt.project)
			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("buildTitleGenerationPrompt() = %q, want to contain %q", result, want)
				}
			}
			// Personal project should not appear in prompt
			if tt.project == "personal" && strings.Contains(result, "Project: personal") {
				t.Error("buildTitleGenerationPrompt() should not include 'Project: personal'")
			}
		})
	}
}

func TestGenerateTitle_NoAPIKey(t *testing.T) {
	svc := NewService("")
	_, err := svc.GenerateTitle(context.TODO(), "Some description", "")
	if err == nil {
		t.Error("GenerateTitle() should return error when no API key")
	}
}

func TestGenerateTitle_EmptyBody(t *testing.T) {
	svc := NewService("test-api-key")
	_, err := svc.GenerateTitle(context.TODO(), "", "")
	if err == nil {
		t.Error("GenerateTitle() should return error when body is empty")
	}

	_, err = svc.GenerateTitle(context.TODO(), "   ", "")
	if err == nil {
		t.Error("GenerateTitle() should return error when body is whitespace only")
	}
}
