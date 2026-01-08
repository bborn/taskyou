package executor

import (
	"os"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func TestNeedsTriage(t *testing.T) {
	tests := []struct {
		name     string
		task     *db.Task
		expected bool
	}{
		{
			name: "empty project needs triage",
			task: &db.Task{
				Title:   "Fix the login bug",
				Body:    "The login form doesn't work properly",
				Project: "",
				Type:    "code",
			},
			expected: true,
		},
		{
			name: "empty type needs triage",
			task: &db.Task{
				Title:   "Fix the login bug",
				Body:    "The login form doesn't work properly",
				Project: "myapp",
				Type:    "",
			},
			expected: true,
		},
		{
			name: "short description needs triage",
			task: &db.Task{
				Title:   "Fix bug",
				Body:    "",
				Project: "myapp",
				Type:    "code",
			},
			expected: true,
		},
		{
			name: "well-defined task skips triage",
			task: &db.Task{
				Title:   "Implement user authentication with JWT tokens",
				Body:    "Add JWT-based authentication to the API endpoints",
				Project: "myapp",
				Type:    "code",
			},
			expected: false,
		},
		{
			name: "long title short body skips triage",
			task: &db.Task{
				Title:   "Implement user authentication with JWT tokens for the main API",
				Body:    "",
				Project: "myapp",
				Type:    "code",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsTriage(tt.task)
			if got != tt.expected {
				t.Errorf("NeedsTriage() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTaskIsWellDefined(t *testing.T) {
	// Create temp database
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cfg := &config.Config{}
	exec := New(database, cfg)

	tests := []struct {
		name     string
		task     *db.Task
		expected bool
	}{
		{
			name: "fully defined",
			task: &db.Task{
				Title:   "Implement user authentication",
				Body:    "Add JWT auth with refresh tokens",
				Project: "api",
				Type:    "code",
			},
			expected: true,
		},
		{
			name: "missing project",
			task: &db.Task{
				Title:   "Implement feature",
				Body:    "Some description here",
				Project: "",
				Type:    "code",
			},
			expected: false,
		},
		{
			name: "missing type",
			task: &db.Task{
				Title:   "Implement feature",
				Body:    "Some description here",
				Project: "myapp",
				Type:    "",
			},
			expected: false,
		},
		{
			name: "too short",
			task: &db.Task{
				Title:   "Fix bug",
				Body:    "",
				Project: "myapp",
				Type:    "code",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exec.taskIsWellDefined(tt.task)
			if got != tt.expected {
				t.Errorf("taskIsWellDefined() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain JSON",
			input:    `{"project": "myapp", "type": "code"}`,
			expected: `{"project": "myapp", "type": "code"}`,
		},
		{
			name:     "JSON with extra text",
			input:    `Here is the result: {"project": "myapp"}`,
			expected: ``,
		},
		{
			name: "JSON in code block",
			input: "```json\n{\"project\": \"myapp\"}\n```",
			expected: `{"project": "myapp"}`,
		},
		{
			name: "JSON in plain code block",
			input: "```\n{\"project\": \"myapp\"}\n```",
			expected: `{"project": "myapp"}`,
		},
		{
			name: "nested JSON",
			input: `{"outer": {"inner": "value"}, "other": 123}`,
			expected: `{"outer": {"inner": "value"}, "other": 123}`,
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.expected {
				t.Errorf("extractJSON() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBuildTriagePrompt(t *testing.T) {
	// Create temp database
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Add some test projects
	database.CreateProject(&db.Project{
		Name:         "workflow",
		Path:         "/home/user/workflow",
		Aliases:      "wf",
		Instructions: "Always run tests before committing",
	})
	database.CreateProject(&db.Project{
		Name: "api",
		Path: "/home/user/api",
	})

	cfg := &config.Config{}
	exec := New(database, cfg)

	task := &db.Task{
		Title: "Fix bug",
		Body:  "",
	}

	prompt := exec.buildTriagePrompt(task)

	// Should include task types
	if !strings.Contains(prompt, "code") || !strings.Contains(prompt, "writing") {
		t.Error("prompt should include task types")
	}

	// Should include projects
	if !strings.Contains(prompt, "workflow") || !strings.Contains(prompt, "api") {
		t.Error("prompt should include available projects")
	}

	// Should include project instructions
	if !strings.Contains(prompt, "Always run tests before committing") {
		t.Error("prompt should include project instructions")
	}

	// Should include the task
	if !strings.Contains(prompt, "Fix bug") {
		t.Error("prompt should include task title")
	}

	// Should ask for JSON response
	if !strings.Contains(prompt, "JSON") {
		t.Error("prompt should ask for JSON response")
	}
}

func TestApplyTriageResult(t *testing.T) {
	// Create temp database
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cfg := &config.Config{}
	exec := New(database, cfg)

	t.Run("apply project and type", func(t *testing.T) {
		task := &db.Task{
			Title:   "Fix bug",
			Body:    "",
			Project: "",
			Type:    "",
		}
		database.CreateTask(task)

		result := &TriageResult{
			Project: "myapp",
			Type:    "code",
		}

		exec.applyTriageResult(task, result)

		if task.Project != "myapp" {
			t.Errorf("expected project 'myapp', got %q", task.Project)
		}
		if task.Type != "code" {
			t.Errorf("expected type 'code', got %q", task.Type)
		}
	})

	t.Run("enhance body", func(t *testing.T) {
		task := &db.Task{
			Title:   "Fix bug",
			Body:    "Original body",
			Project: "myapp",
			Type:    "code",
		}
		database.CreateTask(task)

		result := &TriageResult{
			EnhancedBody: "Enhanced description with more details",
		}

		exec.applyTriageResult(task, result)

		if !strings.Contains(task.Body, "Original body") {
			t.Error("should preserve original body")
		}
		if !strings.Contains(task.Body, "Enhanced description") {
			t.Error("should include enhanced body")
		}
	})

	t.Run("don't overwrite existing values", func(t *testing.T) {
		task := &db.Task{
			Title:   "Fix bug",
			Project: "existing-project",
			Type:    "writing",
		}
		database.CreateTask(task)

		result := &TriageResult{
			Project: "new-project",
			Type:    "code",
		}

		exec.applyTriageResult(task, result)

		// Should keep existing values
		if task.Project != "existing-project" {
			t.Errorf("should not overwrite existing project, got %q", task.Project)
		}
		if task.Type != "writing" {
			t.Errorf("should not overwrite existing type, got %q", task.Type)
		}
	})
}

func TestProjectInstructionsInPrompt(t *testing.T) {
	// Create temp database
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Add a project with instructions
	database.CreateProject(&db.Project{
		Name:         "myapp",
		Path:         "/home/user/myapp",
		Instructions: "Always use TypeScript. Run npm test before committing.",
	})

	cfg := &config.Config{}
	exec := New(database, cfg)

	task := &db.Task{
		Title:   "Add login feature",
		Body:    "Implement OAuth2 login",
		Project: "myapp",
		Type:    "code",
	}

	prompt := exec.buildPrompt(task, nil)

	// Should include project instructions
	if !strings.Contains(prompt, "Project Instructions") {
		t.Error("prompt should include project instructions section")
	}
	if !strings.Contains(prompt, "Always use TypeScript") {
		t.Error("prompt should include actual project instructions")
	}
}
