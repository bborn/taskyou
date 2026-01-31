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

// TestExecutorInterfaceImplementation verifies all executors properly implement
// the session and dangerous mode interface methods.
func TestExecutorInterfaceImplementation(t *testing.T) {
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

	tests := []struct {
		name                    string
		executorName            string
		supportsSessionResume   bool
		supportsDangerousMode   bool
		dangerousFlag           string // The flag used for dangerous mode
	}{
		{
			name:                  "Claude executor",
			executorName:          db.ExecutorClaude,
			supportsSessionResume: true,
			supportsDangerousMode: true,
			dangerousFlag:         "--dangerously-skip-permissions",
		},
		{
			name:                  "Codex executor",
			executorName:          db.ExecutorCodex,
			supportsSessionResume: true,
			supportsDangerousMode: true,
			dangerousFlag:         "--dangerously-bypass-approvals-and-sandbox",
		},
		{
			name:                  "Gemini executor",
			executorName:          db.ExecutorGemini,
			supportsSessionResume: true,
			supportsDangerousMode: true,
			dangerousFlag:         "--dangerously-allow-run",
		},
		{
			name:                  "OpenClaw executor",
			executorName:          db.ExecutorOpenClaw,
			supportsSessionResume: true,
			supportsDangerousMode: false, // OpenClaw does not support dangerous mode
			dangerousFlag:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := exec.executorFactory.Get(tt.executorName)
			if executor == nil {
				t.Fatalf("executor %s not found in factory", tt.executorName)
			}

			// Test SupportsSessionResume
			if got := executor.SupportsSessionResume(); got != tt.supportsSessionResume {
				t.Errorf("SupportsSessionResume() = %v, want %v", got, tt.supportsSessionResume)
			}

			// Test SupportsDangerousMode
			if got := executor.SupportsDangerousMode(); got != tt.supportsDangerousMode {
				t.Errorf("SupportsDangerousMode() = %v, want %v", got, tt.supportsDangerousMode)
			}

			// Test Name
			if got := executor.Name(); got != tt.executorName {
				t.Errorf("Name() = %v, want %v", got, tt.executorName)
			}
		})
	}
}

// TestBuildCommandDangerousMode tests that BuildCommand correctly includes
// the dangerous mode flag based on task.DangerousMode field.
func TestBuildCommandDangerousMode(t *testing.T) {
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

	// Clear the env var to ensure we're testing the task field
	os.Unsetenv("WORKTREE_DANGEROUS_MODE")

	tests := []struct {
		name          string
		executorName  string
		dangerousMode bool
		wantFlag      string
	}{
		// Claude tests
		{
			name:          "Claude with dangerous mode enabled",
			executorName:  db.ExecutorClaude,
			dangerousMode: true,
			wantFlag:      "--dangerously-skip-permissions",
		},
		{
			name:          "Claude with dangerous mode disabled",
			executorName:  db.ExecutorClaude,
			dangerousMode: false,
			wantFlag:      "",
		},
		// Codex tests
		{
			name:          "Codex with dangerous mode enabled",
			executorName:  db.ExecutorCodex,
			dangerousMode: true,
			wantFlag:      "--dangerously-bypass-approvals-and-sandbox",
		},
		{
			name:          "Codex with dangerous mode disabled",
			executorName:  db.ExecutorCodex,
			dangerousMode: false,
			wantFlag:      "",
		},
		// Gemini tests
		{
			name:          "Gemini with dangerous mode enabled",
			executorName:  db.ExecutorGemini,
			dangerousMode: true,
			wantFlag:      "--dangerously-allow-run",
		},
		{
			name:          "Gemini with dangerous mode disabled",
			executorName:  db.ExecutorGemini,
			dangerousMode: false,
			wantFlag:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &db.Task{
				ID:           1,
				DangerousMode: tt.dangerousMode,
				Port:         8080,
				WorktreePath: "/tmp/test-worktree",
			}

			executor := exec.executorFactory.Get(tt.executorName)
			cmd := executor.BuildCommand(task, "", "")

			if tt.wantFlag != "" {
				if !strings.Contains(cmd, tt.wantFlag) {
					t.Errorf("BuildCommand() = %q, should contain %q", cmd, tt.wantFlag)
				}
			} else {
				// Should NOT contain any dangerous flag
				dangerousFlags := []string{
					"--dangerously-skip-permissions",
					"--dangerously-bypass-approvals-and-sandbox",
					"--dangerously-allow-run",
				}
				for _, flag := range dangerousFlags {
					if strings.Contains(cmd, flag) {
						t.Errorf("BuildCommand() = %q, should NOT contain %q", cmd, flag)
					}
				}
			}
		})
	}
}

