package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/executor"
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

// headSHA returns the worktree's current HEAD commit.
func headSHA(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestWorkflowStepFinished: a step counts as finished only when it PRODUCED A COMMIT
// (HEAD moved off the worktree's base commit) and pushed it, with a clean worktree.
//
// The base-commit check is load-bearing: a freshly-created step worktree is clean and
// its HEAD (the parent step's commit) is already reachable from origin. Reporting that
// as "finished" let the daemon sweep mark steps done before their agent ever started,
// silently dropping Build/Review stages and shipping unreviewed code.
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

	// The parent step's commit — already pushed. This is where a new step's worktree
	// starts, and it is the step's base commit.
	if err := os.WriteFile(filepath.Join(wt, "parent.txt"), []byte("parent"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-m", "parent step work")
	git(t, wt, "push", "origin", "HEAD:pipeline/shared")
	base := headSHA(t, wt)

	// THE REGRESSION: a step that has produced nothing sits at its base commit with a
	// clean worktree, and that commit IS on origin. It must NOT read as finished.
	if executor.WorkflowStepFinished(wt, base) {
		t.Error("a step sitting at its base commit (no work) must NOT be finished — this silently drops unstarted steps")
	}

	// Committed but NOT pushed → not finished.
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-m", "work")
	if executor.WorkflowStepFinished(wt, base) {
		t.Error("committed but unpushed step should NOT be finished")
	}

	// Pushed WITHOUT -u (no upstream tracking, like a real step) → finished.
	git(t, wt, "push", "origin", "HEAD:pipeline/shared")
	if _, err := exec.Command("git", "-C", wt, "rev-parse", "@{u}").Output(); err == nil {
		t.Fatal("test precondition failed: expected NO upstream tracking after push without -u")
	}
	if !executor.WorkflowStepFinished(wt, base) {
		t.Error("clean + committed + pushed step SHOULD be finished")
	}

	// DETACHED HEAD → still finished. Non-root steps share one branch and git won't
	// attach two worktrees to it, so those worktrees run detached.
	git(t, wt, "checkout", "--detach", "HEAD")
	if out, err := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "HEAD").Output(); err != nil || strings.TrimSpace(string(out)) != "HEAD" {
		t.Fatalf("test precondition failed: expected a detached HEAD, got %q (%v)", strings.TrimSpace(string(out)), err)
	}
	if !executor.WorkflowStepFinished(wt, base) {
		t.Error("clean + committed + pushed step on a DETACHED HEAD SHOULD be finished")
	}

	// Uncommitted change → not finished.
	if err := os.WriteFile(filepath.Join(wt, "f2.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if executor.WorkflowStepFinished(wt, base) {
		t.Error("dirty worktree should NOT be finished")
	}

	// No recorded base commit → no evidence of work → never finished.
	if executor.WorkflowStepFinished(wt, "") {
		t.Error("a step with no recorded base commit must NOT be finished")
	}
	// Empty worktree path → not finished.
	if executor.WorkflowStepFinished("", base) {
		t.Error("empty worktree path should NOT be finished")
	}
}
