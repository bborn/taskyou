package db

import (
	"path/filepath"
	"testing"
)

// openWorktreeRefDB builds a fresh database with one project, returns a
// freshly created task (whose ID the caller controls through the returned
// *Task), and a helper that writes arbitrary worktree_path / archive_worktree_path
// pairs directly to the row for setup of TOCTOU-style states.
func openWorktreeRefDB(t *testing.T) (*DB, *Task) {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := database.CreateProject(&Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	task := &Task{Title: "t", Status: StatusDone, Type: TypeCode, Project: "p"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	// Set completed_at so the row really is a fully-realized "done" task.
	if _, err := database.Exec(
		`UPDATE tasks SET completed_at = datetime('now', '-30 days'), updated_at = datetime('now', '-30 days') WHERE id = ?`,
		task.ID,
	); err != nil {
		t.Fatalf("stamp task: %v", err)
	}
	return database, task
}

func setWorktreeRef(t *testing.T, database *DB, taskID int64, worktreePath, archivePath string) {
	t.Helper()
	if _, err := database.Exec(
		`UPDATE tasks SET worktree_path = ?, archive_worktree_path = ? WHERE id = ?`,
		worktreePath, archivePath, taskID,
	); err != nil {
		t.Fatalf("set worktree refs: %v", err)
	}
}

func mustGetTask(t *testing.T, database *DB, id int64) *Task {
	t.Helper()
	got, err := database.GetTask(id)
	if err != nil || got == nil {
		t.Fatalf("get task %d: %v", id, err)
	}
	return got
}

// TestClearTaskWorktreeRefsIfMatch verifies the conditional UPDATE that backs the
// audit's TOCTOU defense: it only clears when the row still matches the
// snapshot, returns whether the clear happened, leaves updated_at alone, and
// preserves saved archive state.
func TestClearTaskWorktreeRefsIfMatch(t *testing.T) {
	cases := []struct {
		name           string
		dbWorktree     string
		dbArchive      string
		wantWorktree   string
		wantArchive    string
		wantCleared    bool
		wantWorktreeDB string
		wantArchiveDB  string
	}{
		{
			name:           "both match the snapshot - cleared",
			dbWorktree:     "/proj/.git",
			dbArchive:      "",
			wantWorktree:   "/proj/.git",
			wantArchive:    "",
			wantCleared:    true,
			wantWorktreeDB: "",
			wantArchiveDB:  "",
		},
		{
			name:           "worktree_path no longer matches (daemon replaced it) - skip",
			dbWorktree:     "/proj/.task-worktrees/5-fresh",
			dbArchive:      "",
			wantWorktree:   "/proj/.git",
			wantArchive:    "",
			wantCleared:    false,
			wantWorktreeDB: "/proj/.task-worktrees/5-fresh",
			wantArchiveDB:  "",
		},
		{
			name:           "archive_worktree_path no longer matches - skip",
			dbWorktree:     "/proj/.git",
			dbArchive:      "/proj/.task-worktrees/3-restored",
			wantWorktree:   "/proj/.git",
			wantArchive:    "",
			wantCleared:    false,
			wantWorktreeDB: "/proj/.git",
			wantArchiveDB:  "/proj/.task-worktrees/3-restored",
		},
		{
			name:           "both differ from the snapshot - skip",
			dbWorktree:     "/proj/.task-worktrees/5-fresh",
			dbArchive:      "/proj/.task-worktrees/3-restored",
			wantWorktree:   "/proj/.git",
			wantArchive:    "",
			wantCleared:    false,
			wantWorktreeDB: "/proj/.task-worktrees/5-fresh",
			wantArchiveDB:  "/proj/.task-worktrees/3-restored",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, task := openWorktreeRefDB(t)
			setWorktreeRef(t, database, task.ID, tc.dbWorktree, tc.dbArchive)
			before := mustGetTask(t, database, task.ID).UpdatedAt.Time

			cleared, err := database.ClearTaskWorktreeRefsIfMatch(task.ID, tc.wantWorktree, tc.wantArchive)
			if err != nil {
				t.Fatalf("ClearTaskWorktreeRefsIfMatch: %v", err)
			}
			if cleared != tc.wantCleared {
				t.Fatalf("cleared = %v, want %v", cleared, tc.wantCleared)
			}

			got := mustGetTask(t, database, task.ID)
			if got.WorktreePath != tc.wantWorktreeDB {
				t.Errorf("worktree_path = %q, want %q", got.WorktreePath, tc.wantWorktreeDB)
			}
			if got.ArchiveWorktreePath != tc.wantArchiveDB {
				t.Errorf("archive_worktree_path = %q, want %q", got.ArchiveWorktreePath, tc.wantArchiveDB)
			}
			// Like ClearTaskWorktreeRefs, the conditional clear must never bump updated_at.
			if !got.UpdatedAt.Time.Equal(before) {
				t.Errorf("updated_at must not move: %s -> %s", before, got.UpdatedAt.Time)
			}
		})
	}
}

// TestClearTaskWorktreeRefsIfMatchMissingRow: a non-existent taskID is a no-op
// with no error, never a false-positive "cleared" result.
func TestClearTaskWorktreeRefsIfMatchMissingRow(t *testing.T) {
	database, _ := openWorktreeRefDB(t)
	cleared, err := database.ClearTaskWorktreeRefsIfMatch(999999, "", "")
	if err != nil {
		t.Fatalf("ClearTaskWorktreeRefsIfMatch missing row: %v", err)
	}
	if cleared {
		t.Errorf("cleared = true for a missing row, want false")
	}
}

// TestClearTaskWorktreeRefsIfMatchPreservesArchiveState: the clear drops only
// the two path columns; saved archive metadata the unarchive flow needs is
// left alone.
func TestClearTaskWorktreeRefsIfMatchPreservesArchiveState(t *testing.T) {
	database, task := openWorktreeRefDB(t)
	if _, err := database.Exec(
		`UPDATE tasks SET worktree_path = ?, archive_worktree_path = ?,
		                  archive_ref = ?, archive_commit = ?, archive_branch_name = ? WHERE id = ?`,
		"/proj/.git", "", "refs/task-archive/7", "abcdef", "task/old", task.ID,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cleared, err := database.ClearTaskWorktreeRefsIfMatch(task.ID, "/proj/.git", "")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !cleared {
		t.Fatalf("expected a cleared row when values match")
	}

	got := mustGetTask(t, database, task.ID)
	if got.WorktreePath != "" || got.ArchiveWorktreePath != "" {
		t.Errorf("worktree_path/archive_worktree_path should be cleared: wt=%q archive=%q",
			got.WorktreePath, got.ArchiveWorktreePath)
	}
	if got.ArchiveRef != "refs/task-archive/7" {
		t.Errorf("archive_ref must survive the clear, got %q", got.ArchiveRef)
	}
	if got.ArchiveCommit != "abcdef" {
		t.Errorf("archive_commit must survive the clear, got %q", got.ArchiveCommit)
	}
	if got.ArchiveBranchName != "task/old" {
		t.Errorf("archive_branch_name must survive the clear, got %q", got.ArchiveBranchName)
	}
}
