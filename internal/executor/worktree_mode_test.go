package executor

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
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

// gitInTestRepo runs a git command in dir, failing the test on error.
func gitInTestRepo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestSetupWorktreePerTaskOverride exercises setupWorktree end-to-end with the
// per-task overrides: an in-place task in a worktrees-on project runs in the
// project directory itself, and a forced-worktree task in a worktrees-off
// project gets a real git worktree branched from the task's BaseBranch.
func TestSetupWorktreePerTaskOverride(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	newRepo := func(name string) string {
		dir := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitInTestRepo(t, dir, "init", "-b", "main")
		gitInTestRepo(t, dir, "commit", "--allow-empty", "-m", "base")
		gitInTestRepo(t, dir, "branch", "develop")
		// Advance main so develop and main point at different commits.
		gitInTestRepo(t, dir, "commit", "--allow-empty", "-m", "main only")
		return dir
	}
	onDir := newRepo("wt-on-repo")
	offDir := newRepo("wt-off-repo")

	if err := database.CreateProject(&db.Project{Name: "wt-on", Path: onDir, UseWorktrees: true}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := database.CreateProject(&db.Project{Name: "wt-off", Path: offDir, UseWorktrees: false}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	e := New(database, config.New(database))

	t.Run("in-place task in worktrees-on project uses project dir", func(t *testing.T) {
		task := &db.Task{Title: "in place", Status: db.StatusQueued, Project: "wt-on", WorktreeMode: db.WorktreeModeInPlace}
		if err := database.CreateTask(task); err != nil {
			t.Fatal(err)
		}
		workDir, err := e.setupWorktree(task)
		if err != nil {
			t.Fatalf("setupWorktree: %v", err)
		}
		if workDir != onDir {
			t.Errorf("workDir = %q, want project dir %q", workDir, onDir)
		}
		if _, err := os.Stat(filepath.Join(onDir, ".task-worktrees")); !os.IsNotExist(err) {
			t.Error("no .task-worktrees directory should be created for an in-place task")
		}
	})

	t.Run("forced worktree in worktrees-off project branches from base branch", func(t *testing.T) {
		task := &db.Task{
			Title:        "forced worktree",
			Status:       db.StatusQueued,
			Project:      "wt-off",
			WorktreeMode: db.WorktreeModeWorktree,
			BaseBranch:   "develop",
		}
		if err := database.CreateTask(task); err != nil {
			t.Fatal(err)
		}
		workDir, err := e.setupWorktree(task)
		if err != nil {
			t.Fatalf("setupWorktree: %v", err)
		}
		wantPrefix := filepath.Join(offDir, ".task-worktrees") + string(filepath.Separator)
		if !strings.HasPrefix(workDir, wantPrefix) {
			t.Fatalf("workDir = %q, want a worktree under %q", workDir, wantPrefix)
		}

		// The worktree's HEAD must match develop (the base branch), not main.
		head := gitInTestRepo(t, workDir, "rev-parse", "HEAD")
		develop := gitInTestRepo(t, offDir, "rev-parse", "develop")
		main := gitInTestRepo(t, offDir, "rev-parse", "main")
		if head != develop {
			t.Errorf("worktree HEAD = %s, want develop %s", head, develop)
		}
		if head == main {
			t.Error("worktree HEAD should not be main when a base branch is set")
		}
	})
}
