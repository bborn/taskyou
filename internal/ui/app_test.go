package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestInterruptKeyEnabled(t *testing.T) {
	// Create a test database
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer database.Close()

	// Create app model with nil executor (we'll test the keys directly)
	keys := DefaultKeyMap()

	// By default, the interrupt key should be enabled
	if !keys.Interrupt.Enabled() {
		t.Error("interrupt key should be enabled by default")
	}

	// After disabling, it should be disabled
	keys.Interrupt.SetEnabled(false)
	if keys.Interrupt.Enabled() {
		t.Error("interrupt key should be disabled after SetEnabled(false)")
	}

	// Re-enable
	keys.Interrupt.SetEnabled(true)
	if !keys.Interrupt.Enabled() {
		t.Error("interrupt key should be enabled after SetEnabled(true)")
	}
}

func TestDetailViewHelp(t *testing.T) {
	// Create a test database
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer database.Close()

	tests := []struct {
		name          string
		taskStatus    string
		wantInterrupt bool
	}{
		{
			name:          "backlog task should not show interrupt",
			taskStatus:    db.StatusBacklog,
			wantInterrupt: false,
		},
		{
			name:          "queued task should show interrupt",
			taskStatus:    db.StatusQueued,
			wantInterrupt: true,
		},
		{
			name:          "processing task should show interrupt",
			taskStatus:    db.StatusProcessing,
			wantInterrupt: true,
		},
		{
			name:          "done task should not show interrupt",
			taskStatus:    db.StatusDone,
			wantInterrupt: false,
		},
		{
			name:          "blocked task should not show interrupt",
			taskStatus:    db.StatusBlocked,
			wantInterrupt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create task with given status
			task := &db.Task{
				ID:     1,
				Title:  "Test Task",
				Status: tt.taskStatus,
			}
			if err := database.CreateTask(task); err != nil {
				t.Fatalf("failed to create task: %v", err)
			}
			defer database.DeleteTask(task.ID)

			// Create detail model
			model := NewDetailModel(task, database, 80, 24)

			// Render the help and check for interrupt key
			help := model.renderHelp()
			hasInterrupt := contains(help, "interrupt")

			if hasInterrupt != tt.wantInterrupt {
				t.Errorf("help contains interrupt = %v, want %v (status: %s)", hasInterrupt, tt.wantInterrupt, tt.taskStatus)
			}
		})
	}
}

// contains checks if substr is in s
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
