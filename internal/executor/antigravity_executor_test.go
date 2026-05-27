package executor

import (
	"os"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// newTestExecutor spins up an Executor backed by a throwaway SQLite database.
func newTestExecutor(t *testing.T) *Executor {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "test-antigravity-*.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	return New(database, &config.Config{})
}

func TestAntigravityExecutorName(t *testing.T) {
	exec := newTestExecutor(t)
	a := exec.executorFactory.Get(db.ExecutorAntigravity)
	if a == nil {
		t.Fatal("antigravity executor not registered in factory")
	}
	if got := a.Name(); got != db.ExecutorAntigravity {
		t.Errorf("Name() = %q, want %q", got, db.ExecutorAntigravity)
	}
}

func TestAntigravityExecutorRegistered(t *testing.T) {
	exec := newTestExecutor(t)
	for _, name := range exec.executorFactory.All() {
		if name == db.ExecutorAntigravity {
			return
		}
	}
	t.Errorf("antigravity not found among registered executors: %v", exec.executorFactory.All())
}

func TestAntigravityBinary(t *testing.T) {
	t.Run("defaults to agy", func(t *testing.T) {
		os.Unsetenv("ANTIGRAVITY_BIN")
		if got := antigravityBinary(); got != "agy" {
			t.Errorf("antigravityBinary() = %q, want %q", got, "agy")
		}
	})

	t.Run("honors ANTIGRAVITY_BIN override", func(t *testing.T) {
		t.Setenv("ANTIGRAVITY_BIN", "/custom/path/agy")
		if got := antigravityBinary(); got != "/custom/path/agy" {
			t.Errorf("antigravityBinary() = %q, want %q", got, "/custom/path/agy")
		}
	})
}

func TestBuildAntigravityPromptFlag(t *testing.T) {
	t.Run("defaults to -i", func(t *testing.T) {
		os.Unsetenv("ANTIGRAVITY_PROMPT_FLAG")
		if got := buildAntigravityPromptFlag(); got != "-i " {
			t.Errorf("buildAntigravityPromptFlag() = %q, want %q", got, "-i ")
		}
	})

	t.Run("honors ANTIGRAVITY_PROMPT_FLAG override", func(t *testing.T) {
		t.Setenv("ANTIGRAVITY_PROMPT_FLAG", "--prompt")
		got := buildAntigravityPromptFlag()
		if got != "--prompt " {
			t.Errorf("buildAntigravityPromptFlag() = %q, want %q (with trailing space)", got, "--prompt ")
		}
	})
}

func TestAntigravityCapabilities(t *testing.T) {
	exec := newTestExecutor(t)
	a := exec.executorFactory.Get(db.ExecutorAntigravity)

	if a.SupportsSessionResume() {
		t.Error("SupportsSessionResume() = true, want false (no documented resume flag)")
	}
	if a.SupportsDangerousMode() {
		t.Error("SupportsDangerousMode() = true, want false (auto-approve is in-app)")
	}
	if got := a.FindSessionID("/tmp/whatever"); got != "" {
		t.Errorf("FindSessionID() = %q, want empty", got)
	}
	if a.ResumeDangerous(&db.Task{ID: 1}, "/tmp/whatever") {
		t.Error("ResumeDangerous() = true, want false")
	}
	if a.ResumeSafe(&db.Task{ID: 1}, "/tmp/whatever") {
		t.Error("ResumeSafe() = true, want false")
	}
}

func TestAntigravityBuildCommand(t *testing.T) {
	os.Unsetenv("ANTIGRAVITY_BIN")
	os.Unsetenv("ANTIGRAVITY_PROMPT_FLAG")

	exec := newTestExecutor(t)
	a := exec.executorFactory.Get(db.ExecutorAntigravity)

	task := &db.Task{
		ID:           7,
		Port:         3199,
		WorktreePath: "/tmp/test-worktree/7-task",
	}

	t.Run("without prompt", func(t *testing.T) {
		cmd := a.BuildCommand(task, "", "")
		if !strings.Contains(cmd, "WORKTREE_TASK_ID=7") {
			t.Errorf("BuildCommand() should contain WORKTREE_TASK_ID=7, got %q", cmd)
		}
		if !strings.Contains(cmd, "WORKTREE_PORT=3199") {
			t.Errorf("BuildCommand() should contain WORKTREE_PORT=3199, got %q", cmd)
		}
		if !strings.Contains(cmd, "agy") {
			t.Errorf("BuildCommand() should invoke the agy binary, got %q", cmd)
		}
	})

	t.Run("with prompt", func(t *testing.T) {
		cmd := a.BuildCommand(task, "", "do the thing")
		if !strings.Contains(cmd, "-i ") {
			t.Errorf("BuildCommand() should contain the -i prompt flag, got %q", cmd)
		}
		if !strings.Contains(cmd, "cat ") {
			t.Errorf("BuildCommand() should read the prompt from a temp file, got %q", cmd)
		}
	})

	t.Run("never includes a dangerous-mode flag", func(t *testing.T) {
		dangerousTask := &db.Task{ID: 8, Port: 3200, WorktreePath: "/tmp/wt", DangerousMode: true}
		cmd := a.BuildCommand(dangerousTask, "", "")
		for _, flag := range []string{"--dangerously-allow-run", "--yolo", "--dangerously-skip-permissions"} {
			if strings.Contains(cmd, flag) {
				t.Errorf("BuildCommand() = %q, should not contain %q", cmd, flag)
			}
		}
	})
}
