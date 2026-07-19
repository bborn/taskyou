package executor

import (
	"os/exec"
	"testing"
)

// git runs a git command in dir and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// initRepo creates a git repo with one commit on a `main` branch.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.email", "test@example.com")
	gitIn(t, dir, "config", "user.name", "Test")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "initial")
}

func TestResolveSourceBranchRef(t *testing.T) {
	e := &Executor{}

	t.Run("falls back to a local-only branch (document-first pipeline)", func(t *testing.T) {
		dir := t.TempDir()
		initRepo(t, dir)
		// Shared pipeline branch exists locally but was never pushed to origin.
		gitIn(t, dir, "branch", "pipeline/4887-shared")

		ref, err := e.resolveSourceBranchRef(dir, "pipeline/4887-shared")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ref != "pipeline/4887-shared" {
			t.Errorf("ref = %q, want local branch %q", ref, "pipeline/4887-shared")
		}
	})

	t.Run("prefers origin/<branch> when the remote-tracking ref exists", func(t *testing.T) {
		remote := t.TempDir()
		gitIn(t, remote, "init", "-q", "--bare", "-b", "main")

		dir := t.TempDir()
		initRepo(t, dir)
		gitIn(t, dir, "remote", "add", "origin", remote)
		// Push a branch and establish the remote-tracking ref, then also keep a
		// local branch of the same name so both refs are resolvable.
		gitIn(t, dir, "branch", "feature/pushed")
		gitIn(t, dir, "push", "-q", "origin", "feature/pushed")
		gitIn(t, dir, "fetch", "-q", "origin")

		ref, err := e.resolveSourceBranchRef(dir, "feature/pushed")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ref != "origin/feature/pushed" {
			t.Errorf("ref = %q, want remote-tracking ref %q", ref, "origin/feature/pushed")
		}
	})

	t.Run("errors when the branch exists nowhere", func(t *testing.T) {
		dir := t.TempDir()
		initRepo(t, dir)

		if _, err := e.resolveSourceBranchRef(dir, "pipeline/does-not-exist"); err == nil {
			t.Fatal("expected an error for a branch that exists neither on origin nor locally")
		}
	})
}
