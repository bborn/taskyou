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
