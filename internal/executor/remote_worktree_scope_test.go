package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// These tests pin the fix for the remote worktree branch-derivation bug:
// downstream pipeline phases routed to a remote host used to be provisioned
// from origin/main (because setupRemoteWorktree never consulted
// task.SourceBranch). They now mirror the LOCAL path: a sequential phase
// attaches to the shared branch its predecessor pushed; a fan-out peer cuts its
// step branch from that shared branch. LOCAL parity lives in
// addSourceBranchWorktree / addStepBranchWorktree — these tests are the remote
// counterparts to worktree_source_branch_test.go and shared_branch_test.go.

// remoteScriptRepo builds an origin plus a checkout that has fetched the
// shared branch but does NOT have a LOCAL ref for it. That is what a host
// primary clone looks like after a predecessor pushed the shared branch: the
// only copy of the work the host can reach is origin's.
//
// root/origin.git  — the bare origin
// root/checkout     — a host's primary clone (default branch checked out)
// root/seed.txt     — a sentinel file the predecessor committed on <shared>
//
// The seed is committed on <shared> only — never on main — so a downstream
// phase that starts from origin/main cannot POSSIBLY contain it. Asserting the
// seed is PRESENT after provisioning is therefore a deterministic check that
// the worktree came from the shared branch.
func remoteScriptRepo(t *testing.T, shared string) (origin, repo string) {
	t.Helper()
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	repo = filepath.Join(root, "checkout")
	src := filepath.Join(root, "src")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
	}

	run(root, "init", "--bare", "--initial-branch=main", origin)
	run(root, "clone", origin, src)
	if err := os.WriteFile(filepath.Join(src, "base"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", "-A")
	run(src, "commit", "-m", "seed")
	run(src, "push", "-u", "origin", "main")

	// Predecessor creates and pushes the SHARED branch with its phase1 work.
	run(src, "checkout", "-b", shared)
	if err := os.WriteFile(filepath.Join(src, "phase1.txt"), []byte("phase 1 output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", "-A")
	run(src, "commit", "-m", "phase 1")
	run(src, "push", "-u", "origin", shared)
	run(src, "checkout", "main")

	// Host: a fresh clone — no LOCAL ref for the shared branch, only origin's
	// remote-tracking one.
	run(root, "clone", origin, repo)
	return origin, repo
}

// gitEnv is the environment used by every git the tests shell out to, matching
// the existing remote_worktree_branch_test.go discipline. GIT_CONFIG_GLOBAL
// and GIT_CONFIG_SYSTEM point at /dev/null so a host's ~/.gitconfig cannot
// influence git's defaults — the suite must pin stock defaults so the
// upstream-tracking behaviour the assertions depend on is reproducible.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=ty", "GIT_AUTHOR_EMAIL=ty@example.com",
		"GIT_COMMITTER_NAME=ty", "GIT_COMMITTER_EMAIL=ty@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
}

// runScript runs the rendered provisioning script in the same environment the
// executor hands to ssh, returning its combined output (the failure case is
// verbose enough on its own — git's chatter is on stderr, the script's answer
// is on stdout, so stderr is wired up too).
func runScript(t *testing.T, script string) []byte {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	return out
}

// worktreeBranch returns the branch checked out in the worktree at wt.
func worktreeBranch(t *testing.T, wt string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD in %s: %v", wt, err)
	}
	return strings.TrimSpace(string(out))
}

