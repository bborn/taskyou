package executor

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs a git command in dir and fails the test on error.
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
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
}

// initRepoWithRemote creates a repo with a bare "origin" it can push to.
func initRepoWithRemote(t *testing.T) (repo string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", bare)
	repo = filepath.Join(root, "repo")
	gitIn(t, root, "init", "-q", "-b", "main", repo)
	gitIn(t, repo, "config", "user.email", "test@example.com")
	gitIn(t, repo, "config", "user.name", "Test")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, repo, "remote", "add", "origin", bare)
	gitIn(t, repo, "push", "-q", "origin", "main")
	return repo
}

// headBranch returns the checked-out branch of a worktree, or "HEAD" if detached.
func headBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse in %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}

// THE REGRESSION TEST. With BOTH a local branch and origin/<branch> present,
// `git worktree add <path> origin/<branch>` silently produces a DETACHED HEAD:
// git will not move an existing branch, so it checks out the remote ref instead.
// A detached step still runs and commits, but its commits never land on the
// shared branch, so the next step sees none of the work. Observed in production
// on the Lumen pipeline, where every step worktree was detached and the shared
// branch never advanced past its base commit.
func TestSourceBranchWorktreeAttachesWhenLocalAndRemoteBothExist(t *testing.T) {
	repo := initRepoWithRemote(t)
	gitIn(t, repo, "branch", "shared")
	gitIn(t, repo, "push", "-q", "origin", "shared")
	gitIn(t, repo, "fetch", "-q", "origin")

	e := &Executor{}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := e.addSourceBranchWorktree(repo, wt, "shared"); err != nil {
		t.Fatalf("addSourceBranchWorktree: %v", err)
	}

	if got := headBranch(t, wt); got != "shared" {
		t.Fatalf("worktree HEAD = %q, want branch %q — a detached worktree throws its commits away", got, "shared")
	}
}

// A commit made in the worktree must actually advance the shared branch — the
// property the pipeline depends on to hand work forward.
func TestSourceBranchWorktreeCommitsLandOnSharedBranch(t *testing.T) {
	repo := initRepoWithRemote(t)
	gitIn(t, repo, "branch", "shared")
	gitIn(t, repo, "push", "-q", "origin", "shared")
	gitIn(t, repo, "fetch", "-q", "origin")

	e := &Executor{}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := e.addSourceBranchWorktree(repo, wt, "shared"); err != nil {
		t.Fatalf("addSourceBranchWorktree: %v", err)
	}

	gitIn(t, wt, "config", "user.email", "test@example.com")
	gitIn(t, wt, "config", "user.name", "Test")
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "step output")

	// The shared branch in the main repo must now point at that commit.
	wtHead := strings.TrimSpace(gitIn(t, wt, "rev-parse", "HEAD"))
	branchHead := strings.TrimSpace(gitIn(t, repo, "rev-parse", "shared"))
	if wtHead != branchHead {
		t.Fatalf("shared branch is at %s but the step committed %s — the next step would not see this work", branchHead[:7], wtHead[:7])
	}
}

// Only a local branch (the document-first pipeline case from #668): the shared
// branch is built locally and never pushed, so origin/<branch> does not exist.
func TestSourceBranchWorktreeAttachesToLocalOnlyBranch(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	gitIn(t, repo, "branch", "pipeline/1-local-only")

	e := &Executor{}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := e.addSourceBranchWorktree(repo, wt, "pipeline/1-local-only"); err != nil {
		t.Fatalf("addSourceBranchWorktree: %v", err)
	}
	if got := headBranch(t, wt); got != "pipeline/1-local-only" {
		t.Fatalf("worktree HEAD = %q, want the local branch", got)
	}
}

// Only origin has the branch: the local branch must be created AND attached,
// not checked out detached.
func TestSourceBranchWorktreeCreatesLocalBranchFromRemoteOnly(t *testing.T) {
	repo := initRepoWithRemote(t)
	gitIn(t, repo, "branch", "remote-only")
	gitIn(t, repo, "push", "-q", "origin", "remote-only")
	gitIn(t, repo, "fetch", "-q", "origin")
	// Delete the local branch so ONLY origin/remote-only remains.
	gitIn(t, repo, "branch", "-D", "remote-only")

	e := &Executor{}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := e.addSourceBranchWorktree(repo, wt, "remote-only"); err != nil {
		t.Fatalf("addSourceBranchWorktree: %v", err)
	}
	if got := headBranch(t, wt); got != "remote-only" {
		t.Fatalf("worktree HEAD = %q, want branch remote-only", got)
	}
}