// TestBuildCommandDangerousModeEnvVar tests that WORKTREE_DANGEROUS_MODE env var
// also enables dangerous mode even when task.DangerousMode is false.
func TestBuildCommandDangerousModeEnvVar(t *testing.T) {
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

	// Set the env var
	os.Setenv("WORKTREE_DANGEROUS_MODE", "1")
	defer os.Unsetenv("WORKTREE_DANGEROUS_MODE")

	task := &db.Task{
		ID:           1,
		DangerousMode: false, // Task field is false
		Port:         8080,
		WorktreePath: "/tmp/test-worktree",
	}

	tests := []struct {
		executorName string
		wantFlag     string
	}{
		{db.ExecutorClaude, "--dangerously-skip-permissions"},
		{db.ExecutorCodex, "--dangerously-bypass-approvals-and-sandbox"},
		{db.ExecutorGemini, "--dangerously-allow-run"},
	}

	for _, tt := range tests {
		t.Run(tt.executorName, func(t *testing.T) {
			executor := exec.executorFactory.Get(tt.executorName)
			cmd := executor.BuildCommand(task, "", "")

			if !strings.Contains(cmd, tt.wantFlag) {
				t.Errorf("BuildCommand() with WORKTREE_DANGEROUS_MODE=1 should contain %q, got %q", tt.wantFlag, cmd)
			}
		})
	}
}

// TestBuildGeminiDangerousFlag tests the Gemini dangerous flag builder
// including the GEMINI_DANGEROUS_ARGS customization.
func TestBuildGeminiDangerousFlag(t *testing.T) {
	// Clear env vars before test
	os.Unsetenv("WORKTREE_DANGEROUS_MODE")
	os.Unsetenv("GEMINI_DANGEROUS_ARGS")

	t.Run("returns empty when not enabled", func(t *testing.T) {
		got := buildGeminiDangerousFlag(false)
		if got != "" {
			t.Errorf("buildGeminiDangerousFlag(false) = %q, want empty", got)
		}
	})

	t.Run("returns default flag when enabled", func(t *testing.T) {
		got := buildGeminiDangerousFlag(true)
		want := "--dangerously-allow-run "
		if got != want {
			t.Errorf("buildGeminiDangerousFlag(true) = %q, want %q", got, want)
		}
	})

	t.Run("respects WORKTREE_DANGEROUS_MODE env var", func(t *testing.T) {
		os.Setenv("WORKTREE_DANGEROUS_MODE", "1")
		defer os.Unsetenv("WORKTREE_DANGEROUS_MODE")

		got := buildGeminiDangerousFlag(false) // false but env var is set
		want := "--dangerously-allow-run "
		if got != want {
			t.Errorf("buildGeminiDangerousFlag(false) with WORKTREE_DANGEROUS_MODE=1 = %q, want %q", got, want)
		}
	})

	t.Run("respects GEMINI_DANGEROUS_ARGS customization", func(t *testing.T) {
		os.Setenv("GEMINI_DANGEROUS_ARGS", "--custom-flag --another-flag")
		defer os.Unsetenv("GEMINI_DANGEROUS_ARGS")

		got := buildGeminiDangerousFlag(true)
		want := "--custom-flag --another-flag "
		if got != want {
			t.Errorf("buildGeminiDangerousFlag(true) with custom args = %q, want %q", got, want)
		}
	})

	t.Run("adds trailing space if not present", func(t *testing.T) {
		os.Setenv("GEMINI_DANGEROUS_ARGS", "--no-trailing-space")
		defer os.Unsetenv("GEMINI_DANGEROUS_ARGS")

		got := buildGeminiDangerousFlag(true)
		if !strings.HasSuffix(got, " ") {
			t.Errorf("buildGeminiDangerousFlag should add trailing space, got %q", got)
		}
	})
}

