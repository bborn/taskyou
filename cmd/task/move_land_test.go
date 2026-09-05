package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// landRepo builds an origin + a project clone, registers the clone as project
// "land", and returns the clone's path. The landing is tested against real git
// for the same reason the carry is: it is the half that decides whether the
// carried work is found or silently replaced by a fresh branch off main.
func landRepo(t *testing.T, database *db.DB) (project string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	project = filepath.Join(root, "project")

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
	run(root, "clone", origin, project)
	if err := os.WriteFile(filepath.Join(project, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(project, "add", "-A")
	run(project, "commit", "-m", "seed")
	run(project, "push", "-u", "origin", "main")

	if err := database.CreateProject(&db.Project{Name: "land", Path: project, UseWorktrees: true}); err != nil {
		t.Fatal(err)
	}
	return project
}

// gitRun is the test's own git, kept away from the user's global config.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=ty", "GIT_AUTHOR_EMAIL=ty@example.com",
		"GIT_COMMITTER_NAME=ty", "GIT_COMMITTER_EMAIL=ty@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// carriedTask is a task in the state a move leaves behind: its work is on a
// branch on origin, and it has no worktree on this machine.
func carriedTask(t *testing.T, database *db.DB, project string) (*db.Task, string) {
	t.Helper()
	task := &db.Task{Title: "carried work", Type: "task", Project: "land"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	branch := "task/" + strconv.FormatInt(task.ID, 10) + "-carried-work"

	// Build the branch the way the source host would have, and push it. The
	// project clone must NOT keep a local ref for it: on the arriving machine the
	// carried work exists only on origin, which is the case that used to be missed.
	src := filepath.Join(t.TempDir(), "src")
	gitRun(t, filepath.Dir(src), "clone", filepath.Join(filepath.Dir(project), "origin.git"), src)
	gitRun(t, src, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(src, "findings.md"), []byte("the work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", "-A")
	gitRun(t, src, "commit", "-m", "the carried work")
	gitRun(t, src, "push", "-u", "origin", branch)

	return task, branch
}

// The point of the whole change: a task that lands here gets a worktree, on the
// carried branch, with the carried work in it. Before this, landing local wrote
// a placement and nothing else, and the next start refused with "task has no
// worktree yet" — the work was on origin and nothing ever went to fetch it.
func TestLandLocallyCreatesTheWorktreeOnTheCarriedBranch(t *testing.T) {
	database := placeTestDB(t)
	project := landRepo(t, database)
	task, branch := carriedTask(t, database, project)

	if err := recordCarriedBranch(database, task, branch); err != nil {
		t.Fatalf("recordCarriedBranch: %v", err)
	}
	if err := landLocally(database, task); err != nil {
		t.Fatalf("landLocally: %v", err)
	}

	if task.WorktreePath == "" {
		t.Fatal("no worktree was created, so the next start refuses to run the task")
	}
	if _, err := os.Stat(task.WorktreePath); err != nil {
		t.Fatalf("worktree path recorded but not on disk: %v", err)
	}
	if got := gitRun(t, task.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Errorf("worktree is on %q, want the carried branch %q", got, branch)
	}
	// The file is the only proof that matters: a worktree on a same-named branch
	// cut fresh from main looks right and contains none of the work.
	if _, err := os.Stat(filepath.Join(task.WorktreePath, "findings.md")); err != nil {
		t.Errorf("the carried work is not in the worktree: %v", err)
	}

	stored, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WorktreePath != task.WorktreePath {
		t.Errorf("stored worktree = %q, want %q", stored.WorktreePath, task.WorktreePath)
	}
}

// The branch has to reach the task as a FIELD, not as prose in the placement
// reason. Reading it back out of an English sentence is not something any code
// path does, which is why the carried branch was invisible to worktree setup.
func TestPlaceLocalRecordsTheCarriedBranchOnTheTask(t *testing.T) {
	database := placeTestDB(t)
	project := landRepo(t, database)

	task := &db.Task{Title: "has a worktree here", Type: "task", Project: "land"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	branch := "task/" + strconv.FormatInt(task.ID, 10) + "-has-a-worktree-here"
	wt := filepath.Join(project, ".task-worktrees", strconv.FormatInt(task.ID, 10)+"-has-a-worktree-here")
	gitRun(t, project, "worktree", "add", "-b", branch, wt, "main")
	task.WorktreePath = wt
	if err := database.UpdateTask(task); err != nil {
		t.Fatal(err)
	}

	if err := carryAndPlace(context.Background(), database, task, db.TaskPlacement{}, "local", "", false); err != nil {
		t.Fatalf("carryAndPlace: %v", err)
	}

	stored, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SourceBranch != branch {
		t.Errorf("SourceBranch = %q, want the carried branch %q", stored.SourceBranch, branch)
	}
	if stored.BranchName != branch {
		t.Errorf("BranchName = %q, want the carried branch %q", stored.BranchName, branch)
	}
}

// A landing that fails must not un-move the task. The carry has already been
// proven at this point and the placement is already written; failing the whole
// command would report "NOT been moved" about a task that HAS moved.
func TestLandingFailureLeavesThePlacementStanding(t *testing.T) {
	database := placeTestDB(t)
	landRepo(t, database)

	task := &db.Task{Title: "nowhere to land", Type: "task", Project: "land"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	// A branch that is on no remote and no local ref: worktree setup cannot
	// resolve it, which is the shape of every landing failure.
	if err := recordCarriedBranch(database, task, "task/does-not-exist-anywhere"); err != nil {
		t.Fatal(err)
	}
	err := landLocally(database, task)
	if err == nil {
		t.Fatal("landing a branch that exists nowhere reported success")
	}

	if err := carryAndPlace(context.Background(), database, task, db.TaskPlacement{}, "local", "", false); err != nil {
		t.Fatalf("carryAndPlace: %v", err)
	}
	got, _ := database.GetTaskPlacementDecision(task.ID)
	if !got.Decided {
		t.Error("the placement was not recorded")
	}
}

// The stuck state, exactly: a placement that says "here" and no worktree here.
// Re-running the command has to repair that rather than report a cheerful no-op,
// because a no-op leaves the task un-startable and says nothing about why.
func TestPlaceLocalRepairsATaskThatArrivedWithoutAWorktree(t *testing.T) {
	database := placeTestDB(t)
	project := landRepo(t, database)
	task, branch := carriedTask(t, database, project)

	if err := database.SetTaskPlacementDecision(task.ID, "", "moved here by hand", ""); err != nil {
		t.Fatal(err)
	}
	current, err := database.GetTaskPlacementDecision(task.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := carryAndPlace(context.Background(), database, task, current, "local", "", false); err != nil {
		t.Fatalf("carryAndPlace: %v", err)
	}

	if task.WorktreePath == "" {
		t.Fatal("re-placing a task with no worktree left it with no worktree")
	}
	if got := gitRun(t, task.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Errorf("worktree is on %q, want the carried branch %q", got, branch)
	}
	if _, err := os.Stat(filepath.Join(task.WorktreePath, "findings.md")); err != nil {
		t.Errorf("the repair cut a fresh branch instead of finding the work on origin: %v", err)
	}
}

// The adoption is what makes that repair possible: nothing recorded the branch,
// so the only way to find the work is that a branch named for this task is on
// origin. It must not fire for a task whose branch is not there — that task has
// simply never run, and it starts from the default branch as always.
func TestLandingLeavesANeverRunTaskOnTheDefaultBranch(t *testing.T) {
	database := placeTestDB(t)
	landRepo(t, database)

	task := &db.Task{Title: "never run", Type: "task", Project: "land"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	if err := landLocally(database, task); err != nil {
		t.Fatalf("landLocally: %v", err)
	}
	if _, err := os.Stat(filepath.Join(task.WorktreePath, "findings.md")); err == nil {
		t.Error("a task that never ran picked up someone else's work")
	}
	if _, err := os.Stat(filepath.Join(task.WorktreePath, "README")); err != nil {
		t.Errorf("worktree was not cut from the default branch: %v", err)
	}
}
