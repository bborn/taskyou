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
		Status:  db.StatusProcessing,
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
	if updatedTask.Status != db.StatusInterrupted {
		t.Errorf("expected status %q, got %q", db.StatusInterrupted, updatedTask.Status)
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