// A local branch strictly behind origin is fast-forwarded, so the step starts
// from the newest pushed work (preserving the old prefer-origin behaviour).
func TestSourceBranchWorktreeFastForwardsBehindLocalBranch(t *testing.T) {
	repo := initRepoWithRemote(t)
	gitIn(t, repo, "branch", "shared")
	gitIn(t, repo, "push", "-q", "origin", "shared")

	// Advance origin/shared beyond the local branch via a second clone.
	otherRoot := t.TempDir()
	other := filepath.Join(otherRoot, "other")
	remote := strings.TrimSpace(gitIn(t, repo, "remote", "get-url", "origin"))
	gitIn(t, otherRoot, "clone", "-q", remote, other)
	gitIn(t, other, "config", "user.email", "test@example.com")
	gitIn(t, other, "config", "user.name", "Test")
	gitIn(t, other, "checkout", "-q", "shared")
	gitIn(t, other, "commit", "-q", "--allow-empty", "-m", "pushed elsewhere")
	gitIn(t, other, "push", "-q", "origin", "shared")

	gitIn(t, repo, "fetch", "-q", "origin")
	ahead := strings.TrimSpace(gitIn(t, repo, "rev-parse", "origin/shared"))

	e := &Executor{}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := e.addSourceBranchWorktree(repo, wt, "shared"); err != nil {
		t.Fatalf("addSourceBranchWorktree: %v", err)
	}
	if got := headBranch(t, wt); got != "shared" {
		t.Fatalf("worktree HEAD = %q, want branch shared", got)
	}
	if got := strings.TrimSpace(gitIn(t, wt, "rev-parse", "HEAD")); got != ahead {
		t.Errorf("worktree at %s, want fast-forwarded to origin %s", got[:7], ahead[:7])
	}
}

// A DIVERGED local branch must NOT be force-moved — it carries this workflow's
// own commits, and discarding them would lose a completed step's work.
func TestSourceBranchWorktreeDoesNotClobberDivergedLocalBranch(t *testing.T) {
	repo := initRepoWithRemote(t)
	gitIn(t, repo, "branch", "shared")
	gitIn(t, repo, "push", "-q", "origin", "shared")

	// origin/shared gains a commit...
	otherRoot := t.TempDir()
	other := filepath.Join(otherRoot, "other")
	remote := strings.TrimSpace(gitIn(t, repo, "remote", "get-url", "origin"))
	gitIn(t, otherRoot, "clone", "-q", remote, other)
	gitIn(t, other, "config", "user.email", "test@example.com")
	gitIn(t, other, "config", "user.name", "Test")
	gitIn(t, other, "checkout", "-q", "shared")
	gitIn(t, other, "commit", "-q", "--allow-empty", "-m", "theirs")
	gitIn(t, other, "push", "-q", "origin", "shared")
	gitIn(t, repo, "fetch", "-q", "origin")

	// ...while the local branch gains a DIFFERENT one (this workflow's work).
	tmpWt := filepath.Join(t.TempDir(), "seed")
	gitIn(t, repo, "worktree", "add", "-q", tmpWt, "shared")
	gitIn(t, tmpWt, "config", "user.email", "test@example.com")
	gitIn(t, tmpWt, "config", "user.name", "Test")
	gitIn(t, tmpWt, "commit", "-q", "--allow-empty", "-m", "ours")
	ours := strings.TrimSpace(gitIn(t, tmpWt, "rev-parse", "HEAD"))
	gitIn(t, repo, "worktree", "remove", "--force", tmpWt)

	e := &Executor{}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := e.addSourceBranchWorktree(repo, wt, "shared"); err != nil {
		t.Fatalf("addSourceBranchWorktree: %v", err)
	}
	if got := strings.TrimSpace(gitIn(t, repo, "rev-parse", "shared")); got != ours {
		t.Fatalf("diverged local branch moved to %s, want it left at our commit %s", got[:7], ours[:7])
	}
}

func TestSourceBranchWorktreeErrorsWhenBranchMissingEverywhere(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)

	e := &Executor{}
	wt := filepath.Join(t.TempDir(), "wt")
	err := e.addSourceBranchWorktree(repo, wt, "pipeline/does-not-exist")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}
