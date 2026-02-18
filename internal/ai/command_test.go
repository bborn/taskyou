package ai

import (
	"testing"
)

func TestExtractTaskID(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"move #42 to done", 42},
		{"close task 15", 15},
		{"#123", 123},
		{"task 99 is blocked", 99},
		{"no task id here", 0},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractTaskID(tt.input)
			if result != tt.expected {
				t.Errorf("extractTaskID(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseResponse_CreateTask(t *testing.T) {
	s := &CommandService{}

	response := `{"type":"create_task","title":"Fix auth bug","message":"Creating task: Fix auth bug"}`
	cmd, err := s.parseResponse(response, "create task about fixing auth bug")

	if err != nil {
		t.Fatalf("parseResponse error: %v", err)
	}
	if cmd.Type != CommandCreateTask {
		t.Errorf("Type = %v, want %v", cmd.Type, CommandCreateTask)
	}
	if cmd.Title != "Fix auth bug" {
		t.Errorf("Title = %q, want %q", cmd.Title, "Fix auth bug")
	}
}

func TestParseResponse_UpdateStatus(t *testing.T) {
	s := &CommandService{}

	response := `{"type":"update_status","task_id":42,"status":"done","message":"Marking task #42 as done"}`
	cmd, err := s.parseResponse(response, "move #42 to done")

	if err != nil {
		t.Fatalf("parseResponse error: %v", err)
	}
	if cmd.Type != CommandUpdateStatus {
		t.Errorf("Type = %v, want %v", cmd.Type, CommandUpdateStatus)
	}
	if cmd.TaskID != 42 {
		t.Errorf("TaskID = %d, want %d", cmd.TaskID, 42)
	}
	if cmd.Status != "done" {
		t.Errorf("Status = %q, want %q", cmd.Status, "done")
	}
}

func TestParseResponse_SelectTask(t *testing.T) {
	s := &CommandService{}

	response := `{"type":"select_task","task_id":7,"message":"Opening task #7"}`
	cmd, err := s.parseResponse(response, "go to task 7")

	if err != nil {
		t.Fatalf("parseResponse error: %v", err)
	}
	if cmd.Type != CommandSelectTask {
		t.Errorf("Type = %v, want %v", cmd.Type, CommandSelectTask)
	}
	if cmd.TaskID != 7 {
		t.Errorf("TaskID = %d, want %d", cmd.TaskID, 7)
	}
}

func TestParseResponse_SearchTasks(t *testing.T) {
	s := &CommandService{}

	response := `{"type":"search_tasks","query":"authentication","message":"Searching for tasks about authentication"}`
	cmd, err := s.parseResponse(response, "find tasks about authentication")

	if err != nil {
		t.Fatalf("parseResponse error: %v", err)
	}
	if cmd.Type != CommandSearchTasks {
		t.Errorf("Type = %v, want %v", cmd.Type, CommandSearchTasks)
	}
	if cmd.Query != "authentication" {
		t.Errorf("Query = %q, want %q", cmd.Query, "authentication")
	}
}

func TestParseResponse_InvalidJSON(t *testing.T) {
	s := &CommandService{}

	// When JSON is invalid, should fall back to search
	cmd, err := s.parseResponse("not valid json", "some input")

	if err != nil {
		t.Fatalf("parseResponse error: %v", err)
	}
	if cmd.Type != CommandSearchTasks {
		t.Errorf("Type = %v, want %v", cmd.Type, CommandSearchTasks)
	}
	if cmd.Query != "some input" {
		t.Errorf("Query = %q, want %q", cmd.Query, "some input")
	}
}

func TestParseResponse_WithProject(t *testing.T) {
	s := &CommandService{}

	response := `{"type":"create_task","title":"Add dark mode","project":"offerlab","message":"Creating task in offerlab: Add dark mode"}`
	cmd, err := s.parseResponse(response, "new task in offerlab: add dark mode")

	if err != nil {
		t.Fatalf("parseResponse error: %v", err)
	}
	if cmd.Type != CommandCreateTask {
		t.Errorf("Type = %v, want %v", cmd.Type, CommandCreateTask)
	}
	if cmd.Project != "offerlab" {
		t.Errorf("Project = %q, want %q", cmd.Project, "offerlab")
	}
}

func TestIsAvailable(t *testing.T) {
	// Without API key
	t.Setenv("ANTHROPIC_API_KEY", "")
	s1 := NewCommandService("")
	if s1.IsAvailable() {
		t.Error("IsAvailable() should return false without API key")
	}

	// With API key
	s2 := &CommandService{apiKey: "test-key"}
	if !s2.IsAvailable() {
		t.Error("IsAvailable() should return true with API key")
	}
}