// TestFindCodexSessionID tests the Codex session discovery function.
func TestFindCodexSessionID(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Could not get home directory")
	}

	// Create a unique test work directory
	testWorkDir := "/tmp/test-codex-session-" + time.Now().Format("20060102150405")

	t.Run("returns empty for non-existent sessions directory", func(t *testing.T) {
		result := findCodexSessionID(testWorkDir)
		if result != "" {
			t.Errorf("expected empty string for non-existent directory, got %q", result)
		}
	})

	t.Run("finds session matching workDir", func(t *testing.T) {
		// Create the sessions directory
		sessionsDir := filepath.Join(home, ".codex", "sessions")
		if err := os.MkdirAll(sessionsDir, 0755); err != nil {
			t.Fatalf("Could not create sessions directory: %v", err)
		}
		defer os.RemoveAll(filepath.Join(home, ".codex"))

		// Create a session file that contains the workDir
		sessionFile := filepath.Join(sessionsDir, "test-session-12345.json")
		sessionContent := `{"workDir": "` + testWorkDir + `", "data": "test"}`
		if err := os.WriteFile(sessionFile, []byte(sessionContent), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		result := findCodexSessionID(testWorkDir)
		if result != "test-session-12345" {
			t.Errorf("expected 'test-session-12345', got %q", result)
		}
	})

	t.Run("returns most recent matching session", func(t *testing.T) {
		sessionsDir := filepath.Join(home, ".codex", "sessions")
		if err := os.MkdirAll(sessionsDir, 0755); err != nil {
			t.Fatalf("Could not create sessions directory: %v", err)
		}
		defer os.RemoveAll(filepath.Join(home, ".codex"))

		// Create older session
		olderSession := filepath.Join(sessionsDir, "older-session.json")
		if err := os.WriteFile(olderSession, []byte(`{"workDir": "`+testWorkDir+`"}`), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		// Create newer session
		newerSession := filepath.Join(sessionsDir, "newer-session.json")
		if err := os.WriteFile(newerSession, []byte(`{"workDir": "`+testWorkDir+`"}`), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		result := findCodexSessionID(testWorkDir)
		if result != "newer-session" {
			t.Errorf("expected 'newer-session' (most recent), got %q", result)
		}
	})

	t.Run("ignores sessions for other workDirs", func(t *testing.T) {
		sessionsDir := filepath.Join(home, ".codex", "sessions")
		if err := os.MkdirAll(sessionsDir, 0755); err != nil {
			t.Fatalf("Could not create sessions directory: %v", err)
		}
		defer os.RemoveAll(filepath.Join(home, ".codex"))

		// Create session for different workDir
		otherSession := filepath.Join(sessionsDir, "other-session.json")
		if err := os.WriteFile(otherSession, []byte(`{"workDir": "/other/path"}`), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		result := findCodexSessionID(testWorkDir)
		if result != "" {
			t.Errorf("expected empty string for non-matching workDir, got %q", result)
		}
	})
}

// TestFindGeminiSessionID tests the Gemini session discovery function.
func TestFindGeminiSessionID(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Could not get home directory")
	}

	// Create a unique test work directory
	testWorkDir := "/tmp/test-gemini-session-" + time.Now().Format("20060102150405")

	t.Run("returns empty for non-existent tmp directory", func(t *testing.T) {
		result := findGeminiSessionID(testWorkDir)
		if result != "" {
			t.Errorf("expected empty string for non-existent directory, got %q", result)
		}
	})

	t.Run("finds session in chats subdirectory", func(t *testing.T) {
		// Create the Gemini tmp/chats directory structure
		geminiChatsDir := filepath.Join(home, ".gemini", "tmp", "project-hash", "chats")
		if err := os.MkdirAll(geminiChatsDir, 0755); err != nil {
			t.Fatalf("Could not create chats directory: %v", err)
		}
		defer os.RemoveAll(filepath.Join(home, ".gemini", "tmp"))

		// Create a session file that contains the workDir
		sessionFile := filepath.Join(geminiChatsDir, "gemini-session-abc.json")
		sessionContent := `{"workDir": "` + testWorkDir + `", "data": "test"}`
		if err := os.WriteFile(sessionFile, []byte(sessionContent), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		result := findGeminiSessionID(testWorkDir)
		if result != "gemini-session-abc" {
			t.Errorf("expected 'gemini-session-abc', got %q", result)
		}
	})

	t.Run("ignores files not in chats directory", func(t *testing.T) {
		// Create the Gemini tmp directory with a file NOT in chats
		geminiTmpDir := filepath.Join(home, ".gemini", "tmp", "project-hash")
		if err := os.MkdirAll(geminiTmpDir, 0755); err != nil {
			t.Fatalf("Could not create tmp directory: %v", err)
		}
		defer os.RemoveAll(filepath.Join(home, ".gemini", "tmp"))

		// Create a session file NOT in chats subdirectory
		sessionFile := filepath.Join(geminiTmpDir, "not-in-chats.json")
		sessionContent := `{"workDir": "` + testWorkDir + `", "data": "test"}`
		if err := os.WriteFile(sessionFile, []byte(sessionContent), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		result := findGeminiSessionID(testWorkDir)
		if result != "" {
			t.Errorf("expected empty string for file not in chats directory, got %q", result)
		}
	})

	t.Run("returns most recent matching session", func(t *testing.T) {
		geminiChatsDir := filepath.Join(home, ".gemini", "tmp", "project-hash2", "chats")
		if err := os.MkdirAll(geminiChatsDir, 0755); err != nil {
			t.Fatalf("Could not create chats directory: %v", err)
		}
		defer os.RemoveAll(filepath.Join(home, ".gemini", "tmp"))

		// Create older session
		olderSession := filepath.Join(geminiChatsDir, "older-gemini.json")
		if err := os.WriteFile(olderSession, []byte(`{"workDir": "`+testWorkDir+`"}`), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		// Create newer session
		newerSession := filepath.Join(geminiChatsDir, "newer-gemini.json")
		if err := os.WriteFile(newerSession, []byte(`{"workDir": "`+testWorkDir+`"}`), 0644); err != nil {
			t.Fatalf("Could not create session file: %v", err)
		}

		result := findGeminiSessionID(testWorkDir)
		if result != "newer-gemini" {
			t.Errorf("expected 'newer-gemini' (most recent), got %q", result)
		}
	})
}

// TestBuildCommandWithSessionResume tests that BuildCommand correctly
// includes the --resume flag when a session ID is provided.
func TestBuildCommandWithSessionResume(t *testing.T) {
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

	// Clear env vars
	os.Unsetenv("WORKTREE_DANGEROUS_MODE")

	task := &db.Task{
		ID:           1,
		DangerousMode: false,
		Port:         8080,
		WorktreePath: "/tmp/test-worktree",
	}

	tests := []struct {
		name         string
		executorName string
		sessionID    string
		wantContains string
	}{
		{
			name:         "Claude with session ID",
			executorName: db.ExecutorClaude,
			sessionID:    "abc123-session-id",
			wantContains: "--resume abc123-session-id",
		},
		{
			name:         "Claude without session ID",
			executorName: db.ExecutorClaude,
			sessionID:    "",
			wantContains: "",
		},
		{
			name:         "Codex with session ID",
			executorName: db.ExecutorCodex,
			sessionID:    "codex-session-456",
			wantContains: "--resume codex-session-456",
		},
		{
			name:         "Gemini with session ID",
			executorName: db.ExecutorGemini,
			sessionID:    "gemini-session-789",
			wantContains: "--resume gemini-session-789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := exec.executorFactory.Get(tt.executorName)
			cmd := executor.BuildCommand(task, tt.sessionID, "")

			if tt.wantContains != "" {
				if !strings.Contains(cmd, tt.wantContains) {
					t.Errorf("BuildCommand() = %q, should contain %q", cmd, tt.wantContains)
				}
			} else {
				if strings.Contains(cmd, "--resume") {
					t.Errorf("BuildCommand() = %q, should NOT contain --resume when no session ID", cmd)
				}
			}
		})
	}
}

// TestBuildCommandWithDangerousAndResume tests that both dangerous mode flag
// and resume flag are included when both are applicable.
func TestBuildCommandWithDangerousAndResume(t *testing.T) {
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

	// Clear env vars
	os.Unsetenv("WORKTREE_DANGEROUS_MODE")

	task := &db.Task{
		ID:           1,
		DangerousMode: true,
		Port:         8080,
		WorktreePath: "/tmp/test-worktree",
	}

	tests := []struct {
		executorName string
		dangerousArg string
	}{
		{db.ExecutorClaude, "--dangerously-skip-permissions"},
		{db.ExecutorCodex, "--dangerously-bypass-approvals-and-sandbox"},
		{db.ExecutorGemini, "--dangerously-allow-run"},
	}

	sessionID := "test-session-combined"

	for _, tt := range tests {
		t.Run(tt.executorName, func(t *testing.T) {
			executor := exec.executorFactory.Get(tt.executorName)
			cmd := executor.BuildCommand(task, sessionID, "")

			// Should contain both flags
			if !strings.Contains(cmd, tt.dangerousArg) {
				t.Errorf("BuildCommand() = %q, should contain dangerous flag %q", cmd, tt.dangerousArg)
			}
			if !strings.Contains(cmd, "--resume "+sessionID) {
				t.Errorf("BuildCommand() = %q, should contain --resume %s", cmd, sessionID)
			}
		})
	}
}

// TestOpenClawDangerousModeNotSupported tests that OpenClaw correctly reports
// it doesn't support dangerous mode and ResumeDangerous returns false.
func TestOpenClawDangerousModeNotSupported(t *testing.T) {
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

	executor := exec.executorFactory.Get(db.ExecutorOpenClaw)

	t.Run("SupportsDangerousMode returns false", func(t *testing.T) {
		if executor.SupportsDangerousMode() {
			t.Error("OpenClaw should not support dangerous mode")
		}
	})

	t.Run("BuildCommand does not include dangerous flags", func(t *testing.T) {
		task := &db.Task{
			ID:           1,
			DangerousMode: true, // Even when enabled on task
			Port:         8080,
			WorktreePath: "/tmp/test-worktree",
		}

		cmd := executor.BuildCommand(task, "", "")

		// Should NOT contain any dangerous flag
		dangerousFlags := []string{
			"--dangerously-skip-permissions",
			"--dangerously-bypass-approvals-and-sandbox",
			"--dangerously-allow-run",
		}
		for _, flag := range dangerousFlags {
			if strings.Contains(cmd, flag) {
				t.Errorf("OpenClaw BuildCommand() = %q, should NOT contain %q", cmd, flag)
			}
		}
	})

	t.Run("ResumeDangerous returns false", func(t *testing.T) {
		task := &db.Task{
			ID:      1,
			Project: "test",
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatal(err)
		}

		result := executor.ResumeDangerous(task, "/tmp/test-worktree")
		if result {
			t.Error("OpenClaw ResumeDangerous should return false")
		}
	})
}

// TestBuildCommandIncludesEnvironmentVariables tests that BuildCommand
// includes the necessary WORKTREE_* environment variables.
func TestBuildCommandIncludesEnvironmentVariables(t *testing.T) {
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

	task := &db.Task{
		ID:           42,
		Port:         9000,
		WorktreePath: "/home/user/projects/myapp/.task-worktrees/42-fix-bug",
	}

	executors := []string{db.ExecutorClaude, db.ExecutorCodex, db.ExecutorGemini, db.ExecutorOpenClaw}

	for _, name := range executors {
		t.Run(name, func(t *testing.T) {
			executor := exec.executorFactory.Get(name)
			cmd := executor.BuildCommand(task, "", "")

			// Check for task ID
			if !strings.Contains(cmd, "WORKTREE_TASK_ID=42") {
				t.Errorf("BuildCommand() should contain WORKTREE_TASK_ID=42, got %q", cmd)
			}

			// Check for port
			if !strings.Contains(cmd, "WORKTREE_PORT=9000") {
				t.Errorf("BuildCommand() should contain WORKTREE_PORT=9000, got %q", cmd)
			}

			// Check for worktree path
			if !strings.Contains(cmd, "WORKTREE_PATH=") {
				t.Errorf("BuildCommand() should contain WORKTREE_PATH=, got %q", cmd)
			}
		})
	}
}
