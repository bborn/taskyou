package executor

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestClaudePermissionFlag(t *testing.T) {
	// Ensure the global env override is not set for these cases.
	t.Setenv("WORKTREE_DANGEROUS_MODE", "")

	cases := []struct {
		name string
		task *db.Task
		want string
	}{
		{"default empty", &db.Task{}, ""},
		{"explicit default", &db.Task{PermissionMode: db.PermissionModeDefault}, ""},
		{"auto", &db.Task{PermissionMode: db.PermissionModeAuto}, "--permission-mode acceptEdits "},
		{"accept-edits alias", &db.Task{PermissionMode: "accept-edits"}, "--permission-mode acceptEdits "},
		{"dangerous", &db.Task{PermissionMode: db.PermissionModeDangerous}, "--dangerously-skip-permissions "},
		{"legacy dangerous bool", &db.Task{DangerousMode: true}, "--dangerously-skip-permissions "},
	}
	for _, c := range cases {
		if got := claudePermissionFlag(c.task); got != c.want {
			t.Errorf("%s: claudePermissionFlag() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestClaudePermissionFlagGlobalDangerousEnv(t *testing.T) {
	t.Setenv("WORKTREE_DANGEROUS_MODE", "1")
	// Even an auto-mode task is forced to dangerous in a sandboxed environment.
	got := claudePermissionFlag(&db.Task{PermissionMode: db.PermissionModeAuto})
	if got != "--dangerously-skip-permissions " {
		t.Errorf("WORKTREE_DANGEROUS_MODE should force dangerous, got %q", got)
	}
}
