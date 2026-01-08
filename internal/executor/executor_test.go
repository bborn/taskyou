package executor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func TestNeedsInputDetection(t *testing.T) {
	tests := []struct {
		name     string
		lines    []string
		wantMsg  string
		wantNeed bool
	}{
		{
			name:     "simple NEEDS_INPUT",
			lines:    []string{"NEEDS_INPUT: What should be the database name?"},
			wantMsg:  "What should be the database name?",
			wantNeed: true,
		},
		{
			name:     "NEEDS_INPUT in middle of output",
			lines:    []string{"Processing...", "NEEDS_INPUT: Please clarify the requirements", "More output"},
			wantMsg:  "Please clarify the requirements",
			wantNeed: true,
		},
		{
			name:     "TASK_COMPLETE",
			lines:    []string{"Done", "TASK_COMPLETE"},
			wantMsg:  "",
			wantNeed: false,
		},
		{
			name:     "no markers",
			lines:    []string{"Some output", "More output"},
			wantMsg:  "",
			wantNeed: false,
		},
		{
			name:     "NEEDS_INPUT with extra whitespace",
			lines:    []string{"NEEDS_INPUT:   What's the API key?  "},
			wantMsg:  "What's the API key?",
			wantNeed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMsg, gotNeed := detectNeedsInput(tt.lines)
			if gotNeed != tt.wantNeed {
				t.Errorf("detectNeedsInput() needsInput = %v, want %v", gotNeed, tt.wantNeed)
			}
			if gotMsg != tt.wantMsg {
				t.Errorf("detectNeedsInput() message = %q, want %q", gotMsg, tt.wantMsg)
			}
		})
	}
}

// detectNeedsInput is a helper function that mirrors the detection logic in runCrush
func detectNeedsInput(lines []string) (string, bool) {
	var needsInputMsg string
	var foundComplete bool

	for _, line := range lines {
		if strings.Contains(line, "TASK_COMPLETE") {
			foundComplete = true
		}
		if strings.Contains(line, "NEEDS_INPUT:") {
			if idx := strings.Index(line, "NEEDS_INPUT:"); idx >= 0 {
				needsInputMsg = strings.TrimSpace(line[idx+len("NEEDS_INPUT:"):])
			}
		}
	}

	if foundComplete {
		return "", false
	}
	if needsInputMsg != "" {
		return needsInputMsg, true
	}
	return "", false
}

func TestInterrupt(t *testing.T) {
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

	// Create a test task
	task := &db.Task{
		Title:   "Test task",
		Status:  db.StatusInProgress,
		Project: "test",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	// Mark as running
	exec.mu.Lock()
	exec.runningTasks[task.ID] = true
	ctx, cancel := context.WithCancel(context.Background())
	exec.cancelFuncs[task.ID] = cancel
	exec.mu.Unlock()

	// Test IsRunning
	if !exec.IsRunning(task.ID) {
		t.Error("expected task to be running")
	}

	// Test Interrupt
	result := exec.Interrupt(task.ID)
	if !result {
		t.Error("expected interrupt to return true")
	}

	// Wait a bit for context to be cancelled
	time.Sleep(10 * time.Millisecond)

	// Verify context was cancelled
	select {
	case <-ctx.Done():
		// Expected
	default:
		t.Error("expected context to be cancelled")
	}

	// Verify status was updated
	updatedTask, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedTask.Status != db.StatusBacklog {
		t.Errorf("expected status %q, got %q", db.StatusBacklog, updatedTask.Status)
	}
}

func TestRunningTasks(t *testing.T) {
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

	// No running tasks initially
	if len(exec.RunningTasks()) != 0 {
		t.Error("expected no running tasks")
	}

	// Add some running tasks
	exec.mu.Lock()
	exec.runningTasks[1] = true
	exec.runningTasks[2] = true
	exec.mu.Unlock()

	// Should have 2 running tasks
	running := exec.RunningTasks()
	if len(running) != 2 {
		t.Errorf("expected 2 running tasks, got %d", len(running))
	}
}

func TestConversationHistory(t *testing.T) {
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

	// Create a test task
	task := &db.Task{
		Title:   "Test task",
		Status:  db.StatusBacklog,
		Type:    db.TypeCode,
		Project: "test",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	t.Run("no history for fresh task", func(t *testing.T) {
		prompt := exec.buildPrompt(task)
		if strings.Contains(prompt, "Previous Conversation") {
			t.Error("fresh task should not have conversation history")
		}
	})

	t.Run("history after retry with feedback", func(t *testing.T) {
		// Simulate a first run with a question
		database.AppendTaskLog(task.ID, "system", "Starting task #1: Test task")
		database.AppendTaskLog(task.ID, "output", "Looking at the code...")
		database.AppendTaskLog(task.ID, "question", "What database name should I use?")
		database.AppendTaskLog(task.ID, "system", "Task needs input - use 'r' to retry with your answer")

		// Simulate retry with feedback
		database.RetryTask(task.ID, "Use 'myapp_production' as the database name")

		prompt := exec.buildPrompt(task)

		if !strings.Contains(prompt, "Previous Conversation") {
			t.Error("retry should include conversation history")
		}
		if !strings.Contains(prompt, "What database name should I use?") {
			t.Error("retry should include the original question")
		}
		if !strings.Contains(prompt, "myapp_production") {
			t.Error("retry should include user's feedback")
		}
	})

	t.Run("multiple continuations", func(t *testing.T) {
		// Clear logs for fresh test
		database.ClearTaskLogs(task.ID)

		// First run
		database.AppendTaskLog(task.ID, "question", "First question?")
		database.AppendTaskLog(task.ID, "system", "--- Continuation ---")
		database.AppendTaskLog(task.ID, "text", "Feedback: First answer")

		// Second run
		database.AppendTaskLog(task.ID, "question", "Second question?")
		database.AppendTaskLog(task.ID, "system", "--- Continuation ---")
		database.AppendTaskLog(task.ID, "text", "Feedback: Second answer")

		prompt := exec.buildPrompt(task)

		if !strings.Contains(prompt, "First question?") {
			t.Error("should include first question")
		}
		if !strings.Contains(prompt, "First answer") {
			t.Error("should include first answer")
		}
		if !strings.Contains(prompt, "Second question?") {
			t.Error("should include second question")
		}
		if !strings.Contains(prompt, "Second answer") {
			t.Error("should include second answer")
		}
	})
}
