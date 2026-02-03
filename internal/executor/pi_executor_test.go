package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	// Should contain --session flag with calculated path
	expectedSessionPath := filepath.Join("/tmp", "sessions", "task-123.jsonl")
	if !containsString(cmd, fmt.Sprintf("--session %q", expectedSessionPath)) {
		t.Errorf("command should contain explicit session path, got: %s", cmd)
	}

	// Test building command with session ID (resume)
	sessionPath := "/custom/path/to/session.jsonl"
	cmdResume := piExec.BuildCommand(task, sessionPath, "")
	if cmdResume == "" {
		t.Error("expected non-empty resume command")
	}

	// Should contain --continue flag for resume
	if !containsString(cmdResume, "--continue") {
		t.Error("resume command should contain --continue flag")
	}
	// Should contain --session flag with provided path
	if !containsString(cmdResume, fmt.Sprintf("--session %q", sessionPath)) {
		t.Errorf("resume command should contain provided session path, got: %s", cmdResume)
	}
}

func TestFindPiSessionID(t *testing.T) {
	// Create a temporary directory structure
	tmpDir := t.TempDir()
	
	// Create .task-worktrees structure
	worktreesDir := filepath.Join(tmpDir, ".task-worktrees")
	sessionsDir := filepath.Join(worktreesDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("failed to create sessions dir: %v", err)
	}

	taskID := int64(123)
	slug := "test-slug"
	workDir := filepath.Join(worktreesDir, fmt.Sprintf("%d-%s", taskID, slug))
	
	// 1. Test finding explicit session path
	explicitSessionPath := filepath.Join(sessionsDir, fmt.Sprintf("task-%d.jsonl", taskID))
	if err := os.WriteFile(explicitSessionPath, []byte("explicit"), 0644); err != nil {
		t.Fatalf("failed to write explicit session file: %v", err)
	}
	
	foundSession := findPiSessionID(workDir)
	if foundSession != explicitSessionPath {
		t.Errorf("expected explicit session %q, got %q", explicitSessionPath, foundSession)
	}

	// 2. Test fallback to legacy path (when explicit doesn't exist)
	os.Remove(explicitSessionPath)

	home := os.Getenv("HOME")
	defer os.Setenv("HOME", home)
	os.Setenv("HOME", tmpDir)

	escapedPath := "--" + strings.ReplaceAll(workDir, "/", "-") + "--"
	legacySessionDir := filepath.Join(tmpDir, ".pi", "agent", "sessions", escapedPath)
	if err := os.MkdirAll(legacySessionDir, 0755); err != nil {
		t.Fatalf("failed to create legacy session dir: %v", err)
	}

	legacySessionPath := filepath.Join(legacySessionDir, "legacy-session.jsonl")
	if err := os.WriteFile(legacySessionPath, []byte("legacy"), 0644); err != nil {
		t.Fatalf("failed to write legacy session file: %v", err)
	}

	foundLegacySession := findPiSessionID(workDir)
	if foundLegacySession != legacySessionPath {
		t.Errorf("expected legacy session %q, got %q", legacySessionPath, foundLegacySession)
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
