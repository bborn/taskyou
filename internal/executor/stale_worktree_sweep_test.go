package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// newGitRepo creates a real git repository with one commit and returns its path.
func newGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "initial")
	return dir
}

// addWorktree creates a linked worktree of repo at path on a new branch.
func addWorktree(t *testing.T, repo, path, branch string) string {
	t.Helper()
	cmd := exec.Command("git", "worktree", "add", "-b", branch, path)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return path
}

// newWorktreeSweepFixture wires an executor against a temp DB and a project whose
// directory is a real git repo using worktree isolation.
func newWorktreeSweepFixture(t *testing.T) (*Executor, *db.DB, string) {
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

// staleTask creates a done task completed long ago with the given worktree path.
func staleTask(t *testing.T, database *db.DB, worktreePath string) *db.Task {
	t.Helper()
	task := &db.Task{Title: "old work", Project: "proj", Type: db.TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-150 * 24 * time.Hour).UTC()
	if _, err := database.Exec(
		`UPDATE tasks SET status = 'done', worktree_path = ?, completed_at = ?, updated_at = ? WHERE id = ?`,
		worktreePath, old, old, task.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return reloaded
}

func taskUpdatedAt(t *testing.T, database *db.DB, id int64) time.Time {
	t.Helper()
	task, err := database.GetTask(id)
	if err != nil || task == nil {
		t.Fatalf("get task %d: %v", id, err)
	}
	return task.UpdatedAt.Time
}

// TestSweepSkipsMainWorkingTree is the regression test for the bug this fix
// exists for: a done task whose worktree_path is the project's MAIN checkout was
// swept every 10 minutes forever, each attempt logging a guaranteed
// "fatal: is a main working tree" and bumping updated_at so the months-old task
// kept resurfacing. The sweep must now be a complete no-op on the repo: no git
// removal, no archive state, and the bogus reference dropped.
func TestSweepSkipsMainWorkingTree(t *testing.T) {
	exec, database, repo := newWorktreeSweepFixture(t)
	task := staleTask(t, database, repo)
	before := taskUpdatedAt(t, database, task.ID)

	exec.cleanupStaleWorktrees()

	// The checkout itself must be untouched.
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("main checkout must survive the sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
		t.Fatalf("main checkout contents must survive the sweep: %v", err)
	}

	after, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.WorktreePath != "" {
		t.Errorf("worktree_path must be cleared, got %q", after.WorktreePath)
	}
	if after.ArchiveWorktreePath != "" {
		t.Errorf("archive_worktree_path must be cleared, got %q", after.ArchiveWorktreePath)
	}
	if after.ArchiveRef != "" {
		t.Errorf("no archive state may be written for a main working tree, got ref %q", after.ArchiveRef)
	}
	if !after.UpdatedAt.Time.Equal(before) {
		t.Errorf("a corrective sweep must not bump updated_at: %s -> %s", before, after.UpdatedAt.Time)
	}

	// And with the reference gone the row is no longer a sweep target at all.
	remaining, err := database.GetStaleWorktreeTasks(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range remaining {
		if r.ID == task.ID {
			t.Error("task must no longer be a stale-worktree sweep target")
		}
	}
}

// TestSweepArchivesLinkedWorktree: the guard must not break the case the sweeper
// exists for - a genuine linked worktree is still archived and removed.
func TestSweepArchivesLinkedWorktree(t *testing.T) {
	exec, database, repo := newWorktreeSweepFixture(t)
	wt := addWorktree(t, repo, filepath.Join(repo, ".task-worktrees", "1-old-work"), "task/old-work")
	task := staleTask(t, database, wt)

	exec.cleanupStaleWorktrees()

	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("linked worktree should have been removed, stat err = %v", err)
	}
	after, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.WorktreePath != "" {
		t.Errorf("worktree_path should be cleared after archiving, got %q", after.WorktreePath)
	}
	if after.ArchiveRef == "" {
		t.Error("archiving a real worktree must record archive state so it can be restored")
	}
}

// TestFailedSweepIsAttemptedOnce: an archive that cannot succeed is tried once,
// marked, and never retried by the automatic sweep.
func TestFailedSweepIsAttemptedOnce(t *testing.T) {
	exec, database, repo := newWorktreeSweepFixture(t)
	// A directory that exists, is not the main tree, and is not a git worktree:
	// archiving fails at the first git call.
	bogus := filepath.Join(repo, "..", "not-a-worktree")
	if err := os.MkdirAll(bogus, 0o755); err != nil {
		t.Fatal(err)
	}
	task := staleTask(t, database, bogus)

	exec.cleanupStaleWorktrees()

	failedAt, err := database.WorktreeSweepFailedAt(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failedAt == nil {
		t.Fatal("a failed archive must mark the task un-sweepable")
	}

	// Second sweep: the row is no longer offered as a target at all.
	targets, err := database.GetStaleWorktreeTasks(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if target.ID == task.ID {
			t.Error("an un-sweepable task must not be returned to the automatic sweep again")
		}
	}

	// The on-demand cleanup still sees it, so a human can retry.
	manual, err := database.GetStaleWorktreeTasksIncludingFailed(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range manual {
		if m.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Error("`task worktrees cleanup` must still be able to retry an un-sweepable task")
	}
}

// TestArchiveWorktreeRefusesMainWorkingTree covers the direct callers (the TUI's
// archive action), not just the sweeper.
func TestArchiveWorktreeRefusesMainWorkingTree(t *testing.T) {
	exec, database, repo := newWorktreeSweepFixture(t)
	task := staleTask(t, database, repo)

	if err := exec.ArchiveWorktree(task); err != nil {
		t.Fatalf("archiving a main working tree must be a silent no-op, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
		t.Fatalf("main checkout must survive: %v", err)
	}
	after, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.WorktreePath != "" || after.ArchiveRef != "" {
		t.Errorf("main-tree archive must clear the reference and write no archive state: path=%q ref=%q",
			after.WorktreePath, after.ArchiveRef)
	}
}

// TestAuditWorktreePathsFindsMainCheckouts: the audit reports (and with fix,
// clears) rows pointing at a main checkout, and leaves real worktrees alone.
func TestAuditWorktreePathsFindsMainCheckouts(t *testing.T) {
	exec, database, repo := newWorktreeSweepFixture(t)
	bad := staleTask(t, database, repo)
	wt := addWorktree(t, repo, filepath.Join(repo, ".task-worktrees", "2-good"), "task/good")
	good := staleTask(t, database, wt)

	issues, err := exec.AuditWorktreePaths(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].TaskID != bad.ID {
		t.Fatalf("audit should flag exactly the main-checkout row, got %+v", issues)
	}
	if issues[0].Fixed {
		t.Error("audit without --fix must not modify anything")
	}
	if got := taskWorktreePath(t, database, bad.ID); got != repo {
		t.Errorf("audit without --fix must leave the row alone, got %q", got)
	}

	if _, err := exec.AuditWorktreePaths(true); err != nil {
		t.Fatal(err)
	}
	if got := taskWorktreePath(t, database, bad.ID); got != "" {
		t.Errorf("audit --fix should clear the bad reference, got %q", got)
	}
	if got := taskWorktreePath(t, database, good.ID); got != wt {
		t.Errorf("audit --fix must not touch a real worktree, got %q", got)
	}
}

func taskWorktreePath(t *testing.T, database *db.DB, id int64) string {
	t.Helper()
	task, err := database.GetTask(id)
	if err != nil || task == nil {
		t.Fatalf("get task %d: %v", id, err)
	}
	return task.WorktreePath
}

// TestClassifyWorktreePath pins the classification the guards depend on.
func TestClassifyWorktreePath(t *testing.T) {
	root := t.TempDir()
	repo := newGitRepo(t, filepath.Join(root, "repo"))
	wt := addWorktree(t, repo, filepath.Join(root, "wt"), "feature")
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		want worktreeKind
	}{
		{"main working tree", repo, worktreeMain},
		{"subdir of main working tree", filepath.Join(repo, ".git"), worktreeMain},
		{"linked worktree", wt, worktreeLinked},
		{"plain directory", plain, worktreeUnknown},
		{"missing path", filepath.Join(root, "nope"), worktreeUnknown},
		{"empty path", "", worktreeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyWorktreePath(tc.path); got != tc.want {
				t.Errorf("classifyWorktreePath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}

	if !isMainWorkingTree(repo) {
		t.Error("isMainWorkingTree must be true for the main checkout")
	}
	if isMainWorkingTree(wt) {
		t.Error("isMainWorkingTree must be false for a linked worktree")
	}
}
