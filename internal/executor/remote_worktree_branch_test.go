package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scriptRepo builds an origin plus a checkout, and puts a branch carrying real
// work on the origin ONLY — no local ref for it in the checkout. That is exactly
// what a host sees when a task is moved onto it: the work arrived by push, and
// the only copy it can reach is origin's.
func scriptRepo(t *testing.T, branch string) (repo string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo = filepath.Join(root, "checkout")
	src := filepath.Join(root, "src")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=ty", "GIT_AUTHOR_EMAIL=ty@example.com",
			"GIT_COMMITTER_NAME=ty", "GIT_COMMITTER_EMAIL=ty@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
	}

	run(root, "init", "--bare", "--initial-branch=main", origin)
	run(root, "clone", origin, src)
	if err := os.WriteFile(filepath.Join(src, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", "-A")
	run(src, "commit", "-m", "seed")
	run(src, "push", "-u", "origin", "main")
	run(src, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(src, "findings.md"), []byte("the work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", "-A")
	run(src, "commit", "-m", "carried work")
	run(src, "push", "-u", "origin", branch)

	run(root, "clone", origin, repo)
	return repo
}

// The remote landing used to ask only "is there a LOCAL branch by this name?"
// and, finding none, cut a fresh one from origin/main. The provisioning
// succeeded, the task started, and the carried work was simply not there — the
// carry gate proves the work reached origin and nothing then went to get it.
func TestRemoteWorktreeScriptChecksOutTheCarriedBranchFromOrigin(t *testing.T) {
	branch := "task/9-carried"
	repo := scriptRepo(t, branch)

	cmd := exec.Command("sh", "-c", remoteWorktreeScript(repo, "9-carried", branch))
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	wt := filepath.Join(repo, ".task-worktrees", "9-carried")
	head, err := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("no worktree at %s: %v", wt, err)
	}
	if got := strings.TrimSpace(string(head)); got != branch {
		t.Errorf("worktree is on %q, want the carried branch %q", got, branch)
	}
	if _, err := os.Stat(filepath.Join(wt, "findings.md")); err != nil {
		t.Errorf("the carried work is not in the worktree, so the move lost it: %v", err)
	}
}

// A branch nobody has ever pushed is a task that has not run yet, and that
// still has to start — from the default branch, as before.
func TestRemoteWorktreeScriptStartsFreshWhenTheBranchIsNew(t *testing.T) {
	repo := scriptRepo(t, "task/9-carried")

	cmd := exec.Command("sh", "-c", remoteWorktreeScript(repo, "10-brand-new", "task/10-brand-new"))
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	wt := filepath.Join(repo, ".task-worktrees", "10-brand-new")
	head, _ := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if got := strings.TrimSpace(string(head)); got != "task/10-brand-new" {
		t.Errorf("worktree is on %q, want a fresh task/10-brand-new", got)
	}
	if _, err := os.Stat(filepath.Join(wt, "findings.md")); err == nil {
		t.Error("a brand new branch was cut from the carried branch instead of the default one")
	}
}
