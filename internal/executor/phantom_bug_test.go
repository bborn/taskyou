package executor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// TestReleaseBranchFinishedHolderPhantomWorktree reproduces the phantom-holder
// stall: a finished step's worktree dir is removed out-of-band (rm -rf) and then
// the automatic cleanupStaleWorktrees' IsNotExist shortcut clears the DB
// worktree_path WITHOUT running `git worktree prune`. The git worktree registry
// entry survives, still pins the branch, but no DB row names it, so
// releaseBranchFromFinishedHolder's `task == nil` branch returns (false, nil) and
// the caller wraps that as ErrBranchBusy -- turning a phantom (no agent behind it)
// into what looks like transient contention and deferring for 30m before parking
// as blocked. A single `git worktree prune` frees the branch.
//
// This is a characterization test of the stall mechanism and the prune remedy at
// the spawn path; the root-cause fix lives in the sweeps (see
// TestSweepStaleWorktreePrunesPhantomHolder below). It deliberately replays the
// pre-fix sweep behaviour (ClearTaskWorktreePath with no prune) to show that
// releaseBranchFromFinishedHolder's conservative `task == nil` branch — which
// protects hand-made worktrees on a shared branch — cannot distinguish an
// orphaned registry entry from one, and so the prune must happen before the entry
// reaches the spawn path.
func TestReleaseBranchFinishedHolderPhantomWorktree(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holder := holdBranch(t, e, database, repo, branch, db.StatusDone)
	holderPath := holder.WorktreePath

	// 1. External removal of the worktree directory (docs-acknowledged rm -rf).
	if err := os.RemoveAll(holderPath); err != nil {
		t.Fatal(err)
	}
	if got := gitWorktreeHolder(repo, branch); got != holderPath {
		t.Fatalf("gitWorktreeHolder before sweep: got %q, want phantom %q", got, holderPath)
	}

	// 2. Replay the automatic cleanupStaleWorktrees' IsNotExist shortcut
	// (executor.go: IsNotExist branch): dir already gone -> ClearTaskWorktreePath
	// only, NO `git worktree prune`. With the root-cause fix the sweep prunes here
	// instead; this replay isolates the spawn path's behaviour from the sweep fix.
	if err := database.ClearTaskWorktreePath(holder.ID); err != nil {
		t.Fatalf("ClearTaskWorktreePath: %v", err)
	}
	reloaded, err := database.GetTask(holder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.WorktreePath != "" {
		t.Fatalf("after ClearTaskWorktreePath: worktree_path = %q, want \"\"", reloaded.WorktreePath)
	}
	if reloaded.BranchName != branch {
		t.Fatalf("after ClearTaskWorktreePath: branch_name = %q, want %q", reloaded.BranchName, branch)
	}
	if got := gitWorktreeHolder(repo, branch); got != holderPath {
		t.Fatalf("gitWorktreeHolder after sweep shortcut: got %q, want phantom %q", got, holderPath)
	}

	// 3. releaseBranchFromFinishedHolder: task == nil -> (false, nil), no prune.
	freed, err := e.releaseBranchFromFinishedHolder(repo, branch)
	if err != nil {
		t.Fatalf("releaseBranchFromFinishedHolder: unexpected err %v", err)
	}
	if freed {
		t.Fatalf("releaseBranchFromFinishedHolder: freed = true, want false")
	}
	if got := gitWorktreeHolder(repo, branch); got != holderPath {
		t.Fatalf("gitWorktreeHolder after release: got %q, want phantom %q", got, holderPath)
	}

	// 4. Caller wraps (false, nil) as ErrBranchBusy -> executeTask spins 30m.
	next := filepath.Join(t.TempDir(), "next")
	addErr := e.addSourceBranchWorktree(repo, next, branch)
	if addErr == nil {
		t.Fatalf("addSourceBranchWorktree on phantom-held branch: expected ErrBranchBusy, got nil")
	}
	if !errors.Is(addErr, ErrBranchBusy) {
		t.Fatalf("addSourceBranchWorktree: err = %v, want it to wrap ErrBranchBusy", addErr)
	}

	// 5. The fix: a single `git worktree prune` frees the branch.
	if out, err := exec.Command("git", "-C", repo, "worktree", "prune").CombinedOutput(); err != nil {
		t.Fatalf("explicit prune: %v\n%s", err, out)
	}
	if got := gitWorktreeHolder(repo, branch); got != "" {
		t.Fatalf("gitWorktreeHolder after prune: got %q, want \"\"", got)
	}

	// 6. After prune, the next step attaches to the shared branch cleanly.
	if err := e.addSourceBranchWorktree(repo, next, branch); err != nil {
		t.Fatalf("addSourceBranchWorktree after prune: %v", err)
	}
}

// TestSweepStaleWorktreePrunesPhantomHolder is the regression test for the
// root-cause fix: when the automatic cleanupStaleWorktrees encounters a task whose
// worktree directory was already removed out-of-band, it must `git worktree prune`
// the orphaned registry entry in the same tick — not merely clear the DB
// worktree_path. Before the fix the phantom survived and a later step reusing
// the shared branch was handed an ErrBranchBusy for a holder that no agent was
// driving, deferring it for up to 30 minutes and then parking it as 'blocked'.
func TestSweepStaleWorktreePrunesPhantomHolder(t *testing.T) {
	exec, database, repo := newWorktreeSweepFixture(t)
	branch := "pipeline/demo-shared"
	holderPath := addWorktree(t, repo, filepath.Join(t.TempDir(), "holder"), branch)
	task := staleTask(t, database, holderPath)

	// External removal of the worktree directory (docs-acknowledged rm -rf).
	if err := os.RemoveAll(holderPath); err != nil {
		t.Fatal(err)
	}
	// The git registry still lists the now-prunable worktree, pinning the branch.
	if got := gitWorktreeHolder(repo, branch); got != holderPath {
		t.Fatalf("pre-sweep: gitWorktreeHolder = %q, want phantom %q", got, holderPath)
	}

	// The automatic sweep used to only clear the DB reference on this path,
	// leaving the phantom pinning the branch (-> ErrBranchBusy -> 30m defer ->
	// blocked). It must now prune the orphaned registry entry too.
	exec.cleanupStaleWorktrees()

	if got := gitWorktreeHolder(repo, branch); got != "" {
		t.Fatalf("post-sweep: gitWorktreeHolder = %q, want \"\" (orphan pruned)", got)
	}
	reloaded, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.WorktreePath != "" {
		t.Errorf("post-sweep: worktree_path = %q, want \"\"", reloaded.WorktreePath)
	}

	// A later step reusing the shared branch attaches cleanly: no 30-minute
	// ErrBranchBusy deferral behind a phantom holder.
	next := filepath.Join(t.TempDir(), "next")
	if err := exec.addSourceBranchWorktree(repo, next, branch); err != nil {
		t.Fatalf("addSourceBranchWorktree after sweep: %v (branch should be free)", err)
	}
	if got, gErr := gitCurrentBranch(next); gErr != nil || got != branch {
		t.Fatalf("next step is on %q (err %v), want %q", got, gErr, branch)
	}
}

// TestSweepTrashedTaskPrunesPhantomHolder covers the secondary producer of an
// orphaned registry entry: sweepTrashedTasks skips CleanupWorktree when the
// trashed task's worktree dir is already gone and jumps straight to DeleteTask,
// which used to leave the git registry pinning the branch with no DB row naming
// it. The sweep must now prune the orphaned entry before deleting the row.
func TestSweepTrashedTaskPrunesPhantomHolder(t *testing.T) {
	exec, database, repo := newWorktreeSweepFixture(t)
	branch := "pipeline/trashed-shared"
	holderPath := addWorktree(t, repo, filepath.Join(t.TempDir(), "holder"), branch)

	// A task whose worktree is on a shared branch, soft-deleted and backdated past
	// the trash retention so the sweep will hard-delete it. CreateTask does not
	// write worktree_path/branch_name, so set them directly like staleTask does.
	task := &db.Task{Title: "trashed step", Project: "proj", Type: db.TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`UPDATE tasks SET worktree_path = ?, branch_name = ? WHERE id = ?`,
		holderPath, branch, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.SoftDeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	// DefaultTrashRetention is 14d; backdate well past it.
	if _, err := database.Exec(`UPDATE tasks SET deleted_at = ? WHERE id = ?`,
		time.Now().Add(-30*24*time.Hour).UTC(), task.ID); err != nil {
		t.Fatal(err)
	}

	// External removal of the worktree directory (docs-acknowledged rm -rf).
	if err := os.RemoveAll(holderPath); err != nil {
		t.Fatal(err)
	}
	if got := gitWorktreeHolder(repo, branch); got != holderPath {
		t.Fatalf("pre-sweep: gitWorktreeHolder = %q, want phantom %q", got, holderPath)
	}

	// The trash sweep's dir-gone branch used to skip CleanupWorktree and jump
	// straight to DeleteTask, leaving the phantom pinning the branch. It must
	// now prune the orphaned registry entry too.
	exec.sweepTrashedTasks()

	if got := gitWorktreeHolder(repo, branch); got != "" {
		t.Fatalf("post-sweep: gitWorktreeHolder = %q, want \"\" (orphan pruned)", got)
	}
	if rowExists(t, database, task.ID) {
		t.Error("trashed task should have been hard-deleted by the sweep")
	}

	// The freed branch is available for a later step immediately.
	next := filepath.Join(t.TempDir(), "next")
	if err := exec.addSourceBranchWorktree(repo, next, branch); err != nil {
		t.Fatalf("addSourceBranchWorktree after trash sweep: %v (branch should be free)", err)
	}
	if got, gErr := gitCurrentBranch(next); gErr != nil || got != branch {
		t.Fatalf("next step is on %q (err %v), want %q", got, gErr, branch)
	}
}
