package executor

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// TestTaskUsesWorktreesPerTaskOverride verifies the executor's worktree
// decision combines the task's WorktreeMode override with the project's
// UseWorktrees setting: inherit follows the project, "worktree" forces a
// fresh worktree even when the project default is off, and "in-place" runs
// in the project directory even when the default is on.
func TestTaskUsesWorktreesPerTaskOverride(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	if err := database.CreateProject(&db.Project{Name: "wt-on", Path: t.TempDir(), UseWorktrees: true}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := database.CreateProject(&db.Project{Name: "wt-off", Path: t.TempDir(), UseWorktrees: false}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	exec := New(database, &config.Config{})

	cases := []struct {
		name string
		task *db.Task
		want bool
	}{
		// No mode set: identical to the previous project-level behavior.
		{"inherit in worktrees-on project", &db.Task{Project: "wt-on"}, true},
		{"inherit in worktrees-off project", &db.Task{Project: "wt-off"}, false},
		{"inherit in unknown project defaults on", &db.Task{Project: "nope"}, true},

		// Per-task overrides win over the project setting.
		{"in-place overrides worktrees-on project", &db.Task{Project: "wt-on", WorktreeMode: db.WorktreeModeInPlace}, false},
		{"worktree overrides worktrees-off project", &db.Task{Project: "wt-off", WorktreeMode: db.WorktreeModeWorktree}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exec.taskUsesWorktrees(c.task); got != c.want {
				t.Errorf("taskUsesWorktrees(%+v) = %v, want %v", c.task, got, c.want)
			}
		})
	}
}
