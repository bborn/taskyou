package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestWorkflowStepFinished: a step counts as finished only when its worktree is
// clean AND its HEAD is pushed to its upstream — the "did the handoff" signal used
// to auto-advance a step whose agent forgot taskyou_complete.
func TestWorkflowStepFinished(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	git(t, root, "init", "--bare", remote)
	wt := filepath.Join(root, "wt")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "init")
	git(t, wt, "remote", "add", "origin", remote)

	task := &db.Task{WorktreePath: wt}

	// Committed but NOT pushed → not finished.
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-m", "work")
	if workflowStepFinished(task) {
		t.Error("committed but unpushed step should NOT be finished")
	}

	// Pushed + clean → finished.
	git(t, wt, "push", "-u", "origin", "HEAD")
	if !workflowStepFinished(task) {
		t.Error("clean + pushed step SHOULD be finished")
	}

	// Uncommitted change → not finished.
	if err := os.WriteFile(filepath.Join(wt, "f2.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if workflowStepFinished(task) {
		t.Error("dirty worktree should NOT be finished")
	}

	// Empty worktree path → not finished.
	if workflowStepFinished(&db.Task{WorktreePath: ""}) {
		t.Error("empty worktree path should NOT be finished")
	}
}
