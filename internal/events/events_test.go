package events

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()

	// Create temp database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	return database
}

func TestEventEmission(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	// Create hooks directory
	hooksDir := t.TempDir()

	// Create event manager
	mgr := NewSilent(database, hooksDir)
	defer mgr.Stop()

	// Subscribe to events
	eventCh := mgr.Subscribe()
	defer mgr.Unsubscribe(eventCh)

	// Create a test task
	task := &db.Task{
		ID:     123,
		Title:  "Test Task",
		Status: db.StatusBacklog,
	}

	// Emit task created event
	mgr.EmitTaskCreated(task)

	// Wait for event
	select {
	case event := <-eventCh:
		if event.Type != EventTaskCreated {
			t.Errorf("Expected event type %s, got %s", EventTaskCreated, event.Type)
		}
		if event.TaskID != 123 {
			t.Errorf("Expected task ID 123, got %d", event.TaskID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for event")
	}
}

func TestEventLog(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	hooksDir := t.TempDir()
	mgr := NewSilent(database, hooksDir)
	defer mgr.Stop()

	// Create a test task in database
	task := &db.Task{
		Title:  "Test Task",
		Status: db.StatusBacklog,
		Body:   "Test body",
		Project: "personal",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Emit event synchronously to ensure it's logged before we query
	mgr.EmitSync(Event{
		Type:    EventTaskCreated,
		TaskID:  task.ID,
		Task:    task,
		Message: "Task created",
	})

	// Give it a moment to write to database
	time.Sleep(100 * time.Millisecond)

	// Query event log
	var count int
	err := database.QueryRow("SELECT COUNT(*) FROM event_log WHERE task_id = ?", task.ID).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query event log: %v", err)
	}

	if count == 0 {
		t.Error("Event was not logged to database")
	}
}

func TestStatusChangeEvents(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	hooksDir := t.TempDir()
	mgr := NewSilent(database, hooksDir)
	defer mgr.Stop()

	eventCh := mgr.Subscribe()
	defer mgr.Unsubscribe(eventCh)

	task := &db.Task{
		ID:     456,
		Title:  "Status Test",
		Status: db.StatusBacklog,
	}

	// Test various status transitions
	testCases := []struct {
		oldStatus string
		newStatus string
		eventType string
	}{
		{db.StatusBacklog, db.StatusQueued, EventTaskQueued},
		{db.StatusQueued, db.StatusProcessing, EventTaskProcessing},
		{db.StatusProcessing, db.StatusBlocked, EventTaskBlocked},
		{db.StatusBlocked, db.StatusDone, EventTaskCompleted},
	}

	for _, tc := range testCases {
		t.Run(tc.eventType, func(t *testing.T) {
			task.Status = tc.newStatus

			// Emit status changed event
			mgr.EmitTaskStatusChanged(task, tc.oldStatus, tc.newStatus)

			// Wait for event
			select {
			case event := <-eventCh:
				if event.Type != EventTaskStatusChanged {
					t.Errorf("Expected event type %s, got %s", EventTaskStatusChanged, event.Type)
				}
				if event.Metadata["old_status"] != tc.oldStatus {
					t.Errorf("Expected old status %s, got %v", tc.oldStatus, event.Metadata["old_status"])
				}
				if event.Metadata["new_status"] != tc.newStatus {
					t.Errorf("Expected new status %s, got %v", tc.newStatus, event.Metadata["new_status"])
				}
			case <-time.After(1 * time.Second):
				t.Fatal("Timeout waiting for event")
			}
		})
	}
}

func TestHookExecution(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	hooksDir := t.TempDir()
	mgr := New(database, hooksDir)
	defer mgr.Stop()

	// Create a simple hook script
	hookPath := filepath.Join(hooksDir, EventTaskCreated)
	hookScript := `#!/bin/bash
echo "Task $TASK_ID: $TASK_TITLE" > ` + filepath.Join(hooksDir, "hook_output.txt")

	if err := os.WriteFile(hookPath, []byte(hookScript), 0755); err != nil {
		t.Fatalf("Failed to create hook script: %v", err)
	}

	task := &db.Task{
		ID:     789,
		Title:  "Hook Test",
		Status: db.StatusBacklog,
	}

	// Emit event
	mgr.EmitTaskCreated(task)

	// Wait for hook to execute (it runs in background)
	time.Sleep(500 * time.Millisecond)

	// Check if hook created output file
	outputPath := filepath.Join(hooksDir, "hook_output.txt")
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Error("Hook script was not executed")
	}
}

func TestWebhookManagement(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	hooksDir := t.TempDir()
	mgr := NewSilent(database, hooksDir)
	defer mgr.Stop()

	// Add webhooks
	url1 := "http://example.com/webhook1"
	url2 := "http://example.com/webhook2"

	if err := mgr.AddWebhook(url1); err != nil {
		t.Fatalf("Failed to add webhook: %v", err)
	}
	if err := mgr.AddWebhook(url2); err != nil {
		t.Fatalf("Failed to add webhook: %v", err)
	}

	// List webhooks
	webhooks := mgr.ListWebhooks()
	if len(webhooks) != 2 {
		t.Errorf("Expected 2 webhooks, got %d", len(webhooks))
	}

	// Remove webhook
	if err := mgr.RemoveWebhook(url1); err != nil {
		t.Fatalf("Failed to remove webhook: %v", err)
	}

	webhooks = mgr.ListWebhooks()
	if len(webhooks) != 1 {
		t.Errorf("Expected 1 webhook after removal, got %d", len(webhooks))
	}
	if webhooks[0] != url2 {
		t.Errorf("Expected webhook %s, got %s", url2, webhooks[0])
	}
}

func TestMultipleSubscribers(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	hooksDir := t.TempDir()
	mgr := NewSilent(database, hooksDir)
	defer mgr.Stop()

	// Create multiple subscribers
	ch1 := mgr.Subscribe()
	ch2 := mgr.Subscribe()
	ch3 := mgr.Subscribe()

	defer mgr.Unsubscribe(ch1)
	defer mgr.Unsubscribe(ch2)
	defer mgr.Unsubscribe(ch3)

	task := &db.Task{
		ID:     999,
		Title:  "Multi-subscriber Test",
		Status: db.StatusBacklog,
	}

	// Emit event
	mgr.EmitTaskCreated(task)

	// All subscribers should receive the event
	timeout := time.After(1 * time.Second)
	received := 0

	for i := 0; i < 3; i++ {
		select {
		case <-ch1:
			received++
		case <-ch2:
			received++
		case <-ch3:
			received++
		case <-timeout:
			t.Fatalf("Timeout waiting for events, received %d/3", received)
		}
	}

	if received != 3 {
		t.Errorf("Expected 3 events received, got %d", received)
	}
}
