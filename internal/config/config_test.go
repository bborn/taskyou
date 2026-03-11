package config

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestProjectUsesWorktrees(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	cfg := New(database)

	t.Run("empty project name defaults to true", func(t *testing.T) {
		if !cfg.ProjectUsesWorktrees("") {
			t.Error("empty project name should default to true")
		}
	})

	t.Run("unknown project defaults to true", func(t *testing.T) {
		if !cfg.ProjectUsesWorktrees("nonexistent-project") {
			t.Error("unknown project should default to true")
		}
	})

	t.Run("worktree-enabled project returns true", func(t *testing.T) {
		if err := database.CreateProject(&db.Project{
			Name: "git-proj", Path: filepath.Join(tmpDir, "git"), UseWorktrees: true,
		}); err != nil {
			t.Fatalf("failed to create project: %v", err)
		}
		if !cfg.ProjectUsesWorktrees("git-proj") {
			t.Error("worktree-enabled project should return true")
		}
	})

	t.Run("worktree-disabled project returns false", func(t *testing.T) {
		if err := database.CreateProject(&db.Project{
			Name: "no-git-proj", Path: filepath.Join(tmpDir, "nogit"), UseWorktrees: false,
		}); err != nil {
			t.Fatalf("failed to create project: %v", err)
		}
		if cfg.ProjectUsesWorktrees("no-git-proj") {
			t.Error("worktree-disabled project should return false")
		}
	})
}
