package executor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// TestBuildCommandIncludesProjectConfigDir is a regression test for "lost executor
// pane". The TUI detail view resumes a task's executor via BuildCommand. If
// BuildCommand omits CLAUDE_CONFIG_DIR for a project with a custom config dir, then
// `claude --resume <id>` runs against the default ~/.claude, can't find the session
// (which lives in the custom dir), and exits immediately — leaving only a placeholder
// pane. BuildCommand must carry the project's config dir like the daemon launch path.
func TestBuildCommandIncludesProjectConfigDir(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if err := database.CreateProject(&db.Project{Name: "ik", Path: "/tmp/ik", ClaudeConfigDir: "~/.claude-ik"}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateProject(&db.Project{Name: "plain", Path: "/tmp/plain"}); err != nil {
		t.Fatal(err)
	}

	exec := New(database, &config.Config{})
	claude := exec.executorFactory.Get(db.ExecutorClaude)

	// Custom-config project: the prefix must be present and precede `claude`.
	custom := claude.BuildCommand(
		&db.Task{ID: 1, Project: "ik", Port: 8080, WorktreePath: "/tmp/ik/.task-worktrees/1-x"},
		"sess-123", "")
	if !strings.Contains(custom, "CLAUDE_CONFIG_DIR=") {
		t.Errorf("custom-config project: BuildCommand must set CLAUDE_CONFIG_DIR; got:\n  %s", custom)
	}
	if ci, pi := strings.Index(custom, "CLAUDE_CONFIG_DIR="), strings.Index(custom, " claude "); ci < 0 || pi < 0 || ci > pi {
		t.Errorf("CLAUDE_CONFIG_DIR must precede `claude`; got:\n  %s", custom)
	}

	// Default-config project: no prefix (setting CLAUDE_CONFIG_DIR to the default
	// breaks MCP discovery, so claudeEnvPrefix returns "").
	def := claude.BuildCommand(
		&db.Task{ID: 2, Project: "plain", Port: 8081, WorktreePath: "/tmp/plain"},
		"sess-456", "")
	if strings.Contains(def, "CLAUDE_CONFIG_DIR=") {
		t.Errorf("default-config project: BuildCommand must NOT set CLAUDE_CONFIG_DIR; got:\n  %s", def)
	}
}
