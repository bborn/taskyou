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
		{"auto", &db.Task{PermissionMode: db.PermissionModeAuto}, "--permission-mode auto "},
		{"accept-edits", &db.Task{PermissionMode: db.PermissionModeAcceptEdits}, "--permission-mode acceptEdits "},
		{"acceptEdits spelling normalizes", &db.Task{PermissionMode: "acceptEdits"}, "--permission-mode acceptEdits "},
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

func TestPermissionFlagForMode(t *testing.T) {
	cases := map[string]string{
		db.PermissionModeDefault:     "",
		db.PermissionModeAcceptEdits: "--permission-mode acceptEdits ",
		db.PermissionModeAuto:        "--permission-mode auto ",
		db.PermissionModeDangerous:   "--dangerously-skip-permissions ",
	}
	for mode, want := range cases {
		if got := permissionFlagForMode(mode); got != want {
			t.Errorf("permissionFlagForMode(%q) = %q, want %q", mode, got, want)
		}
	}
}

// TestSafeResumeFlagPreservesMode is the regression for the bug where the safe
// side of the permission-mode toggle relaunched with a bare `claude --resume`,
// silently dropping an auto/accept-edits task into prompt-for-everything. A safe
// resume must keep the task's non-dangerous mode, and dangerous must degrade to
// default (never to a bypass flag).
func TestSafeResumeFlagPreservesMode(t *testing.T) {
	cases := []struct {
		mode string
		want string // flag a "safe" resume should launch with
	}{
		{db.PermissionModeAuto, "--permission-mode auto "},               // was bug: came back as ""
		{db.PermissionModeAcceptEdits, "--permission-mode acceptEdits "}, // was bug: came back as ""
		{db.PermissionModeDefault, ""},
		{db.PermissionModeDangerous, ""}, // safe can never mean bypass
	}
	for _, c := range cases {
		got := permissionFlagForMode(safePermissionMode(c.mode))
		if got != c.want {
			t.Errorf("safe resume of %q: flag = %q, want %q", c.mode, got, c.want)
		}
	}
}