// worktreeConfig reads a single git config value from a worktree.
func worktreeConfig(t *testing.T, wt, key string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", wt, "config", key)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// scriptForTask renders the provisioning script the same way setupRemoteWorktree
// would for a task with the given fields — exercising the routing the bug fix
// adds so the assertions stay close to the real call site.
func scriptForTask(t *testing.T, task *db.Task, repo string) string {
	t.Helper()
	slug := slugify(task.Title, 40)
	dirName := fmt.Sprintf("%d-%s", task.ID, slug)
	branch, mode, source := remoteWorktreePlan(task, slug)
	return remoteWorktreeScript(repo, dirName, branch, mode, source)
}

// ============ Sequential phase: attaches to the shared branch ============

// TestRemoteWorktreeScriptAttachesToSharedBranchForSequentialPhase is the
// flipped version of TestRemoteWorktreeScriptSilentlyDropsPredecessorFor*
// (the bug). A sequential downstream phase (BranchName=="", SourceBranch=<shared>)
// must attach its worktree to the shared branch already pushed by the
// predecessor — NOT cut a fresh per-task branch from origin/main.
//
// Asserted facts (each one inverts the bug):
//   - the rendered script references the shared branch and never falls back to
//     origin/main (no base/ fallback clause);
//   - the worktree is on the shared branch, not task/<id>-<slug>;
//   - the predecessor's phase1.txt is PRESENT (the bug said ABSENT);
//   - the worktree's upstream tracks origin/<shared> (the bug said
//     refs/heads/main), which is what makes the prompt's later `git push` land
//     the step's commits on the shared branch instead of failing with a
//     name-mismatch error.
func TestRemoteWorktreeScriptAttachesToSharedBranchForSequentialPhase(t *testing.T) {
	const shared = "pipeline/1-payload"
	_, repo := remoteScriptRepo(t, shared)

	task := &db.Task{ID: 2, Title: "phase two", SourceBranch: shared}
	script := scriptForTask(t, task, repo)

	if !strings.Contains(script, "branch='pipeline/1-payload'") {
		t.Errorf("script does not pass the shared branch as $branch:\n%s", script)
	}
	if strings.Contains(script, "refs/remotes/origin/task/2-phase-two") {
		t.Errorf("script still derives a per-task branch name:\n%s", script)
	}
	if strings.Contains(script, "base=\"\"") || strings.Contains(script, "origin/$base") {
		t.Errorf("script still has the origin/main fallback a sequential phase must not use:\n%s", script)
	}

	wt := filepath.Join(repo, ".task-worktrees", "2-phase-two")
	runScript(t, script)

	if got := worktreeBranch(t, wt); got != shared {
		t.Errorf("worktree is on %q, want the shared branch %q", got, shared)
	}
	if _, err := os.Stat(filepath.Join(wt, "phase1.txt")); err != nil {
		t.Errorf("predecessor's phase1.txt is ABSENT — the worktree did not attach to the shared branch: %v", err)
	}
	if got, want := worktreeConfig(t, wt, "branch."+shared+".remote"), "origin"; got != want {
		t.Errorf("branch.%s.remote = %q, want %q (the bug tracked origin/main)", shared, got, want)
	}
	if got, want := worktreeConfig(t, wt, "branch."+shared+".merge"), "refs/heads/"+shared; got != want {
		t.Errorf("branch.%s.merge = %q, want %q (the bug tracked refs/heads/main)", shared, got, want)
	}
}

// TestRemoteWorktreeScriptCutsStepBranchFromSharedForFanOutPeer is the flipped
// version of the fan-out peer bug. A fan-out peer (BranchName=<shared>-<slug>,
// SourceBranch=<shared>) must cut its own step branch from origin/<shared> —
// NOT from origin/main.
//
// Asserted facts (each inverts the bug):
//   - the rendered script names origin/$source as the cut point, not origin/main;
//   - --no-track is used, mirroring the local addStepBranchWorktree (a step that
//     tracked the shared branch could `git push` its work onto it);
//   - the worktree is on the peer's own step branch, not the shared branch;
//   - the predecessor's phase1.txt is PRESENT (the bug said ABSENT — the peer
//     was cut from origin/main);
//   - the step branch has NO upstream tracking (verified by absence), so a bare
//     `git push` from this peer asks `--set-upstream origin <stepBranch>` rather
//     than silently pushing to the shared branch.
func TestRemoteWorktreeScriptCutsStepBranchFromSharedForFanOutPeer(t *testing.T) {
	const shared = "pipeline/1-payload"
	const step = "pipeline/1-payload-review-a"
	_, repo := remoteScriptRepo(t, shared)

	task := &db.Task{ID: 3, Title: "review a", BranchName: step, SourceBranch: shared}
	script := scriptForTask(t, task, repo)

	if !strings.Contains(script, "branch='pipeline/1-payload-review-a'") {
		t.Errorf("script does not pass the step branch as $branch:\n%s", script)
	}
	if !strings.Contains(script, "source='pipeline/1-payload'") {
		t.Errorf("script does not pass the shared branch as $source:\n%s", script)
	}
	if !strings.Contains(script, "refs/remotes/origin/$source") {
		t.Errorf("script does not cut from origin/$source:\n%s", script)
	}
	if !strings.Contains(script, "--no-track") {
		t.Errorf("script does not pass --no-track (the step branch would track the shared branch):\n%s", script)
	}
	if strings.Contains(script, "base=\"\"") || strings.Contains(script, "origin/$base") {
		t.Errorf("script still has the origin/main fallback a fan-out peer must not use:\n%s", script)
	}

	wt := filepath.Join(repo, ".task-worktrees", "3-review-a")
	runScript(t, script)

	if got := worktreeBranch(t, wt); got != step {
		t.Errorf("worktree is on %q, want the peer's step branch %q", got, step)
	}
	if _, err := os.Stat(filepath.Join(wt, "phase1.txt")); err != nil {
		t.Errorf("predecessor's phase1.txt is ABSENT — the step branch was cut from origin/main, not the shared branch: %v", err)
	}
	if got := worktreeConfig(t, wt, "branch."+step+".remote"); got != "" {
		t.Errorf("branch.%s.remote = %q, want empty (--no-track must not set upstream)", step, got)
	}
	if got := worktreeConfig(t, wt, "branch."+step+".merge"); got != "" {
		t.Errorf("branch.%s.merge = %q, want empty (--no-track must not set upstream)", step, got)
	}
}

// ============ Routing table: sequential / fan-out / carry-move / standalone ============

// TestRemoteWorktreePlanRoutesByTaskShape pins the routing table the fix adds.
// Each row is the same task shape the LOCAL path sees (executor.go:5252-5262
// decides addStepBranchWorktree vs. addSourceBranchWorktree) — the remote path
// must now route the same shapes the same way.
func TestRemoteWorktreePlanRoutesByTaskShape(t *testing.T) {
	cases := []struct {
		name        string
		task        *db.Task
		wantBranch  string
		wantMode    remoteWorktreeMode
		wantSource  string
		wantHasBase bool // true when the script must contain the origin/main fallback
	}{
		{
			name:        "sequential downstream: BranchName empty, SourceBranch set",
			task:        &db.Task{ID: 2, Title: "code", BranchName: "", SourceBranch: "pipeline/1-x"},
			wantBranch:  "pipeline/1-x",
			wantMode:    modeAttach,
			wantSource:  "pipeline/1-x",
			wantHasBase: false,
		},
		{
			name:        "fan-out peer: BranchName set and differs from SourceBranch",
			task:        &db.Task{ID: 3, Title: "review a", BranchName: "pipeline/1-x-review-a", SourceBranch: "pipeline/1-x"},
			wantBranch:  "pipeline/1-x-review-a",
			wantMode:    modeStepBranch,
			wantSource:  "pipeline/1-x",
			wantHasBase: false,
		},
		{
			name:        "carry-move: BranchName == SourceBranch",
			task:        &db.Task{ID: 4, Title: "carried", BranchName: "task/4-carried", SourceBranch: "task/4-carried"},
			wantBranch:  "task/4-carried",
			wantMode:    modeFresh,
			wantSource:  "",
			wantHasBase: true,
		},
		{
			name:        "pipeline root: BranchName set, SourceBranch empty (the only row pinned by the pipeline builder)",
			task:        &db.Task{ID: 1, Title: "plan", BranchName: "pipeline/1-x", SourceBranch: ""},
			wantBranch:  "pipeline/1-x",
			wantMode:    modeFresh,
			wantSource:  "",
			wantHasBase: true,
		},
		{
			name:        "standalone: neither BranchName nor SourceBranch",
			task:        &db.Task{ID: 5, Title: "fix typo"},
			wantBranch:  "task/5-fix-typo",
			wantMode:    modeFresh,
			wantSource:  "",
			wantHasBase: true,
		},
		{
			name:        "BranchName whitespace-only is ignored (treat as empty BranchName)",
			task:        &db.Task{ID: 6, Title: "phase two", BranchName: "   ", SourceBranch: "pipeline/1-x"},
			wantBranch:  "pipeline/1-x",
			wantMode:    modeAttach,
			wantSource:  "pipeline/1-x",
			wantHasBase: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slug := slugify(tc.task.Title, 40)
			branch, mode, source := remoteWorktreePlan(tc.task, slug)
			if branch != tc.wantBranch {
				t.Errorf("branch = %q, want %q", branch, tc.wantBranch)
			}
			if mode != tc.wantMode {
				t.Errorf("mode = %v, want %v", mode, tc.wantMode)
			}
			if source != tc.wantSource {
				t.Errorf("source = %q, want %q", source, tc.wantSource)
			}
			script := remoteWorktreeScript("/repo", "dir", branch, mode, source)
			hasBase := strings.Contains(script, "base=")
			if hasBase != tc.wantHasBase {
				t.Errorf("script has origin/main fallback = %v, want %v\n%s", hasBase, tc.wantHasBase, script)
			}
		})
	}
}

