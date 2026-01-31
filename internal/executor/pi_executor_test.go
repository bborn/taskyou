package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func TestPiExecutor_Name(t *testing.T) {
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
	piExec := exec.GetExecutor("pi")

	if piExec == nil {
		t.Fatal("pi executor not found")
	}

	if piExec.Name() != db.ExecutorPi {
		t.Errorf("expected name %q, got %q", db.ExecutorPi, piExec.Name())
	}
}

func TestPiExecutor_IsAvailable(t *testing.T) {
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
	piExec := exec.GetExecutor("pi")

	if piExec == nil {
		t.Fatal("pi executor not found")
	}

	// Check if pi is in PATH
	_, pathErr := os.Stat("/usr/local/bin/pi")
	piInPath := pathErr == nil

	// IsAvailable should match whether pi is in PATH
	available := piExec.IsAvailable()
	if available != piInPath {
		t.Logf("pi availability: %v (pi in path: %v)", available, piInPath)
	}
}

func TestPiExecutor_SupportsSessionResume(t *testing.T) {
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
	piExec := exec.GetExecutor("pi")

	if piExec == nil {
		t.Fatal("pi executor not found")
	}

	if !piExec.SupportsSessionResume() {
		t.Error("pi executor should support session resume")
	}
}

func TestPiExecutor_DangerousMode(t *testing.T) {
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
	piExec := exec.GetExecutor("pi")

	if piExec == nil {
		t.Fatal("pi executor not found")
	}

	// Pi doesn't support dangerous mode
	if piExec.SupportsDangerousMode() {
		t.Error("pi executor should not support dangerous mode")
	}
}

func TestPiExecutor_BuildCommand(t *testing.T) {
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
	piExec := exec.GetExecutor("pi")

	if piExec == nil {
		t.Fatal("pi executor not found")
	}

	task := &db.Task{
		ID:           123,
		Title:        "Test Task",
		WorktreePath: "/tmp/test-worktree",
		Port:         3100,
	}

	// Test building command without session ID
	cmd := piExec.BuildCommand(task, "", "test prompt")
	if cmd == "" {
		t.Error("expected non-empty command")
	}

	// Should contain task environment variables
	if !containsString(cmd, "WORKTREE_TASK_ID=123") {
		t.Error("command should contain WORKTREE_TASK_ID")
	}
	if !containsString(cmd, "WORKTREE_PORT=3100") {
		t.Error("command should contain WORKTREE_PORT")
	}
	if !containsString(cmd, "pi") {
		t.Error("command should contain 'pi'")
	}

	// Test building command with session ID (resume)
	cmdResume := piExec.BuildCommand(task, "session-123", "")
	if cmdResume == "" {
		t.Error("expected non-empty resume command")
	}

	// Should contain --continue flag for resume
	if !containsString(cmdResume, "--continue") {
		t.Error("resume command should contain --continue flag")
	}
}

func TestFindPiSessionID(t *testing.T) {
	// Create a temporary session directory structure
	tmpDir := t.TempDir()
	home := os.Getenv("HOME")
	
	// Save original HOME and restore after test
	defer os.Setenv("HOME", home)
	os.Setenv("HOME", tmpDir)

	workDir := "/test/work/dir"
	escapedPath := "--" + strings.ReplaceAll(workDir, "/", "-") + "--"
	
	sessionDir := filepath.Join(tmpDir, ".pi", "agent", "sessions", escapedPath)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	// Create some test session files with different timestamps
	sessions := []struct {
		name    string
		age     time.Duration
		content string
	}{
		{"2026-01-01T10-00-00-000Z_old-session.jsonl", 2 * time.Hour, "old"},
		{"2026-01-01T12-00-00-000Z_recent-session.jsonl", 1 * time.Hour, "recent"},
		{"2026-01-01T11-00-00-000Z_middle-session.jsonl", 90 * time.Minute, "middle"},
	}

	for _, s := range sessions {
		path := filepath.Join(sessionDir, s.name)
		if err := os.WriteFile(path, []byte(s.content), 0644); err != nil {
			t.Fatalf("failed to write session file: %v", err)
		}
		// Set modification time
		mtime := time.Now().Add(-s.age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("failed to set mtime: %v", err)
		}
	}

	// Find the most recent session
	sessionID := findPiSessionID(workDir)
	if sessionID == "" {
		t.Fatal("expected to find a session")
	}

	// Should be the most recent one
	expectedPath := filepath.Join(sessionDir, "2026-01-01T12-00-00-000Z_recent-session.jsonl")
	if sessionID != expectedPath {
		t.Errorf("expected most recent session %q, got %q", expectedPath, sessionID)
	}
}

func TestPiSessionExists(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Test non-existent session
	if piSessionExists("") {
		t.Error("empty session path should not exist")
	}

	if piSessionExists("/nonexistent/session.jsonl") {
		t.Error("nonexistent session should return false")
	}

	// Test existing session
	sessionPath := filepath.Join(tmpDir, "test-session.jsonl")
	if err := os.WriteFile(sessionPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test session: %v", err)
	}

	if !piSessionExists(sessionPath) {
		t.Error("existing session should return true")
	}
}

func TestPiExecutor_ResumeDangerous(t *testing.T) {
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
	piExec := exec.GetExecutor("pi")

	if piExec == nil {
		t.Fatal("pi executor not found")
	}

	task := &db.Task{
		ID:           123,
		WorktreePath: "/tmp/test",
	}

	// Pi doesn't support dangerous mode, should return false
	if piExec.ResumeDangerous(task, "/tmp/test") {
		t.Error("pi executor should not support ResumeDangerous")
	}
}

func TestPiExecutor_ResumeSafe(t *testing.T) {
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
	piExec := exec.GetExecutor("pi")

	if piExec == nil {
		t.Fatal("pi executor not found")
	}

	task := &db.Task{
		ID:           123,
		WorktreePath: "/tmp/test",
	}

	// Pi doesn't support dangerous mode, should return false
	if piExec.ResumeSafe(task, "/tmp/test") {
		t.Error("pi executor should not support ResumeSafe")
	}
}

func TestPiExecutor_Execute(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

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
	piExec := exec.GetExecutor("pi")

	if piExec == nil {
		t.Fatal("pi executor not found")
	}

	if !piExec.IsAvailable() {
		t.Skip("pi CLI not available, skipping integration test")
	}

	// This is an integration test that would require tmux and actual Pi execution
	// We'll skip it in normal test runs but it's here as a placeholder
	t.Skip("integration test - requires tmux and pi setup")
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && 
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
		indexString(s, substr) >= 0))
}

func indexString(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
