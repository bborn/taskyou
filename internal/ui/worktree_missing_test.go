package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// The regression this file guards: task 5040 kept a worktree path whose
// directory had been reaped. Opening its detail view started an executor whose
// pane could never be adopted, so setup ran again — 178 Claude sessions in 30
// minutes. Detection and the resulting decision are asserted separately so a
// future change to either half fails loudly.

func TestTaskWorktreeMissingDetectsReapedWorktree(t *testing.T) {
	present := t.TempDir()

	reaped := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(reaped, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.RemoveAll(reaped); err != nil {
		t.Fatalf("remove: %v", err)
	}

	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cases := []struct {
		name    string
		task    *db.Task
		wantPth string
		want    bool
	}{
		{"nil task", nil, "", false},
		{"no worktree recorded", &db.Task{ID: 1}, "", false},
		{"worktree on disk", &db.Task{ID: 2, WorktreePath: present}, "", false},
		{"worktree reaped", &db.Task{ID: 3, WorktreePath: reaped}, reaped, true},
		{"path is not a directory", &db.Task{ID: 4, WorktreePath: notADir}, notADir, true},
		// A remotely placed task keeps a path that only exists on the remote
		// host; absence locally says nothing and must not halt setup.
		{"remote task is never missing", &db.Task{ID: 5, WorktreePath: reaped, PlacementTarget: "mona"}, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, missing := taskWorktreeMissing(tc.task)
			if missing != tc.want || path != tc.wantPth {
				t.Errorf("taskWorktreeMissing() = (%q, %v), want (%q, %v)", path, missing, tc.wantPth, tc.want)
			}
		})
	}
}

// A task whose worktree is gone must never reach the start path, which is what
// spawned an executor into $HOME and set the loop going.
func TestPendingPaneActionHaltsWhenWorktreeMissing(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   paneAction
	}{
		{"blocked halts instead of starting", db.StatusBlocked, paneActionWorktreeMissing},
		// Daemon-owned tasks still wait: the daemon creates the worktree during
		// spin-up, so "missing" here is a race, not a dead end.
		{"queued still waits for the daemon", db.StatusQueued, paneActionWaitForExecutor},
		{"processing still waits for the daemon", db.StatusProcessing, paneActionWaitForExecutor},
		{"done still skips", db.StatusDone, paneActionSkip},
		{"archived still skips", db.StatusArchived, paneActionSkip},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &db.Task{ID: 5040, Status: tc.status, WorktreePath: "/gone/5040"}
			if got := pendingPaneAction(task, true); got != tc.want {
				t.Errorf("pendingPaneAction(%s, missing) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