// ============ Errors when the shared branch is missing everywhere ============

// A sequential phase whose shared branch does not exist on origin OR locally
// must ERROR — not fall back to origin/main (the bug). The bug silently
// provisioned a worktree with no predecessor work; the fix refuses instead.
func TestRemoteWorktreeScriptErrorsWhenSharedBranchMissingSequential(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "checkout")
	src := filepath.Join(root, "src")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
	}
	run(root, "init", "--bare", "--initial-branch=main", origin)
	run(root, "clone", origin, src)
	if err := os.WriteFile(filepath.Join(src, "base"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", "-A")
	run(src, "commit", "-m", "seed")
	run(src, "push", "-u", "origin", "main")
	run(root, "clone", origin, repo)

	// A shared branch the predecessor never pushed. The script must refuse to
	// cut a fresh branch from main.
	const missing = "pipeline/never-pushed"
	task := &db.Task{ID: 2, Title: "phase two", SourceBranch: missing}
	script := scriptForTask(t, task, repo)

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script succeeded; want a failure when the shared branch is missing.\noutput:\n%s", out)
	}
	if !strings.Contains(string(out), missing) {
		t.Errorf("failure message does not name the missing shared branch %q:\n%s", missing, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".task-worktrees", "2-phase-two")); err == nil {
		t.Error("a worktree was created despite the missing shared branch — the fallback to main was not removed")
	}
}

// A fan-out peer whose shared branch does not exist anywhere must also refuse,
// for the same reason: the bug would have cut the step branch from origin/main.
func TestRemoteWorktreeScriptErrorsWhenSharedBranchMissingFanOut(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "checkout")
	src := filepath.Join(root, "src")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
	}
	run(root, "init", "--bare", "--initial-branch=main", origin)
	run(root, "clone", origin, src)
	if err := os.WriteFile(filepath.Join(src, "base"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(src, "add", "-A")
	run(src, "commit", "-m", "seed")
	run(src, "push", "-u", "origin", "main")
	run(root, "clone", origin, repo)

	const missing = "pipeline/never-pushed"
	const step = "pipeline/never-pushed-review-a"
	task := &db.Task{ID: 3, Title: "review a", BranchName: step, SourceBranch: missing}
	script := scriptForTask(t, task, repo)

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script succeeded; want a failure when the shared branch is missing.\noutput:\n%s", out)
	}
	if !strings.Contains(string(out), missing) {
		t.Errorf("failure message does not name the missing shared branch %q:\n%s", missing, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".task-worktrees", "3-review-a")); err == nil {
		t.Error("a worktree was created despite the missing shared branch")
	}
}

// ============ Predecessor commits are visible to the next phase ============

