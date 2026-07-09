package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// clean AND its HEAD is pushed. Critically, non-root workflow steps check out the
// shared branch WITHOUT upstream tracking (no `-u`), so the check must use the
// remote-tracking ref, not @{u}.
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
	// Mimic a workflow step: work on a shared branch, no upstream tracking.
	git(t, wt, "checkout", "-b", "pipeline/shared")

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

	// Pushed WITHOUT -u (no upstream tracking, like a real step) → finished.
	git(t, wt, "push", "origin", "HEAD:pipeline/shared")
	if _, err := exec.Command("git", "-C", wt, "rev-parse", "@{u}").Output(); err == nil {
		t.Fatal("test precondition failed: expected NO upstream tracking after push without -u")
	}
	if !workflowStepFinished(task) {
		t.Error("clean + pushed step (no upstream tracking) SHOULD be finished")
	}

	// DETACHED HEAD → still finished. Non-root steps share one branch and git won't
	// attach two worktrees to it, so those worktrees run detached. HEAD is still the
	// pushed commit, so the step IS finished — the old --abbrev-ref check saw "HEAD"
	// and wrongly reported unfinished forever, stalling the whole DAG.
	git(t, wt, "checkout", "--detach", "HEAD")
	if out, err := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "HEAD").Output(); err != nil || strings.TrimSpace(string(out)) != "HEAD" {
		t.Fatalf("test precondition failed: expected a detached HEAD, got %q (%v)", strings.TrimSpace(string(out)), err)
	}
	if !workflowStepFinished(task) {
		t.Error("clean + pushed step on a DETACHED HEAD SHOULD be finished")
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
