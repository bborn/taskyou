package executor

import "testing"

// When WORKTREE_SESSION_ID is set (e.g. an isolated QA instance), the daemon must
// use task-daemon-<sid> and NEVER adopt an arbitrary existing task-daemon-* session
// — otherwise a second daemon collides with the live instance's tmux session.
func TestGetDaemonSessionName_HonorsSessionIDOverExisting(t *testing.T) {
	orig := findExistingDaemonSession
	t.Cleanup(func() { findExistingDaemonSession = orig })
	// Simulate a live daemon session already present on the tmux server.
	findExistingDaemonSession = func() string { return "task-daemon-LIVE99" }

	t.Setenv("WORKTREE_SESSION_ID", "qa-iso")
	if got := getDaemonSessionName(); got != "task-daemon-qa-iso" {
		t.Errorf("got %q, want task-daemon-qa-iso (must honor WORKTREE_SESSION_ID before any existing session)", got)
	}
}

// With no session id set, it still falls back to reusing an existing session.
func TestGetDaemonSessionName_ReusesExistingWhenNoSessionID(t *testing.T) {
	orig := findExistingDaemonSession
	t.Cleanup(func() { findExistingDaemonSession = orig })
	findExistingDaemonSession = func() string { return "task-daemon-EXISTING" }

	t.Setenv("WORKTREE_SESSION_ID", "")
	if got := getDaemonSessionName(); got != "task-daemon-EXISTING" {
		t.Errorf("got %q, want task-daemon-EXISTING (reuse existing when no session id)", got)
	}
}