// TestRemoteWorktreeSeesPredecessorCommitsOnSharedBranch mirrors
// TestSourceBranchWorktreeReclaimsBranchFromFinishedStep (the local-path test)
// for the remote path. After a finished prior step leaves the shared branch on
// origin (and a leftover LOCAL ref analogous to Plan's worktree dir being
// removed): the next sequential phase and a fan-out peer each provision, and
// asserting each new phase sees the predecessor's commits on the shared branch
// rather than starting from origin/main, and (for the sequential phase) that
// the upstream tracks origin/<shared>.
//
// This is the only "after a finished prior step" test the bug report asks for,
// asserted end-to-end against real git.
func TestRemoteWorktreeSeesPredecessorCommitsOnSharedBranch(t *testing.T) {
	const shared = "pipeline/1-seen-by-next"
	origin, repo := remoteScriptRepo(t, shared)

	// Simulate "the prior step's worktree was removed when it finished": leave a
	// LOCAL ref for the shared branch (the residue a `git worktree remove`
	// leaves behind for a branch it created via `worktree add -b`). The point
	// is to exercise BOTH case 1 (local ref exists) and case 2 (only origin
	// has it) for the sequential phase, without another worktree holding the
	// branch. We do that by introducing the local ref into a SEPARATE clone.
	// The main test asserts case 2 against `repo`; the sub-test below it
	// asserts case 1 against a clone that has the local ref.
	t.Run("sequential", func(t *testing.T) {
		task := &db.Task{ID: 2, Title: "phase two", SourceBranch: shared}
		script := scriptForTask(t, task, repo)
		wt := filepath.Join(repo, ".task-worktrees", "2-phase-two")
		runScript(t, script)

		if got := worktreeBranch(t, wt); got != shared {
			t.Fatalf("worktree is on %q, want %q", got, shared)
		}
		if _, err := os.Stat(filepath.Join(wt, "phase1.txt")); err != nil {
			t.Fatalf("phase1.txt ABSENT: %v", err)
		}
		if got, want := worktreeConfig(t, wt, "branch."+shared+".remote"), "origin"; got != want {
			t.Errorf("branch.%s.remote = %q, want %q", shared, got, want)
		}
		if got, want := worktreeConfig(t, wt, "branch."+shared+".merge"), "refs/heads/"+shared; got != want {
			t.Errorf("branch.%s.merge = %q, want %q", shared, got, want)
		}
	})

	// Case 1 path: the host has a LOCAL reference for the shared branch
	// (left behind when a prior step's worktree was removed) but no worktree
	// holds it. The sequential phase must reuse that local ref and end up on
	// the shared branch with the predecessor's work present.
	t.Run("sequential reuses a leftover local ref", func(t *testing.T) {
		_, repo3 := remoteScriptRepo(t, shared)
		// Create the local ref manually so we don't depend on a worktree being
		// removed; this is what `worktree remove` leaves behind on `repo`.
		cmd := exec.Command("git", "-C", repo3, "branch", shared, "origin/"+shared)
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("branch shared: %v\n%s", err, out)
		}

		task := &db.Task{ID: 2, Title: "phase two", SourceBranch: shared}
		script := scriptForTask(t, task, repo3)
		wt := filepath.Join(repo3, ".task-worktrees", "2-phase-two")
		runScript(t, script)

		if got := worktreeBranch(t, wt); got != shared {
			t.Fatalf("worktree is on %q, want %q", got, shared)
		}
		if _, err := os.Stat(filepath.Join(wt, "phase1.txt")); err != nil {
			t.Errorf("phase1.txt ABSENT: %v", err)
		}
	})

	t.Run("fan-out peer", func(t *testing.T) {
		const step = shared + "-review-a"
		task := &db.Task{ID: 3, Title: "review a", BranchName: step, SourceBranch: shared}
		script := scriptForTask(t, task, repo)
		wt := filepath.Join(repo, ".task-worktrees", "3-review-a")
		runScript(t, script)

		if got := worktreeBranch(t, wt); got != step {
			t.Fatalf("worktree is on %q, want step branch %q", got, step)
		}
		if _, err := os.Stat(filepath.Join(wt, "phase1.txt")); err != nil {
			t.Errorf("phase1.txt ABSENT: %v", err)
		}
		// --no-track: a peer that pushes to its own step branch must never
		// land commits on the shared branch by accident.
		if got := worktreeConfig(t, wt, "branch."+step+".remote"); got != "" {
			t.Errorf("branch.%s.remote = %q, want empty", step, got)
		}
		if got := worktreeConfig(t, wt, "branch."+step+".merge"); got != "" {
			t.Errorf("branch.%s.merge = %q, want empty", step, got)
		}
	})

	// Defensive: assert the bare origin was actually seeded correctly so a
	// silent setup bug cannot pass these tests by accident (a missing
	// phase1.txt on the shared branch would make every "phase1.txt PRESENT"
	// assertion vacuous).
	if out, err := exec.Command("git", "-C", origin, "show", shared+":phase1.txt").Output(); err != nil || !strings.Contains(string(out), "phase 1 output") {
		t.Fatalf("fixture sanity: origin/%s:phase1.txt missing or malformed: %v\n%s", shared, err, out)
	}
}

// TestRemoteWorktreeReclaimsBranchFromFinishedStep is the closest analogue of
// the local path's TestSourceBranchWorktreeReclaimsBranchFromFinishedStep
// (shared_branch_test.go:86). A pipeline's prior step finishes, and its
// worktree keeps holding the shared branch (because teardown-with-completion
// is a separate, later sweep — at the moment the next step spawns the prior
// step's worktree is still on disk, still readable, and still on the shared
// branch). The next sequential phase must take the shared branch off the
// holder, and the next fan-out peer must cut its step branch from the shared
// branch — neither may fail with "is already checked out at ...", and neither
// may fall back to origin/main.
//
// What "take" means here is the local-path's
// releaseBranchFromFinishedHolder: detach the holder's worktree HEAD (its
// commits stay on the branch; its working files stay on disk; only its HEAD
// moves off the branch ref, so git allows another worktree to attach). The
// remote script has no daemon-side "is this holder's task finished?" check;
// it conservatively detaches ONLY holders whose path is under
// $repo/.task-worktrees/ — every worktree ty ever creates remotely lives
// there, and the spawn path only reaches a downstream phase after the
// workflow advanced past a finished predecessor, so a holder there is a
// finished predecessor's worktree by construction.
//
// A holder that is NOT under .task-worktrees/ must NOT be silently detached
// — it could be a human's checkout, the host's primary clone, anything we
// cannot prove is a finished task's worktree. The script must refuse instead,
// which the second sub-test verifies.
func TestRemoteWorktreeReclaimsBranchFromFinishedStep(t *testing.T) {
	const shared = "pipeline/1-reclaimed"
	_, repo := remoteScriptRepo(t, shared)

	// Build a finished predecessor's worktree on the shared branch — what a
	// real Plan step leaves behind. This is exactly the local-path fixture's
	// `holdBranch(t, e, database, repo, branch, db.StatusDone)` (modulo the
	// daemon state, which the remote path deliberately does not consult).
	holder := filepath.Join(repo, ".task-worktrees", "1-plan")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", holder, shared).CombinedOutput(); err != nil {
		t.Fatalf("seed predecessor worktree: %v\n%s", err, out)
	}
	if got := worktreeBranch(t, holder); got != shared {
		t.Fatalf("fixture: holder is on %q, want %q", got, shared)
	}

	t.Run("sequential reclaims the shared branch from the finished holder", func(t *testing.T) {
		task := &db.Task{ID: 2, Title: "phase two", SourceBranch: shared}
		script := scriptForTask(t, task, repo)
		wt := filepath.Join(repo, ".task-worktrees", "2-phase-two")
		runScript(t, script)

		if got := worktreeBranch(t, wt); got != shared {
			t.Fatalf("next step worktree is on %q, want the shared branch %q", got, shared)
		}
		// The finished step keeps its files; only its HEAD moved off the
		// branch, exactly as releaseBranchFromFinishedHolder promises.
		if _, err := os.Stat(filepath.Join(holder, "phase1.txt")); err != nil {
			t.Fatalf("predecessor's worktree should stay readable: %v", err)
		}
		if got, err := gitCurrentBranchSkipErr(holder); err == nil && got != "HEAD" {
			t.Fatalf("holder should be detached, got %q", got)
		}
		if _, err := os.Stat(filepath.Join(wt, "phase1.txt")); err != nil {
			t.Fatalf("next step's worktree should see the predecessor's phase1.txt: %v", err)
		}
		if got, want := worktreeConfig(t, wt, "branch."+shared+".remote"), "origin"; got != want {
			t.Errorf("branch.%s.remote = %q, want %q", shared, got, want)
		}
		if got, want := worktreeConfig(t, wt, "branch."+shared+".merge"), "refs/heads/"+shared; got != want {
			t.Errorf("branch.%s.merge = %q, want %q", shared, got, want)
		}
	})

	t.Run("fan-out peer cuts from the shared branch held by a finished holder", func(t *testing.T) {
		// Use a FRESH repo so the previous sub-test's worktree does not keep
		// holding the shared branch — each sub-test wants a clean fixture.
		const step = shared + "-review-a"
		_, repoF := remoteScriptRepo(t, shared)
		holderF := filepath.Join(repoF, ".task-worktrees", "1-plan")
		if out, err := exec.Command("git", "-C", repoF, "worktree", "add", holderF, shared).CombinedOutput(); err != nil {
			t.Fatalf("seed holder worktree: %v\n%s", err, out)
		}

		task := &db.Task{ID: 3, Title: "review a", BranchName: step, SourceBranch: shared}
		script := scriptForTask(t, task, repoF)
		wt := filepath.Join(repoF, ".task-worktrees", "3-review-a")
		runScript(t, script)

		if got := worktreeBranch(t, wt); got != step {
			t.Fatalf("peer worktree is on %q, want step branch %q", got, step)
		}
		if _, err := os.Stat(filepath.Join(wt, "phase1.txt")); err != nil {
			t.Fatalf("peer should see the predecessor's phase1.txt: %v", err)
		}
		if got := worktreeConfig(t, wt, "branch."+step+".remote"); got != "" {
			t.Errorf("branch.%s.remote = %q, want empty (--no-track)", step, got)
		}
		// The fan-out cut does not need the holder detached — only attaching
		// to the SAME branch as the holder does. The holder stays put.
		if got := worktreeBranch(t, holderF); got != shared {
			t.Errorf("fan-out provisioning touched a holder it should not: holder is now on %q", got)
		}
	})

	t.Run("refuses to detach a holder that is not under .task-worktrees/", func(t *testing.T) {
		_, repo2 := remoteScriptRepo(t, shared)
		// A "primary checkout" the human left on the shared branch — the
		// script must NOT detach this; doing so would silently move someone
		// else's HEAD off the branch they parked it on.
		other := filepath.Join(repo2, "human-checkout")
		if out, err := exec.Command("git", "-C", repo2, "worktree", "add", other, shared).CombinedOutput(); err != nil {
			t.Fatalf("seed human holder: %v\n%s", err, out)
		}

		task := &db.Task{ID: 2, Title: "phase two", SourceBranch: shared}
		script := scriptForTask(t, task, repo2)
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = gitEnv()
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("script succeeded; want a refusal when the holder is not a ty worktree.\noutput:\n%s", out)
		}
		if !strings.Contains(string(out), "not a ty worktree") && !strings.Contains(string(out), "refusing to detach") {
			t.Errorf("failure message does not name the refusal:\n%s", out)
		}
		// The human holder must be untouched.
		if got := worktreeBranch(t, other); got != shared {
			t.Errorf("human holder was detached despite the refusal: now on %q", got)
		}
	})
}

