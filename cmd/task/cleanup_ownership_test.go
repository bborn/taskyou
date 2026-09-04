package main

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// The failure this guards against: an agent placed on another host ran the
// taskyou test suite, cleanupOrphanedSessions walked the host's tmux, found the
// agent's own window for a task that lives in a DIFFERENT machine's database,
// called it deleted, and killed it. The agent's shell command came back "exit
// code 137" and the task parked as "Task needs review" with nothing explaining
// why. Four attempts died this way.
func TestCleanupOrphanedSessions_SparesAnotherMachinesSession(t *testing.T) {
	requireTmux(t)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("WORKTREE_DB_PATH", dbPath)
	t.Setenv("WORKTREE_SESSION_ID", "")
	if got := db.DefaultPath(); got != dbPath {
		t.Skipf("db.DefaultPath() does not honor WORKTREE_DB_PATH (got %q)", got)
	}

	socketDir, err := os.MkdirTemp("/tmp", "tytmux")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	t.Setenv("TMUX_TMPDIR", socketDir)
	t.Cleanup(func() {
		osexec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(socketDir)
	})
	suppressStdout(t)

	// A session owned by another machine, holding a window for a task ID this
	// machine's database has never heard of — exactly the placed-task shape.
	const session = "task-daemon-82562"
	const remoteTaskID = 5289
	window := fmt.Sprintf("task-%d", remoteTaskID)
	if err := osexec.Command("tmux", "new-session", "-d", "-s", session, "-n", window, "sleep", "60").Run(); err != nil {
		t.Fatalf("create foreign session: %v", err)
	}
	if err := osexec.Command("tmux", "set-option", "-t", session, executor.TmuxOwnerOption, "someone-elses-laptop").Run(); err != nil {
		t.Fatalf("tag foreign session: %v", err)
	}

	cleanupOrphanedSessions(false)

	if !windowExists(session, window) {
		t.Fatal("cleanup killed a live window belonging to another machine's daemon")
	}
}

// The feature still has to work: our OWN session's orphan is still collected.
func TestCleanupOrphanedSessions_StillKillsOurOwnOrphan(t *testing.T) {
	session := setupCleanupTest(t, "owned")
	const orphanID = 992002
	makeDaemonSessionWithName(t, session, orphanID)
	if err := osexec.Command("tmux", "set-option", "-t", session, executor.TmuxOwnerOption, executor.LocalOwnerTag()).Run(); err != nil {
		t.Fatalf("tag own session: %v", err)
	}

	cleanupOrphanedSessions(false)

	if windowExists(session, fmt.Sprintf("task-%d", orphanID)) {
		t.Error("our own orphan window survived cleanup")
	}
}

// The tag is new; every session created before it is untagged — including the
// long-lived daemon session on a placed host, which is the exact one whose live
// agent window kept being killed. An untagged session must therefore be treated
// as unknown, not as ours, or the ownership guard protects only sessions that
// were never in danger.
func TestCleanupOrphanedSessions_SparesAnUntaggedSession(t *testing.T) {
	session := setupCleanupTest(t, "untagged")
	const foreignTaskID = 5289
	makeDaemonSessionWithName(t, session, foreignTaskID)
	// Deliberately NO @ty_owner option set.

	cleanupOrphanedSessions(false)

	if !windowExists(session, fmt.Sprintf("task-%d", foreignTaskID)) {
		t.Fatal("cleanup killed a window in an untagged session; untagged means unknown, not ours")
	}
}
