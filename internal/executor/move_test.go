package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// carryRepo builds a real origin + working clone on disk. The carry gate is the
// only thing standing between a move and lost work, so it is tested against git
// itself rather than a fake that agrees with it.
func carryRepo(t *testing.T) (work string, origin string) {
	t.Helper()
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	work = filepath.Join(root, "work")

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
	run(root, "clone", origin, work)
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "add", "-A")
	run(work, "commit", "-m", "seed")
	run(work, "push", "-u", "origin", "main")
	run(work, "checkout", "-b", "task/5277")
	return work, origin
}

func localSource(dir string) WorkSource {
	return WorkSource{Runner: LocalRunner{}, WorkDir: dir}
}

// The whole promise: an edit the agent never committed is on the target host's
// branch after a move.
func TestCarryWorkCarriesUncommittedWork(t *testing.T) {
	work, origin := carryRepo(t)
	if err := os.WriteFile(filepath.Join(work, "feature.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := CarryWork(context.Background(), localSource(work), "handoff body", "bruce")
	if err != nil {
		t.Fatalf("CarryWork: %v", err)
	}
	if !rep.WIPCommit {
		t.Error("uncommitted work was carried without reporting that a commit was made")
	}
	if rep.Branch != "task/5277" {
		t.Errorf("Branch = %q, want task/5277", rep.Branch)
	}

	// Read it back out of ORIGIN, which is the only copy the target host can see.
	out, err := exec.Command("git", "--git-dir", origin, "show", "task/5277:feature.go").CombinedOutput()
	if err != nil {
		t.Fatalf("the uncommitted file never reached origin: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "package x" {
		t.Errorf("origin has %q, want the file as it was on the source host", got)
	}

	hand, err := exec.Command("git", "--git-dir", origin, "show", "task/5277:"+HandoffPath).CombinedOutput()
	if err != nil {
		t.Fatalf("the handoff never reached origin: %v\n%s", err, hand)
	}
	if !strings.Contains(string(hand), "handoff body") {
		t.Errorf("handoff on origin = %q", hand)
	}
}

// A worktree with nothing to commit still has to end up pushed; "clean" is not
// the same as "the target host can see it".
func TestCarryWorkPushesAnAlreadyCommittedBranch(t *testing.T) {
	work, origin := carryRepo(t)
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", "work")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=ty", "GIT_AUTHOR_EMAIL=ty@e.com",
		"GIT_COMMITTER_NAME=ty", "GIT_COMMITTER_EMAIL=ty@e.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	rep, err := CarryWork(context.Background(), localSource(work), "", "bruce")
	if err != nil {
		t.Fatalf("CarryWork: %v", err)
	}
	if rep.WIPCommit {
		t.Error("reported a WIP commit for a clean worktree")
	}
	if out, err := exec.Command("git", "--git-dir", origin, "rev-parse", "task/5277").CombinedOutput(); err != nil {
		t.Fatalf("branch not on origin: %v\n%s", err, out)
	}
}

// Ignored files do not travel. Discovering that as an absence on the far side,
// hours later, is the failure this prevents.
func TestCarryWorkNamesWhatItLeavesBehind(t *testing.T) {
	work, _ := carryRepo(t)
	if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".env"), []byte("SECRET=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := CarryWork(context.Background(), localSource(work), "", "bruce")
	if err != nil {
		t.Fatalf("CarryWork: %v", err)
	}
	var found bool
	for _, f := range rep.LeftBehind {
		if strings.Contains(f, ".env") {
			found = true
		}
	}
	if !found {
		t.Errorf("LeftBehind = %v, want it to name .env, which does not move", rep.LeftBehind)
	}
}

// A detached worktree is how ty has lost work before: commits land on no branch,
// the push carries nothing, and the target clones a branch that never saw them.
func TestCarryWorkRefusesADetachedWorktree(t *testing.T) {
	work, _ := carryRepo(t)
	cmd := exec.Command("git", "checkout", "--detach")
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	_, err := CarryWork(context.Background(), localSource(work), "", "bruce")
	if err == nil {
		t.Fatal("carried work off a detached HEAD; the commits would belong to no branch")
	}
	if !strings.Contains(err.Error(), "detached") {
		t.Errorf("error does not explain the detached HEAD: %v", err)
	}
}

// The gate: if the work cannot be proven to have reached origin, the move must
// fail. Here origin is gone, so the push cannot succeed.
func TestCarryWorkFailsWhenTheWorkCannotReachOrigin(t *testing.T) {
	work, origin := carryRepo(t)
	if err := os.WriteFile(filepath.Join(work, "feature.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}

	_, err := CarryWork(context.Background(), localSource(work), "", "bruce")
	if err == nil {
		t.Fatal("reported success with no reachable origin; the move would have dropped the work")
	}
}

func TestCarryWorkRefusesWithoutAWorktree(t *testing.T) {
	if _, err := CarryWork(context.Background(), WorkSource{Runner: LocalRunner{}}, "", "bruce"); err == nil {
		t.Fatal("carried work from a task that has no worktree")
	}
}

// The gate itself, isolated: a branch that is committed but not pushed is work
// that exists only on the host we are about to stop using. verifyCarried is the
// only thing that notices, so it is tested without the push that normally
// precedes it.
func TestVerifyCarriedRejectsAnUnpushedBranch(t *testing.T) {
	work, _ := carryRepo(t)
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", "local only")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=ty", "GIT_AUTHOR_EMAIL=ty@e.com",
		"GIT_COMMITTER_NAME=ty", "GIT_COMMITTER_EMAIL=ty@e.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	err := localSource(work).verifyCarried(context.Background(), "task/5277")
	if err == nil {
		t.Fatal("verifyCarried passed a branch that is not on origin; the target host could not fetch it")
	}
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("error does not say the branch is missing from origin: %v", err)
	}
}

// The other half of the gate: everything pushed, but an edit still sitting in
// the worktree. That is a move that leaves work behind.
func TestVerifyCarriedRejectsADirtyWorktree(t *testing.T) {
	work, _ := carryRepo(t)
	if _, err := CarryWork(context.Background(), localSource(work), "", "bruce"); err != nil {
		t.Fatalf("setup carry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "late.go"), []byte("package late\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := localSource(work).verifyCarried(context.Background(), "task/5277")
	if err == nil {
		t.Fatal("verifyCarried passed a dirty worktree; the move would leave the edit behind")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("error does not name the uncommitted work: %v", err)
	}
}