// gitCurrentBranchSkipErr returns the branch checked out at dir, or "HEAD"
// when detached. It tolerates an error to keep a test concise.
func gitCurrentBranchSkipErr(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TestRemoteWorktreeScriptFastForwardsStaleLocalRefToOrigin pins the cross-host
// sequential-revisit bug: a host that ran an earlier phase of a multi-host
// sequential pipeline carries a stale local refs/heads/<shared> at that earlier
// phase's pushed tip (created by the phase's case-2 attach and advanced by its
// in-worktree commit). When another host later advances origin/<shared> and
// the revisiting host runs a later sequential phase, the modeAttach case-1 arm
// used to attach to refs/heads/$branch VERBATIM — without fast-forwarding it to
// origin/<branch> first — so the new worktree started at the stale tip and the
// immediately-preceding phase's content was absent. The fix mirrors
// addSourceBranchWorktree's fast-forward guard (executor.go:6421-6429): when
// origin/$branch exists and the local ref is a STRICT ancestor of it (never the
// reverse), advance the local ref before attaching — so a local ref that
// carries this workflow's own unpushed commits is never force-moved over them.
//
// The "behind origin" sub-test seeds the behind-origin configuration the
// existing suite never exercised (every existing case-1 test seeds the local
// ref AT origin, where the missing fast-forward is a no-op), keeps a
// predecessor's worktree on the branch (the production design keeps finished
// worktrees) so the holder-detach path also runs, then renders and runs
// remoteWorktreeScript(modeAttach) and asserts the worktree starts at
// origin/<shared> with the immediately-preceding phase's content present. The
// "ahead of origin" sub-test is the regression guard for the strict-ancestor
// guard: a local ref with unpushed commits beyond origin must be preserved
// (the local path's "never when the two have diverged" promise).
func TestRemoteWorktreeScriptFastForwardsStaleLocalRefToOrigin(t *testing.T) {
	const shared = "pipeline/1-revisit"

	t.Run("behind origin fast-forwards to origin tip", func(t *testing.T) {
		origin, repo := remoteScriptRepo(t, shared)
		// remoteScriptRepo leaves origin/<shared> at "phase 1" (P1) with the
		// host clone carrying only an origin remote-tracking ref for it.

		gitIn := func(dir string, args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			cmd.Env = gitEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
			}
		}
		revParse := func(dir, ref string) string {
			t.Helper()
			out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
			if err != nil {
				t.Fatalf("rev-parse %s in %s: %v", ref, dir, err)
			}
			return strings.TrimSpace(string(out))
		}
		isAncestor := func(ancestor, descendant string) bool {
			cmd := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", ancestor, descendant)
			cmd.Env = gitEnv()
			return cmd.Run() == nil
		}

		// P1 ran on THIS host: case-2 attach created refs/heads/<shared> at
		// origin's current tip (P1) and a worktree holding the branch. The
		// production design keeps the finished step's worktree
		// (remote_worktree.go:225-227), so it keeps <shared> checked out —
		// the holder-detach paragraph must run before the case-1 attach.
		p1wt := filepath.Join(repo, ".task-worktrees", "1-plan")
		gitIn(repo, "worktree", "add", "-b", shared, p1wt, "origin/"+shared)

		// Another host advances origin/<shared> with the immediately-preceding
		// phase's work (P2). A second clone of the same bare origin moves
		// origin/<shared> past the local ref on this host.
		root := filepath.Dir(repo)
		hostB := filepath.Join(root, "hostB")
		gitIn(root, "clone", "-q", origin, hostB)
		gitIn(hostB, "checkout", "-q", shared)
		if err := os.WriteFile(filepath.Join(hostB, "OUT2.md"), []byte("phase 2 output\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(hostB, "add", "-A")
		gitIn(hostB, "commit", "-q", "-m", "phase 2")
		gitIn(hostB, "push", "-q", "origin", "HEAD:"+shared)

		// The revisiting host fetches: refs/remotes/origin/<shared> advances
		// to P2, refs/heads/<shared> stays at P1 (fetch never moves local
		// heads). This is the behind-origin config the fix targets.
		gitIn(repo, "fetch", "-q", "origin")
		localTip := revParse(repo, "refs/heads/"+shared)
		originTip := revParse(repo, "refs/remotes/origin/"+shared)
		if localTip == originTip {
			t.Fatalf("fixture: local ref == origin tip; the behind-origin config was not produced")
		}
		if !isAncestor(localTip, originTip) {
			t.Fatalf("fixture: local ref %s is not an ancestor of origin tip %s", localTip, originTip)
		}

		// Render remoteWorktreeScript(modeAttach) — NOT bare `git worktree add`
		// — so the holder-detach + fast-forward + attach path all execute.
		task := &db.Task{ID: 3, Title: "phase three", SourceBranch: shared}
		script := scriptForTask(t, task, repo)
		p3wt := filepath.Join(repo, ".task-worktrees", "3-phase-three")
		runScript(t, script)

		// (a) HEAD == origin/<shared>: the worktree started at the
		// immediately-preceding phase's tip, not the stale local tip.
		if got := revParse(p3wt, "HEAD"); got != originTip {
			t.Errorf("worktree HEAD = %s, want origin tip %s (attached to stale local ref)", got, originTip)
		}
		// (b) OUT2.md present — the immediately-preceding phase's content. The
		// current bug drops this: it attaches at P1, before OUT2.md existed.
		if _, err := os.Stat(filepath.Join(p3wt, "OUT2.md")); err != nil {
			t.Errorf("immediately-preceding phase's OUT2.md is ABSENT: %v", err)
		}
		// phase1.txt (P1's file, still in history) must also be present, so the
		// OUT2.md check is not vacuous.
		if _, err := os.Stat(filepath.Join(p3wt, "phase1.txt")); err != nil {
			t.Errorf("predecessor phase1.txt is ABSENT: %v", err)
		}
		// (c) The worktree is on refs/heads/<shared>, not detached — the
		// fast-forward advanced the local ref, then `worktree add <wt> <branch>`
		// attached to it (a detached HEAD would mean the agent's later
		// `git push` lands its commits nowhere).
		symOut, err := exec.Command("git", "-C", p3wt, "symbolic-ref", "HEAD").Output()
		if err != nil {
			t.Fatalf("symbolic-ref HEAD in worktree: %v", err)
		}
		if got, want := strings.TrimSpace(string(symOut)), "refs/heads/"+shared; got != want {
			t.Errorf("worktree HEAD symbolic-ref = %q, want %q (fast-forward must not detach)", got, want)
		}
		// (d) The local ref was actually advanced to origin's tip — a later
		// revisit on this same host must not re-bite.
		if got := revParse(repo, "refs/heads/"+shared); got != originTip {
			t.Errorf("local ref refs/heads/%s = %s, want origin tip %s (was not fast-forwarded)", shared, got, originTip)
		}
	})

	t.Run("ahead of origin preserves local commits", func(t *testing.T) {
		_, repo := remoteScriptRepo(t, shared)
		// remoteScriptRepo: origin/<shared> at P1, host clone with origin ref only.

		gitIn := func(dir string, args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			cmd.Env = gitEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
			}
		}
		revParse := func(dir, ref string) string {
			t.Helper()
			out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
			if err != nil {
				t.Fatalf("rev-parse %s in %s: %v", ref, dir, err)
			}
			return strings.TrimSpace(string(out))
		}
		isAncestor := func(ancestor, descendant string) bool {
			cmd := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", ancestor, descendant)
			cmd.Env = gitEnv()
			return cmd.Run() == nil
		}

		// The host ran P1: case-2 attach created the local ref at P1 and a
		// worktree holding it.
		p1wt := filepath.Join(repo, ".task-worktrees", "1-plan")
		gitIn(repo, "worktree", "add", "-b", shared, p1wt, "origin/"+shared)
		// The previous phase committed its work (advancing refs/heads/<shared>
		// beyond origin) but the push was silently rejected / never landed —
		// the "local side carries this workflow's own commits" case the local
		// path's guard explicitly protects. Do NOT push: origin/<shared>
		// stays at P1 while refs/heads/<shared> moves to P1+extra.
		if err := os.WriteFile(filepath.Join(p1wt, "EXTRA.md"), []byte("unpushed local work\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(p1wt, "add", "-A")
		gitIn(p1wt, "commit", "-q", "-m", "unpushed local commit")
		gitIn(repo, "fetch", "-q", "origin")

		localTip := revParse(repo, "refs/heads/"+shared)
		originTip := revParse(repo, "refs/remotes/origin/"+shared)
		if localTip == originTip {
			t.Fatalf("fixture: local ref == origin tip; the ahead-of-origin config was not produced")
		}
		// local is NOT an ancestor of origin — origin is an ancestor of local.
		if isAncestor(localTip, originTip) {
			t.Fatalf("fixture: local ref %s IS an ancestor of origin tip %s (expected strictly ahead)", localTip, originTip)
		}

		task := &db.Task{ID: 2, Title: "phase two", SourceBranch: shared}
		script := scriptForTask(t, task, repo)
		wt := filepath.Join(repo, ".task-worktrees", "2-phase-two")
		runScript(t, script)

		// The strict-ancestor guard must NOT fast-forward: the worktree
		// attaches to the local tip (P1+extra), preserving the unpushed
		// commit. Force-moving to origin would have discarded EXTRA.md.
		if got := revParse(wt, "HEAD"); got != localTip {
			t.Errorf("worktree HEAD = %s, want local tip %s (strict-ancestor guard should not fast-forward an ahead local ref)", got, localTip)
		}
		if _, err := os.Stat(filepath.Join(wt, "EXTRA.md")); err != nil {
			t.Errorf("unpushed local commit's EXTRA.md is ABSENT (guard force-moved the local ref over it): %v", err)
		}
		if got := revParse(repo, "refs/heads/"+shared); got != localTip {
			t.Errorf("local ref refs/heads/%s = %s, want unchanged %s (guard must not move an ahead local ref)", shared, got, localTip)
		}
	})

	t.Run("diverged from origin preserves local commits", func(t *testing.T) {
		// Belt-and-braces for the strict-ancestor guard's diverged branch
		// (G4): when local and origin have EACH advanced with commits the
		// other doesn't have (neither is an ancestor of the other), the guard
		// must NOT fast-forward — force-moving the local ref to origin would
		// discard the local-only commits. The "ahead" sub-test already
		// exercises the same `gitIsAncestor(local, origin)==false` guard
		// branch; this sub-test makes the divergence explicit by also
		// advancing origin past the common base.
		origin, repo := remoteScriptRepo(t, shared)

		gitIn := func(dir string, args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			cmd.Env = gitEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
			}
		}
		revParse := func(dir, ref string) string {
			t.Helper()
			out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
			if err != nil {
				t.Fatalf("rev-parse %s in %s: %v", ref, dir, err)
			}
			return strings.TrimSpace(string(out))
		}
		isAncestor := func(ancestor, descendant string) bool {
			cmd := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", ancestor, descendant)
			cmd.Env = gitEnv()
			return cmd.Run() == nil
		}

		// Local side: host ran P1 (case-2 attach at P1) and committed an
		// unpushed local-only commit on top.
		p1wt := filepath.Join(repo, ".task-worktrees", "1-plan")
		gitIn(repo, "worktree", "add", "-b", shared, p1wt, "origin/"+shared)
		if err := os.WriteFile(filepath.Join(p1wt, "LOCAL.md"), []byte("local-only\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(p1wt, "add", "-A")
		gitIn(p1wt, "commit", "-q", "-m", "local-only commit")

		// Origin side: a different host advances origin/<shared> with a
		// commit the local side does NOT have (origin-only commit). Now
		// local and origin have diverged from the P1 base.
		root := filepath.Dir(repo)
		hostB := filepath.Join(root, "hostB-div")
		gitIn(root, "clone", "-q", origin, hostB)
		gitIn(hostB, "checkout", "-q", shared)
		if err := os.WriteFile(filepath.Join(hostB, "REMOTE.md"), []byte("origin-only\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(hostB, "add", "-A")
		gitIn(hostB, "commit", "-q", "-m", "origin-only commit")
		gitIn(hostB, "push", "-q", "origin", "HEAD:"+shared)

		gitIn(repo, "fetch", "-q", "origin")
		localTip := revParse(repo, "refs/heads/"+shared)
		originTip := revParse(repo, "refs/remotes/origin/"+shared)
		if localTip == originTip {
			t.Fatalf("fixture: local ref == origin tip; divergence was not produced")
		}
		if isAncestor(localTip, originTip) {
			t.Fatalf("fixture: local IS an ancestor of origin (expected diverged): %s < %s", localTip, originTip)
		}
		if isAncestor(originTip, localTip) {
			t.Fatalf("fixture: origin IS an ancestor of local (expected diverged): %s < %s", originTip, localTip)
		}

		task := &db.Task{ID: 2, Title: "phase two", SourceBranch: shared}
		script := scriptForTask(t, task, repo)
		wt := filepath.Join(repo, ".task-worktrees", "2-phase-two")
		runScript(t, script)

		// Guard must NOT fast-forward: the worktree attaches to the local
		// tip, preserving the local-only commit. LOCAL.md stays present.
		if got := revParse(wt, "HEAD"); got != localTip {
			t.Errorf("worktree HEAD = %s, want local tip %s (guard must not fast-forward a diverged local ref)", got, localTip)
		}
		if _, err := os.Stat(filepath.Join(wt, "LOCAL.md")); err != nil {
			t.Errorf("local-only commit's LOCAL.md is ABSENT (guard force-moved the diverged local ref over it): %v", err)
		}
		if got := revParse(repo, "refs/heads/"+shared); got != localTip {
			t.Errorf("local ref refs/heads/%s = %s, want unchanged %s (guard must not move a diverged local ref)", shared, got, localTip)
		}
	})
}

// ============ setupRemoteWorktree: end-to-end against a stub ssh ============

// TestSetupRemoteWorktreeHonorsSourceBranchSequentialAndFanOut is the
// flipped TestSetupRemoteWorktreeIgnoresSourceBranchSequentialAndFanOut. It
// stubs ssh to capture the args, calls e.setupRemoteWorktree for sequential
// and fan-out tasks, and asserts each lands on the right branch with the
// script attaching to (or cutting from) the shared branch.
//
// The bug asserted the per-task `task/2-phase-two` branch and that the script
// never mentioned `pipeline/1-x`; the fix asserts the OPPOSITE: the shared
// branch is the worktree's branch for the sequential phase, and the script
// does mention `pipeline/1-x` (as `refs/remotes/origin/$branch` for sequential,
// `refs/remotes/origin/$source` for fan-out).
func TestSetupRemoteWorktreeHonorsSourceBranchSequentialAndFanOut(t *testing.T) {
	const shared = "pipeline/1-x"
	const fanStep = "pipeline/1-x-review-a"

	for _, tc := range []struct {
		name       string
		task       *db.Task
		wantBranch string
		// contains and excludes are matched against the args captured from
		// ssh. The "shared branch name" reaches the script as the value of
		// the $branch (sequential) or $source (fan-out) shell variable; it
		// survives shell quoting as a plain substring, so a substring check
		// is enough. Excludes verify the per-task branch name the BUG would
		// have used is NOT present.
		contains string
		excludes string
	}{
		{
			name:       "sequential",
			task:       &db.Task{ID: 2, Title: "phase two", SourceBranch: shared},
			wantBranch: shared,
			contains:   shared,
			excludes:   "task/2-phase-two",
		},
		{
			name:       "fan-out peer",
			task:       &db.Task{ID: 3, Title: "review a", BranchName: fanStep, SourceBranch: shared},
			wantBranch: fanStep,
			contains:   "source=",
			excludes:   "task/3-review-a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capturePath := stubSSHCapture(t)

			e := &Executor{}
			wt, err := e.setupRemoteWorktree(context.Background(), tc.task, RemoteRunner{Host: "mona", WorkDir: "/repo"})
			if err != nil {
				t.Fatalf("setupRemoteWorktree: %v", err)
			}
			if wt.Branch != tc.wantBranch {
				t.Errorf("wt.Branch = %q, want %q", wt.Branch, tc.wantBranch)
			}
			script, err := os.ReadFile(capturePath)
			if err != nil {
				t.Fatalf("could not read captured ssh args: %v", err)
			}
			s := string(script)
			if !strings.Contains(s, tc.contains) {
				t.Errorf("script does not contain %q in the captured args:\n%s", tc.contains, script)
			}
			if strings.Contains(s, tc.excludes) {
				t.Errorf("script still references the per-task branch %q the bug derived:\n%s", tc.excludes, script)
			}
			// And no origin/main fallback for downstream phases — the bug's
			// case 3 ("cut a fresh branch from origin/main") is the silent
			// predecessor-drop the fix removes for sequential and fan-out.
			if strings.Contains(s, "base=") {
				t.Errorf("downstream phase script still has the origin/main fallback it must not use:\n%s", script)
			}
		})
	}
}

// TestSetupRemoteWorktreeCarryMoveKeepsLegacyScript is the regression guard for
// the carry-move path the bug report explicitly says must stay "unchanged".
// A carry-move arrives with BranchName == SourceBranch == rep.Branch, so the
// routing must hand it modeFresh and use newWorktreeBranchName (which returns
// BranchName). The script must still have the origin/main fallback (case 3).
func TestSetupRemoteWorktreeCarryMoveKeepsLegacyScript(t *testing.T) {
	const carried = "task/9-carried"
	task := &db.Task{ID: 9, Title: "carried task", BranchName: carried, SourceBranch: carried}
	slug := slugify(task.Title, 40)
	branch, mode, source := remoteWorktreePlan(task, slug)
	if branch != carried {
		t.Errorf("branch = %q, want %q", branch, carried)
	}
	if mode != modeFresh {
		t.Errorf("mode = %v, want modeFresh", mode)
	}
	if source != "" {
		t.Errorf("source = %q, want empty", source)
	}

	capturePath := stubSSHCapture(t)
	e := &Executor{}
	wt, err := e.setupRemoteWorktree(context.Background(), task, RemoteRunner{Host: "mona", WorkDir: "/repo"})
	if err != nil {
		t.Fatalf("setupRemoteWorktree: %v", err)
	}
	if wt.Branch != carried {
		t.Errorf("wt.Branch = %q, want %q", wt.Branch, carried)
	}
	script, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("could not read captured ssh args: %v", err)
	}
	// The carry-move path must keep ALL three legacy cases: local $branch
	// (case 1), origin/$branch (case 2 — the one the move itself depends on),
	// and the fall-through to $base (case 3 — what the carry gate proved was
	// safe only when origin/$branch is missing). The `$branch` here is the
	// literal shell variable (expanded at the remote host, not in these
	// captured args).
	if !strings.Contains(string(script), "base=") {
		t.Errorf("carry-move script lost the origin/main fallback (case 3):\n%s", script)
	}
	if !strings.Contains(string(script), "refs/remotes/origin/$branch") {
		t.Errorf("carry-move script lost the origin/$branch attach case (case 2):\n%s", script)
	}
}

// TestSetupRemoteWorktreeStandaloneKeepsLegacyScript is the regression guard
// for a standalone task: nothing for the bug to fix here, and the script must
// keep its three cases including the origin/main fallback (case 3).
func TestSetupRemoteWorktreeStandaloneKeepsLegacyScript(t *testing.T) {
	task := &db.Task{ID: 5, Title: "fix typo"}
	slug := slugify(task.Title, 40)
	branch, mode, source := remoteWorktreePlan(task, slug)
	if want := "task/5-fix-typo"; branch != want {
		t.Errorf("branch = %q, want %q", branch, want)
	}
	if mode != modeFresh {
		t.Errorf("mode = %v, want modeFresh", mode)
	}
	if source != "" {
		t.Errorf("source = %q, want empty (standalone task has no source branch)", source)
	}

	capturePath := stubSSHCapture(t)
	e := &Executor{}
	wt, err := e.setupRemoteWorktree(context.Background(), task, RemoteRunner{Host: "mona", WorkDir: "/repo"})
	if err != nil {
		t.Fatalf("setupRemoteWorktree: %v", err)
	}
	if want := "task/5-fix-typo"; wt.Branch != want {
		t.Errorf("wt.Branch = %q, want %q", wt.Branch, want)
	}
	script, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("could not read captured ssh args: %v", err)
	}
	if !strings.Contains(string(script), "base=") {
		t.Errorf("standalone script lost the origin/main fallback:\n%s", script)
	}
}

// stubSSHCapture installs a stub ssh that writes every received argument to a
// capture file (one line per arg), so a test can inspect what
// setupRemoteWorktree actually sent across the wire. The stub exits 0 with an
// "answer" the parser accepts, which is what makes setupRemoteWorktree return
// a non-error result the assertions can read. The returned path is the capture
// file; read it from inside the test body (not from t.Cleanup) to see the args
// sent before the test ended.
func stubSSHCapture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "args")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\n" +
		"for a in \"$@\"; do echo \"$a\"; done >>" + path + "\n" +
		"echo 'created /repo/.task-worktrees/2-phase-two'\n"
	stubSSH(t, body)
	return path
}
