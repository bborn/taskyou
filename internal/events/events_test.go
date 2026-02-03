package events

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// waitForFile polls for a file to exist with given content, with timeout.
// This is more robust than time.Sleep for async operations.
func waitForFile(t *testing.T, path string, timeout time.Duration) ([]byte, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil {
			return content, nil
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	return nil, lastErr
}

func TestEmitterRunsHook(t *testing.T) {
	hooksDir := t.TempDir()
	markerFile := filepath.Join(hooksDir, "marker")
	hookScript := filepath.Join(hooksDir, TaskCreated)

	script := `#!/bin/sh
echo "$TASK_ID:$TASK_TITLE" > "` + markerFile + `"
`
	if err := os.WriteFile(hookScript, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	emitter := New(hooksDir)
	task := &db.Task{ID: 42, Title: "Test Task"}
	emitter.EmitTaskCreated(task)

	// Poll for async hook execution with timeout (more robust than fixed sleep)
	content, err := waitForFile(t, markerFile, 5*time.Second)
	if err != nil {
		t.Fatalf("hook didn't run: %v", err)
	}
	if string(content) != "42:Test Task\n" {
		t.Errorf("unexpected hook output: %q", content)
	}
}

func TestEmitterPassesEnvironment(t *testing.T) {
	hooksDir := t.TempDir()
	markerFile := filepath.Join(hooksDir, "env_marker")
	hookScript := filepath.Join(hooksDir, TaskCompleted)

	script := `#!/bin/sh
echo "$TASK_STATUS:$TASK_PROJECT" > "` + markerFile + `"
`
	if err := os.WriteFile(hookScript, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	emitter := New(hooksDir)
	task := &db.Task{ID: 1, Title: "Done", Status: "done", Project: "personal"}
	emitter.Emit(Event{Type: TaskCompleted, TaskID: task.ID, Task: task})

	// Poll for async hook execution with timeout (more robust than fixed sleep)
	content, err := waitForFile(t, markerFile, 5*time.Second)
	if err != nil {
		t.Fatalf("hook didn't run: %v", err)
	}
	if string(content) != "done:personal\n" {
		t.Errorf("unexpected hook output: %q", content)
	}
}

func TestEmitterNoHooksDir(t *testing.T) {
	emitter := New("")
	// Should not panic
	emitter.Emit(Event{Type: TaskCreated, TaskID: 1})
}

func TestEmitterMissingHook(t *testing.T) {
	emitter := New(t.TempDir())
	// Should not panic when hook doesn't exist
	emitter.Emit(Event{Type: TaskCreated, TaskID: 1})
}
