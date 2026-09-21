package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// archivedRemoteTask builds the DB shape the archive→unarchive flow of a
// previously-remote task leaves behind: a recorded remote decision, a remote
// worktree, and saved archive state. It returns a fresh in-memory task loaded
// from that row so callers see exactly what resolvePlacement / UnarchiveWorktree
// would see in production.
func archivedRemoteTask(t *testing.T, database *db.DB, host, remoteWorktree string) *db.Task {
	t.Helper()
	task := placementTestTask(t, database)
	if err := database.SetTaskPlacementDecision(task.ID, host, "moved by hand", remoteWorktree); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskRemoteWorktree(task.ID, remoteWorktree, "task/7"); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveArchiveState(task.ID, "refs/task-archive/7", "deadbeef", "/local/.task-worktrees/7", "task/7"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return reloaded
}

// resolvePlacement's ArchiveRef branch decides "run here" and then, as of the
// fix, clears the stale remote placement that decision contradicts. The cleared
// placement is recorded as a real local decision so the next spawn reuses it
// verbatim, and every surface that trusts PlacementTarget (TaskCodeLocation,
// the browser bridge, the editor button) reports local.
func TestResolvePlacementClearsStaleRemotePlacementForArchivedTask(t *testing.T) {
	e, database := placementExecutor(t, t.TempDir())
	task := archivedRemoteTask(t, database, "ol-agents", "/home/olgm/.task-worktrees/7")
	if task.PlacementTarget != "ol-agents" {
		t.Fatalf("setup: PlacementTarget = %q, want stale ol-agents", task.PlacementTarget)
	}
	if task.ArchiveRef == "" {
		t.Fatal("setup: archive state not saved")
	}

	runner, _, err := e.resolvePlacement(context.Background(), task)
	if err != nil {
		t.Fatalf("resolvePlacement: %v", err)
	}
	if _, ok := runner.(LocalRunner); !ok {
		t.Fatalf("runner = %T, want LocalRunner for an archived task", runner)
	}

	// The decision is committed to local in memory...
	if task.PlacementTarget != "" {
		t.Errorf("in-memory PlacementTarget = %q, want cleared", task.PlacementTarget)
	}

	// ...and in the database, recorded as a decision the next spawn reuses.
	decided, err := database.GetTaskPlacementDecision(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !decided.Decided {
		t.Error("cleared placement was not recorded as a decision, so the next spawn would not reuse it")
	}
	if decided.Target != "" {
		t.Errorf("recorded placement target = %q, want empty (local)", decided.Target)
	}
	if rwp, _, _ := database.GetTaskRemoteWorktree(task.ID); rwp != "" {
		t.Errorf("remote_worktree_path = %q, want cleared (the task no longer runs there)", rwp)
	}

	// And the surfaces that trusted PlacementTarget now see a local task —
	// loaded fresh from the DB, the way an HTTP handler reads it.
	reloaded, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if TaskCodeLocation(database, reloaded).Remote() {
		t.Error("TaskCodeLocation still reports the unarchived task as remote")
	}
}

// An archived task that always ran here (no recorded remote placement) must not
// get a spurious placement decision written: the guard only clears a stale
// REMOTE target, so a never-placed task is left exactly as it was.
func TestResolvePlacementLeavesAlwaysLocalArchivedTaskUnwritten(t *testing.T) {
	e, database := placementExecutor(t, t.TempDir())
	task := placementTestTask(t, database)
	if err := database.SaveArchiveState(task.ID, "refs/task-archive/8", "cafe", "/local/wt", "task/8"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := database.GetTaskPlacementDecision(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Decided {
		t.Fatal("setup: a never-placed task should not start with a decision")
	}

	if _, _, err := e.resolvePlacement(context.Background(), reloaded); err != nil {
		t.Fatalf("resolvePlacement: %v", err)
	}

	after, err := database.GetTaskPlacementDecision(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Decided {
		t.Errorf("an always-local archived task recorded a decision (%q) it never had", after.Reason)
	}
}

// newArchiveFixture wires an executor against a real git repo whose project
// uses worktree isolation, so the real ArchiveWorktree/UnarchiveWorktree path
// (git worktree add/remove, archive refs, the works) can run end to end.
func newArchiveFixture(t *testing.T) (*Executor, *db.DB, string) {
	t.Helper()
	root := t.TempDir()
	repo := newGitRepo(t, filepath.Join(root, "proj"))
	database, err := db.Open(filepath.Join(root, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "proj", Path: repo, UseWorktrees: true}); err != nil {
		t.Fatal(err)
	}
	return New(database, config.New(database)), database, repo
}

// A task that is placed on a remote host, archived, and then unarchived must
// end up running locally with PlacementTarget cleared — on the unarchive path
// itself, not only on the next spawn. UnarchiveWorktree restores the worktree
// HERE, so it clears the stale remote placement at the same moment, using the
// same field-set a real local decision writes.
func TestUnarchiveWorktreeClearsStaleRemotePlacement(t *testing.T) {
	exec, database, repo := newArchiveFixture(t)

	// A real linked worktree on a real branch — the precondition that makes
	// ArchiveWorktree save archive state instead of dropping a bogus ref.
	wt := filepath.Join(repo, ".task-worktrees", "9-original")
	addWorktree(t, repo, wt, "task/9")

	task := &db.Task{Title: "moved then archived", Project: "proj", Type: db.TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`UPDATE tasks SET worktree_path = ?, branch_name = ? WHERE id = ?`,
		wt, "task/9", task.ID); err != nil {
		t.Fatal(err)
	}
	// The task ran remotely before it was archived: remote placement + worktree.
	if err := database.SetTaskPlacementDecision(task.ID, "ol-agents", "moved by hand", "/home/olgm/.task-worktrees/9"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskRemoteWorktree(task.ID, "/home/olgm/.task-worktrees/9", "task/9"); err != nil {
		t.Fatal(err)
	}

	archived, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Step 1: archive (the sweep / a manual archive). Saves archive state and
	// removes the worktree; the placement is left untouched (the bug).
	if err := exec.ArchiveWorktree(archived); err != nil {
		t.Fatalf("ArchiveWorktree: %v", err)
	}
	afterArchive, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterArchive.ArchiveRef == "" {
		t.Fatal("archive state was not saved")
	}
	if afterArchive.WorktreePath != "" {
		t.Errorf("after archive, worktree_path = %q, want cleared", afterArchive.WorktreePath)
	}
	if afterArchive.PlacementTarget != "ol-agents" {
		t.Errorf("after archive, PlacementTarget = %q (precondition: the archive/unarchive flow leaves it stale)", afterArchive.PlacementTarget)
	}

	// Step 2: unarchive (setupWorktree, or the board's unarchive button).
	// Restores the worktree HERE and — with the fix — clears the placement.
	if err := exec.UnarchiveWorktree(afterArchive); err != nil {
		t.Fatalf("UnarchiveWorktree: %v", err)
	}
	afterUnarchive, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUnarchive.ArchiveRef != "" {
		t.Errorf("after unarchive, ArchiveRef = %q, want cleared", afterUnarchive.ArchiveRef)
	}
	if afterUnarchive.WorktreePath == "" {
		t.Fatal("unarchive did not restore a local worktree path")
	}
	if st, err := os.Stat(afterUnarchive.WorktreePath); err != nil || !st.IsDir() {
		t.Errorf("unarchive did not restore a real local worktree directory: %v (path=%q)", err, afterUnarchive.WorktreePath)
	}
	if afterUnarchive.PlacementTarget != "" {
		t.Errorf("after unarchive, PlacementTarget = %q, want cleared (the worktree is on THIS machine)", afterUnarchive.PlacementTarget)
	}
	decided, err := database.GetTaskPlacementDecision(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !decided.Decided || decided.Target != "" {
		t.Errorf("unarchive left placement decision = %+v, want Decided with empty Target", decided)
	}
	if rwp, _, _ := database.GetTaskRemoteWorktree(task.ID); rwp != "" {
		t.Errorf("after unarchive, remote_worktree_path = %q, want cleared", rwp)
	}

	// The exact condition resolveTaskRoot checks: remote → drop the payload.
	if TaskCodeLocation(database, afterUnarchive).Remote() {
		t.Error("the unarchived task is still reported as remote, so the browser bridge would drop its screenshots")
	}
	if loc := TaskCodeLocation(database, afterUnarchive); loc.Path != afterUnarchive.WorktreePath {
		t.Errorf("TaskCodeLocation path = %q, want the restored local worktree %q", loc.Path, afterUnarchive.WorktreePath)
	}
}

// A task that was always local (never placed remotely) is unarchived: the guard
// means UnarchiveWorktree must not write a placement decision that was never
// made, only clear the stale REMOTE case. This keeps the always-local path
// byte-for-byte what it was.
func TestUnarchiveWorktreeLeavesAlwaysLocalTaskUnwritten(t *testing.T) {
	exec, database, repo := newArchiveFixture(t)

	wt := filepath.Join(repo, ".task-worktrees", "10-local")
	addWorktree(t, repo, wt, "task/10")

	task := &db.Task{Title: "always local", Project: "proj", Type: db.TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`UPDATE tasks SET worktree_path = ?, branch_name = ? WHERE id = ?`,
		wt, "task/10", task.ID); err != nil {
		t.Fatal(err)
	}
	before, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.ArchiveWorktree(before); err != nil {
		t.Fatalf("ArchiveWorktree: %v", err)
	}
	archived, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ArchiveRef == "" {
		t.Fatal("setup: archive state not saved")
	}
	decPre, _ := database.GetTaskPlacementDecision(task.ID)
	if decPre.Decided {
		t.Fatal("setup: a never-placed task should not start with a decision")
	}

	if err := exec.UnarchiveWorktree(archived); err != nil {
		t.Fatalf("UnarchiveWorktree: %v", err)
	}

	decPost, _ := database.GetTaskPlacementDecision(task.ID)
	if decPost.Decided {
		t.Errorf("an always-local archived task recorded a decision (%q) it never had", decPost.Reason)
	}
}
