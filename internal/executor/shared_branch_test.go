package executor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// sharedBranchRepo builds a real git repo with one commit on a shared pipeline
// branch. These tests are about what git actually permits (one worktree per
// branch), so a fake would prove nothing.
func sharedBranchRepo(t *testing.T) (repo, branch string) {
	t.Helper()
	repo = t.TempDir()
	branch = "pipeline/1-demo"
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "base")
	run("branch", branch)
	return repo, branch
}

func sharedBranchExecutor(t *testing.T, repo string) (*Executor, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "p", Path: repo}); err != nil {
		t.Fatal(err)
	}
	return New(database, &config.Config{}), database
}

// holdBranch gives an existing task a worktree attached to branch, the way a
// step that has run leaves things behind.
func holdBranch(t *testing.T, e *Executor, database *db.DB, repo, branch, status string) *db.Task {
	t.Helper()
	holderPath := filepath.Join(t.TempDir(), "holder")
	cmd := exec.Command("git", "worktree", "add", holderPath, branch)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed holder worktree: %v\n%s", err, out)
	}
	task := &db.Task{Title: "[Plan] x", Status: status, Project: "p", Tags: "pipeline", BranchName: branch}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	task.WorktreePath = holderPath
	task.Status = status
	if err := database.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskStatus(task.ID, status); err != nil {
		t.Fatal(err)
	}
	return task
}

// The bug this whole change exists for: a pipeline's first step finishes, its
// worktree keeps the shared branch checked out, and the NEXT step can never
// attach — "fatal: '<branch>' is already checked out at ...". A finished holder
// must hand the branch over.
func TestSourceBranchWorktreeReclaimsBranchFromFinishedStep(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holder := holdBranch(t, e, database, repo, branch, db.StatusDone)

	next := filepath.Join(t.TempDir(), "next")
	if err := e.addSourceBranchWorktree(repo, next, branch); err != nil {
		t.Fatalf("next step could not take the shared branch: %v", err)
	}

	got, err := gitCurrentBranch(next)
	if err != nil {
		t.Fatal(err)
	}
	if got != branch {
		t.Fatalf("next step worktree is on %q, want it attached to %q", got, branch)
	}
	// The finished step keeps its files; only its HEAD moved off the branch.
	if _, err := os.Stat(filepath.Join(holder.WorktreePath, "README.md")); err != nil {
		t.Fatalf("finished step's worktree should stay readable: %v", err)
	}
	if head, err := gitCurrentBranch(holder.WorktreePath); err != nil || head != "HEAD" {
		t.Fatalf("holder should be detached, got %q (err %v)", head, err)
	}
}

// A holder that is still RUNNING keeps the branch, and the waiting step must be
// told to come back later rather than being failed. A hard failure here is what
// stamped started_at/completed_at on a step that never launched an agent.
func TestSourceBranchWorktreeDefersWhileHolderIsRunning(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holdBranch(t, e, database, repo, branch, db.StatusProcessing)

	err := e.addSourceBranchWorktree(repo, filepath.Join(t.TempDir(), "next"), branch)
	if !errors.Is(err, ErrBranchBusy) {
		t.Fatalf("want ErrBranchBusy while the holder runs, got %v", err)
	}
}

// Fan-out: every sibling gets a worktree on its OWN branch, cut from the shared
// branch, and none of them contends with the others or with the root's worktree.
func TestStepBranchWorktreesDoNotContend(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, database := sharedBranchExecutor(t, repo)
	holdBranch(t, e, database, repo, branch, db.StatusProcessing) // root still running

	for _, name := range []string{"planreviewa", "planreviewb", "planreviewc"} {
		stepBranch := branch + "-" + name
		path := filepath.Join(t.TempDir(), name)
		if err := e.addStepBranchWorktree(repo, path, stepBranch, branch); err != nil {
			t.Fatalf("%s could not get its own branch: %v", name, err)
		}
		got, err := gitCurrentBranch(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != stepBranch {
			t.Fatalf("%s is on %q, want %q", name, got, stepBranch)
		}
	}
}

// A retried fan-out step reuses its own branch, keeping whatever it committed
// the first time rather than re-cutting from the shared branch.
func TestStepBranchWorktreeReusesExistingBranchOnRetry(t *testing.T) {
	repo, branch := sharedBranchRepo(t)
	e, _ := sharedBranchExecutor(t, repo)
	stepBranch := branch + "-planreviewa"

	first := filepath.Join(t.TempDir(), "first")
	if err := e.addStepBranchWorktree(repo, first, stepBranch, branch); err != nil {
		t.Fatal(err)
	}
	// The step commits, then its worktree goes away (crash, cleanup, retry).
	writeAndCommit(t, first, "REVIEW.md", "findings")
	want := gitHeadCommit(first)
	rm := exec.Command("git", "worktree", "remove", "--force", first)
	rm.Dir = repo
	if out, err := rm.CombinedOutput(); err != nil {
		t.Fatalf("remove worktree: %v\n%s", err, out)
	}

	second := filepath.Join(t.TempDir(), "second")
	if err := e.addStepBranchWorktree(repo, second, stepBranch, branch); err != nil {
		t.Fatalf("retry could not reattach: %v", err)
	}
	if got := gitHeadCommit(second); got != want {
		t.Fatalf("retry started at %s, want the step's own commit %s", got, want)
	}
}

func writeAndCommit(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", name}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
