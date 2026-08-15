package executor

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func launchWorkdirExecutor(t *testing.T, projectDir string, usesWorktrees bool) *Executor {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "p", Path: projectDir, UseWorktrees: usesWorktrees}); err != nil {
		t.Fatal(err)
	}
	return New(database, config.New(database))
}

// The guard this file exists for. EnsureTaskWindow is reachable from the TUI, the
// GUI and the HTTP API, and it used to resolve its working directory through
// taskWorkdir, which falls back to the project directory and then to $HOME. A task
// the daemon had not yet provisioned could therefore be started BY HAND straight
// into the primary clone — which is exactly how a pipeline verify step ran 42
// minutes in the main repo and, having no worktree recorded, never parked for
// merge review. Starting must fail instead.
func TestLaunchWorkdirRefusesProjectDirForWorktreeProject(t *testing.T) {
	projectDir := t.TempDir()
	e := launchWorkdirExecutor(t, projectDir, true)

	task := &db.Task{Title: "verify step", Project: "p"} // no WorktreePath
	if err := e.db.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	got, err := e.launchWorkdir(task)
	if !errors.Is(err, ErrNoWorktree) {
		t.Fatalf("want ErrNoWorktree for an unprovisioned task, got (%q, %v)", got, err)
	}
	if got == projectDir {
		t.Fatal("returned the primary clone as a place to start an agent")
	}

	// And the old fallback really would have handed back the main repo, which is
	// what makes the guard necessary rather than cosmetic.
	if fallback := e.taskWorkdir(task); fallback != projectDir {
		t.Fatalf("taskWorkdir = %q, expected the project dir %q (the unsafe fallback this guards)", fallback, projectDir)
	}
}

// A project that opts out of worktrees shares the project directory by design;
// that is its normal working directory, not a fallback.
func TestLaunchWorkdirAllowsProjectDirWhenWorktreesDisabled(t *testing.T) {
	projectDir := t.TempDir()
	e := launchWorkdirExecutor(t, projectDir, false)

	task := &db.Task{Title: "ordinary task", Project: "p"}
	if err := e.db.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	got, err := e.launchWorkdir(task)
	if err != nil {
		t.Fatalf("non-worktree project should start in its project dir: %v", err)
	}
	if got != projectDir {
		t.Fatalf("launchWorkdir = %q, want %q", got, projectDir)
	}
}

// A provisioned task starts in its worktree, worktree-project or not.
func TestLaunchWorkdirUsesWorktreeWhenPresent(t *testing.T) {
	projectDir := t.TempDir()
	worktree := t.TempDir()
	e := launchWorkdirExecutor(t, projectDir, true)

	task := &db.Task{Title: "provisioned", Project: "p", WorktreePath: worktree}
	if err := e.db.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	got, err := e.launchWorkdir(task)
	if err != nil {
		t.Fatalf("provisioned task should launch: %v", err)
	}
	if got != worktree {
		t.Fatalf("launchWorkdir = %q, want the worktree %q", got, worktree)
	}
}
