package events

import (
	"os"
	"path/filepath"
	"sync"
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

func TestEmitterWorktreeReadyPassesEnv(t *testing.T) {
	hooksDir := t.TempDir()
	markerFile := filepath.Join(hooksDir, "wt_marker")
	hookScript := filepath.Join(hooksDir, TaskWorktreeReady)

	script := `#!/bin/sh
echo "$WORKTREE_PATH:$WORKTREE_BRANCH:$WORKTREE_PORT" > "` + markerFile + `"
`
	if err := os.WriteFile(hookScript, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	emitter := New(hooksDir)
	task := &db.Task{ID: 7, Title: "Setup", WorktreePath: "/tmp/wt/7-setup", BranchName: "task/7-setup", Port: 4200}
	emitter.EmitTaskWorktreeReady(task)

	content, err := waitForFile(t, markerFile, 5*time.Second)
	if err != nil {
		t.Fatalf("hook didn't run: %v", err)
	}
	if string(content) != "/tmp/wt/7-setup:task/7-setup:4200\n" {
		t.Errorf("unexpected hook output: %q", content)
	}
}

// recordingNotifier captures the events forwarded to it.
type recordingNotifier struct {
	mu     sync.Mutex
	events []string
}

func (r *recordingNotifier) Notify(eventType string, task *db.Task, message string) func() {
	r.mu.Lock()
	r.events = append(r.events, eventType)
	r.mu.Unlock()
	// Return a no-op delivery closure to exercise the emitter's async path.
	return func() {}
}

func (r *recordingNotifier) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func TestEmitterForwardsToNotifier(t *testing.T) {
	// No hooks dir: notifier must still receive every emitted event.
	emitter := New("")
	rec := &recordingNotifier{}
	emitter.SetNotifier(rec)

	emitter.EmitTaskBlocked(&db.Task{ID: 1, Title: "Blocked"}, "needs input")
	emitter.EmitTaskCompleted(&db.Task{ID: 1, Title: "Done"})
	emitter.Wait()

	seen := rec.seen()
	if len(seen) != 2 {
		t.Fatalf("notifier saw %d events, want 2: %v", len(seen), seen)
	}
	if seen[0] != TaskBlocked || seen[1] != TaskCompleted {
		t.Errorf("notifier saw %v, want [%s %s]", seen, TaskBlocked, TaskCompleted)
	}
}

func TestEmitterNoNotifier(t *testing.T) {
	// No notifier set: emitting must not panic.
	emitter := New("")
	emitter.Emit(Event{Type: TaskBlocked, TaskID: 1})
	emitter.Wait()
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
