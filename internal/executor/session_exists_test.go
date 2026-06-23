package executor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestClaudeSessionExists is the predicate behind the "lost executor pane"
// recovery: EnsureTaskWindow falls back to a fresh session when a stored session
// ID no longer maps to a file on disk (Claude prunes session JSONLs after
// cleanupPeriodDays). It must resolve the per-project config dir and apply the
// same path escaping the resume command uses.
func TestClaudeSessionExists(t *testing.T) {
	configDir := t.TempDir()
	// A worktree path with a dot (".task-worktrees") exercises the dot-escaping
	// that the influencekit custom-config-dir tasks hit.
	workDir := "/Users/someone/Projects/app/.task-worktrees/2167-redesign"
	sessionID := "762a60c0-8209-4418-a739-b91c447596d7"

	// escaped: "/" and "." both become "-"
	escaped := "-Users-someone-Projects-app--task-worktrees-2167-redesign"
	projectDir := filepath.Join(configDir, "projects", escaped)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if ClaudeSessionExists(sessionID, workDir, configDir) {
		t.Fatalf("expected false before the session file is written")
	}

	sessionFile := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !ClaudeSessionExists(sessionID, workDir, configDir) {
		t.Fatalf("expected true once %s exists", sessionFile)
	}

	// A different (aged-out) session ID for the same worktree must read as gone.
	if ClaudeSessionExists("nonexistent-session-id", workDir, configDir) {
		t.Fatalf("expected false for a session ID with no file on disk")
	}

	// Empty session ID is never resumable.
	if ClaudeSessionExists("", workDir, configDir) {
		t.Fatalf("expected false for an empty session ID")
	}
}
