package executor

import (
	"os"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// TestModelFlag verifies the modelFlag helper produces the expected CLI flag.
func TestModelFlag(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"", ""},
		{db.ModelOpus, "--model 'opus' "},
		{db.ModelSonnet, "--model 'sonnet' "},
		{db.ModelHaiku, "--model 'haiku' "},
		{"claude-opus-4-8", "--model 'claude-opus-4-8' "},
	}
	for _, tt := range tests {
		if got := modelFlag(tt.model); got != tt.want {
			t.Errorf("modelFlag(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

// TestBuildCommandModel verifies BuildCommand includes the --model flag only
// when a per-task override is set, leaving the default (empty) untouched.
func TestBuildCommandModel(t *testing.T) {
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

	exec := New(database, &config.Config{})
	claude := exec.executorFactory.Get(db.ExecutorClaude)

	// No override: command must not contain --model.
	task := &db.Task{ID: 1, Port: 8080, WorktreePath: "/tmp/test-worktree"}
	if cmd := claude.BuildCommand(task, "", ""); strings.Contains(cmd, "--model") {
		t.Errorf("BuildCommand() with no model override should not contain --model, got %q", cmd)
	}

	// Override set: command must contain --model <name>.
	task.Model = db.ModelOpus
	cmd := claude.BuildCommand(task, "", "")
	if !strings.Contains(cmd, "--model 'opus'") {
		t.Errorf("BuildCommand() with opus model should contain %q, got %q", "--model 'opus'", cmd)
	}
}
