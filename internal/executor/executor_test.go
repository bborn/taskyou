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

	// Create the test project first
	if err := database.CreateProject(&db.Project{Name: "test", Path: "/tmp/test"}); err != nil {
		t.Fatal(err)
	}

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

func TestAttachmentsInPrompt(t *testing.T) {
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

	// Create the test project first
	if err := database.CreateProject(&db.Project{Name: "test", Path: "/tmp/test"}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	exec := New(database, cfg)

	// Create a test task
	task := &db.Task{
		Title:   "Test task with attachments",
		Status:  db.StatusQueued,
		Type:    db.TypeCode,
		Project: "test",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	// Add attachments
	_, err = database.AddAttachment(task.ID, "notes.txt", "text/plain", []byte("test notes content"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.AddAttachment(task.ID, "data.json", "application/json", []byte(`{"key":"value"}`))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("prepareAttachments creates temp files", func(t *testing.T) {
		paths, cleanup := exec.prepareAttachments(task.ID)
		defer cleanup()

		if len(paths) != 2 {
			t.Errorf("expected 2 attachment paths, got %d", len(paths))
		}

		// Verify files exist and have correct content
		for _, path := range paths {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Errorf("attachment file does not exist: %s", path)
			}
		}
	})

	t.Run("getAttachmentsSection uses Read tool", func(t *testing.T) {
		paths := []string{"/tmp/test/notes.txt", "/tmp/test/data.json"}
		section := exec.getAttachmentsSection(task.ID, paths)

		if !strings.Contains(section, "## Attachments") {
			t.Error("section should contain Attachments header")
		}
		if !strings.Contains(section, "Read tool") {
			t.Error("section should mention Read tool (not View tool)")
		}
		if strings.Contains(section, "View tool") {
			t.Error("section should NOT mention View tool")
		}
		if !strings.Contains(section, "/tmp/test/notes.txt") {
			t.Error("section should contain file path")
		}
	})

	t.Run("buildPrompt includes attachments section", func(t *testing.T) {
		paths, cleanup := exec.prepareAttachments(task.ID)
		defer cleanup()

		prompt := exec.buildPrompt(task, paths)

		if !strings.Contains(prompt, "## Attachments") {
			t.Error("prompt should include attachments section")
		}
		if !strings.Contains(prompt, "Read tool") {
			t.Error("prompt should mention Read tool")
		}
	})
}

func TestFindClaudeSessionID(t *testing.T) {
	// Create a temporary directory structure mimicking Claude's session storage
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Could not get home directory")
	}

	// Create a unique test directory
	testWorkDir := "/tmp/test-claude-session-" + time.Now().Format("20060102150405")
	// Match Claude's escaping: replace / with -, replace . with -, keep leading -
	escapedPath := strings.ReplaceAll(testWorkDir, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
	projectDir := home + "/.claude/projects/" + escapedPath

	// Create the project directory
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Could not create project directory: %v", err)
	}
	defer os.RemoveAll(projectDir)

	t.Run("no session files", func(t *testing.T) {
		result := FindClaudeSessionID(testWorkDir)
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("finds most recent session", func(t *testing.T) {
		// Create some session files with different timestamps
		session1 := projectDir + "/abc12345-1234-5678-abcd-123456789abc.jsonl"
		session2 := projectDir + "/def67890-1234-5678-abcd-123456789def.jsonl"

		// Create first session (older)
		if err := os.WriteFile(session1, []byte(`{"test":"data"}`), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}
		time.Sleep(10 * time.Millisecond)

		// Create second session (newer)
		if err := os.WriteFile(session2, []byte(`{"test":"data2"}`), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		result := FindClaudeSessionID(testWorkDir)
		if result != "def67890-1234-5678-abcd-123456789def" {
			t.Errorf("expected most recent session, got %q", result)
		}

		// Clean up
		os.Remove(session1)
		os.Remove(session2)
	})

	t.Run("ignores agent files", func(t *testing.T) {
		// Create an agent file and a regular session file
		agentFile := projectDir + "/agent-abc12345-1234-5678-abcd-123456789abc.jsonl"
		sessionFile := projectDir + "/xyz99999-1234-5678-abcd-123456789xyz.jsonl"

		if err := os.WriteFile(agentFile, []byte(`{"agent":"data"}`), 0644); err != nil {
			t.Fatalf("Could not create agent file: %v", err)
		}
		if err := os.WriteFile(sessionFile, []byte(`{"session":"data"}`), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		result := FindClaudeSessionID(testWorkDir)
		if result != "xyz99999-1234-5678-abcd-123456789xyz" {
			t.Errorf("expected regular session, got %q (should ignore agent files)", result)
		}

		// Clean up
		os.Remove(agentFile)
		os.Remove(sessionFile)
	})
}

func TestRenameClaudeSession(t *testing.T) {
	t.Run("returns nil for non-existent workdir", func(t *testing.T) {
		// Test with a workdir that doesn't have any Claude sessions
		err := RenameClaudeSession("/tmp/non-existent-workdir-12345", "New Name")
		if err != nil {
			t.Errorf("expected nil error for non-existent session, got: %v", err)
		}
	})

	// Note: We can't easily test the actual rename without mocking the claude CLI
	// The function will return an error if claude isn't installed or if the session
	// doesn't exist, but we handle that gracefully
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

	// Create the test project first
	if err := database.CreateProject(&db.Project{Name: "test", Path: "/tmp/test"}); err != nil {
		t.Fatal(err)
	}

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
		prompt := exec.buildPrompt(task, nil)
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

		prompt := exec.buildPrompt(task, nil)

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

		prompt := exec.buildPrompt(task, nil)

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

func TestRunWorktreeInitScriptStreaming(t *testing.T) {
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

	// Create the test project
	if err := database.CreateProject(&db.Project{Name: "test", Path: "/tmp/test"}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	exec := New(database, cfg)

	// Create a test task
	task := &db.Task{
		Title:   "Test streaming task",
		Status:  db.StatusProcessing,
		Project: "test",
		Port:    3100,
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	// Create a temp worktree directory
	worktreeDir, err := os.MkdirTemp("", "test-worktree-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(worktreeDir)

	// Create a temp project directory with a test init script
	projectDir, err := os.MkdirTemp("", "test-project-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(projectDir)

	// Create bin directory
	binDir := projectDir + "/bin"
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Subscribe to task logs to capture streaming output
	logCh := exec.Subscribe(task.ID)
	defer exec.Unsubscribe(task.ID, logCh)

	t.Run("streams output line by line", func(t *testing.T) {
		// Create a test script that outputs multiple lines with delays
		scriptContent := `#!/bin/bash
echo "Line 1: Starting setup"
echo "Line 2: Installing dependencies"
echo "Line 3: Completed"
`
		scriptPath := binDir + "/worktree-setup"
		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
			t.Fatal(err)
		}

		// Collect logs in the background
		var collectedLogs []string
		done := make(chan struct{})
		go func() {
			defer close(done)
			timeout := time.After(5 * time.Second)
			expectedCount := 5 // "Running worktree init script", 3 lines, "completed successfully"
			for {
				select {
				case log := <-logCh:
					collectedLogs = append(collectedLogs, log.Content)
					if len(collectedLogs) >= expectedCount {
						return
					}
				case <-timeout:
					return
				}
			}
		}()

		// Run the script
		exec.runWorktreeInitScript(projectDir, worktreeDir, task)

		// Wait for log collection to complete
		<-done

		// Verify the logs were streamed
		if len(collectedLogs) < 4 {
			t.Errorf("expected at least 4 log entries, got %d: %v", len(collectedLogs), collectedLogs)
		}

		// Check that each line was logged individually with [init] prefix
		var foundLine1, foundLine2, foundLine3 bool
		for _, log := range collectedLogs {
			if strings.Contains(log, "[init] Line 1:") {
				foundLine1 = true
			}
			if strings.Contains(log, "[init] Line 2:") {
				foundLine2 = true
			}
			if strings.Contains(log, "[init] Line 3:") {
				foundLine3 = true
			}
		}

		if !foundLine1 || !foundLine2 || !foundLine3 {
			t.Errorf("expected all three lines to be logged individually, got logs: %v", collectedLogs)
		}
	})

	t.Run("handles stderr output", func(t *testing.T) {
		// Clear previous logs
		collectedLogs := []string{}

		// Create a script that outputs to stderr
		scriptContent := `#!/bin/bash
echo "stdout message"
echo "stderr message" >&2
`
		scriptPath := binDir + "/worktree-setup"
		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
			t.Fatal(err)
		}

		// Collect logs in the background
		done := make(chan struct{})
		go func() {
			defer close(done)
			timeout := time.After(5 * time.Second)
			expectedCount := 4 // "Running worktree init script", stdout, stderr, "completed successfully"
			for {
				select {
				case log := <-logCh:
					collectedLogs = append(collectedLogs, log.Content)
					if len(collectedLogs) >= expectedCount {
						return
					}
				case <-timeout:
					return
				}
			}
		}()

		// Run the script
		exec.runWorktreeInitScript(projectDir, worktreeDir, task)

		// Wait for log collection to complete
		<-done

		// Verify both stdout and stderr were captured
		var foundStdout, foundStderr bool
		for _, log := range collectedLogs {
			if strings.Contains(log, "stdout message") {
				foundStdout = true
			}
			if strings.Contains(log, "stderr message") {
				foundStderr = true
			}
		}

		if !foundStdout {
			t.Error("expected stdout to be captured")
		}
		if !foundStderr {
			t.Error("expected stderr to be captured")
		}
	})

	t.Run("handles script failure", func(t *testing.T) {
		// Clear previous logs
		collectedLogs := []string{}

		// Create a script that fails
		scriptContent := `#!/bin/bash
echo "Starting..."
exit 1
`
		scriptPath := binDir + "/worktree-setup"
		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
			t.Fatal(err)
		}

		// Collect logs in the background
		done := make(chan struct{})
		go func() {
			defer close(done)
			timeout := time.After(5 * time.Second)
			expectedCount := 3 // "Running worktree init script", "Starting...", "Warning: ... failed"
			for {
				select {
				case log := <-logCh:
					collectedLogs = append(collectedLogs, log.Content)
					if len(collectedLogs) >= expectedCount {
						return
					}
				case <-timeout:
					return
				}
			}
		}()

		// Run the script
		exec.runWorktreeInitScript(projectDir, worktreeDir, task)

		// Wait for log collection to complete
		<-done

		// Verify failure was logged
		var foundFailure bool
		for _, log := range collectedLogs {
			if strings.Contains(log, "Warning: worktree init script failed") {
				foundFailure = true
			}
		}

		if !foundFailure {
			t.Errorf("expected failure message, got logs: %v", collectedLogs)
		}
	})
}
