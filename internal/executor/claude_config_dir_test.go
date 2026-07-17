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

// TestBuildCommandHonorsPerTaskConfigDir verifies a task's ClaudeConfigDir
// override wins over the project's configured dir — so a single workflow step
// can route through a different Claude config (e.g. an ollama-backed one)
// without changing the project. This is the executor-side half of the
// pipeline step config_dir feature; the propagation half is tested in
// internal/pipeline.
func TestBuildCommandHonorsPerTaskConfigDir(t *testing.T) {
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

	// Per-task override on an otherwise default-config project: the ollama dir
	// must be set (the project has none, so without the override there'd be no prefix).
	overriddenOnPlain := claude.BuildCommand(
		&db.Task{ID: 1, Project: "plain", Port: 8080, WorktreePath: "/tmp/plain", ClaudeConfigDir: "~/.claude-ollama"},
		"", "")
	if !strings.Contains(overriddenOnPlain, "CLAUDE_CONFIG_DIR=") || !strings.Contains(overriddenOnPlain, ".claude-ollama") {
		t.Errorf("per-task override on plain project: expected CLAUDE_CONFIG_DIR=.claude-ollama; got:\n  %s", overriddenOnPlain)
	}

	// Per-task override on a project that ALSO has a custom config dir: the task's
	// override must win over the project's ~/.claude-ik.
	overriddenOnCustom := claude.BuildCommand(
		&db.Task{ID: 2, Project: "ik", Port: 8081, WorktreePath: "/tmp/ik", ClaudeConfigDir: "~/.claude-ollama"},
		"", "")
	if !strings.Contains(overriddenOnCustom, ".claude-ollama") {
		t.Errorf("per-task override must win over project config; expected .claude-ollama; got:\n  %s", overriddenOnCustom)
	}
	if strings.Contains(overriddenOnCustom, ".claude-ik") {
		t.Errorf("per-task override must replace, not stack with, project config; found .claude-ik too:\n  %s", overriddenOnCustom)
	}

	// Empty override falls back to the project's config dir (existing behavior preserved).
	fallback := claude.BuildCommand(
		&db.Task{ID: 3, Project: "ik", Port: 8082, WorktreePath: "/tmp/ik"},
		"", "")
	if !strings.Contains(fallback, ".claude-ik") {
		t.Errorf("empty per-task override should fall back to project config .claude-ik; got:\n  %s", fallback)
	}
}

// TestBuildCommandInjectsPerTaskEnv verifies a task's EnvJSON (from a workflow
// step's `env:` map) is rendered into the claude command as a process-env
// prefix — the mechanism that routes a step through ollama
// (ANTHROPIC_BASE_URL/AUTH_TOKEN + empty ANTHROPIC_API_KEY) WITHOUT swapping
// CLAUDE_CONFIG_DIR, so the default config dir (plugins, MCP, trusted
// worktrees) stays intact. This is the executor-side half of the per-step env
// feature; the propagation half is tested in internal/pipeline.
func TestBuildCommandInjectsPerTaskEnv(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.CreateProject(&db.Project{Name: "plain", Path: "/tmp/plain"}); err != nil {
		t.Fatal(err)
	}
	exec := New(database, &config.Config{})
	claude := exec.executorFactory.Get(db.ExecutorClaude)

	// Ollama routing env on an otherwise-default project: the prefix must set the
	// ollama base URL + token and explicitly empty ANTHROPIC_API_KEY, precede
	// `claude`, and NOT set CLAUDE_CONFIG_DIR (the whole point is to keep the
	// default config dir).
	cmd := claude.BuildCommand(
		&db.Task{ID: 1, Project: "plain", Port: 8080, WorktreePath: "/tmp/plain",
			EnvJSON: `{"ANTHROPIC_BASE_URL":"http://127.0.0.1:11434","ANTHROPIC_AUTH_TOKEN":"ollama","ANTHROPIC_API_KEY":""}`},
		"", "")
	for _, want := range []string{
		"ANTHROPIC_BASE_URL='http://127.0.0.1:11434'",
		"ANTHROPIC_AUTH_TOKEN='ollama'",
		"ANTHROPIC_API_KEY=''",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("env prefix missing %s; got:\n  %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "CLAUDE_CONFIG_DIR=") {
		t.Errorf("env routing must NOT swap CLAUDE_CONFIG_DIR (keeps default dir); got:\n  %s", cmd)
	}
	if ci, pi := strings.Index(cmd, "ANTHROPIC_BASE_URL="), strings.Index(cmd, " claude "); ci < 0 || pi < 0 || ci > pi {
		t.Errorf("env prefix must precede `claude`; got:\n  %s", cmd)
	}

	// Empty EnvJSON → no env prefix at all (existing behavior preserved).
	plain := claude.BuildCommand(
		&db.Task{ID: 2, Project: "plain", Port: 8081, WorktreePath: "/tmp/plain"},
		"", "")
	if strings.Contains(plain, "ANTHROPIC_BASE_URL=") || strings.Contains(plain, "ANTHROPIC_AUTH_TOKEN=") {
		t.Errorf("empty EnvJSON must add no env prefix; got:\n  %s", plain)
	}
}
