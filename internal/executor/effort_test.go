package executor

import (
	"os"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// TestEffortFlag verifies the effortFlag helper produces the expected CLI flag.
func TestEffortFlag(t *testing.T) {
	tests := []struct {
		level string
		want  string
	}{
		{"", ""},
		{db.EffortLow, "--effort low "},
		{db.EffortMedium, "--effort medium "},
		{db.EffortHigh, "--effort high "},
		{db.EffortXHigh, "--effort xhigh "},
		{db.EffortMax, "--effort max "},
	}
	for _, tt := range tests {
		if got := effortFlag(tt.level); got != tt.want {
			t.Errorf("effortFlag(%q) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

// TestBuildCommandEffort verifies BuildCommand includes the --effort flag only
// when a per-task override is set, leaving the default (empty) untouched.
func TestBuildCommandEffort(t *testing.T) {
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

	// No override: command must not contain --effort.
	task := &db.Task{ID: 1, Port: 8080, WorktreePath: "/tmp/test-worktree"}
	if cmd := claude.BuildCommand(task, "", ""); strings.Contains(cmd, "--effort") {
		t.Errorf("BuildCommand() with no effort override should not contain --effort, got %q", cmd)
	}

	// Override set: command must contain --effort <level>.
	task.EffortLevel = db.EffortHigh
	cmd := claude.BuildCommand(task, "", "")
	if !strings.Contains(cmd, "--effort high") {
		t.Errorf("BuildCommand() with high effort should contain %q, got %q", "--effort high", cmd)
	}
}
