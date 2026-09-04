package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func placeTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "test", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return database
}

func placeTestTask(t *testing.T, database *db.DB) *db.Task {
	t.Helper()
	task := &db.Task{Title: "a task", Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPlaceTaskPinsLocal(t *testing.T) {
	database := placeTestDB(t)
	task := placeTestTask(t, database)

	if err := carryAndPlace(context.Background(), database, task, db.TaskPlacement{}, "local", "", false); err != nil {
		t.Fatalf("carryAndPlace: %v", err)
	}

	got, err := database.GetTaskPlacementDecision(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Decided {
		t.Fatal("pinning local recorded no decision, so the resolver would still be asked")
	}
	if got.Target != "" {
		t.Errorf("target = %q, want local", got.Target)
	}
}

// "here" and "localhost" mean the same thing as "local" — the wire format's
// empty target is not something anyone can type.
func TestPlaceTaskAcceptsEveryNameForLocal(t *testing.T) {
	for _, name := range []string{"local", "here", "Localhost", "LOCAL"} {
		t.Run(name, func(t *testing.T) {
			database := placeTestDB(t)
			task := placeTestTask(t, database)
			if err := carryAndPlace(context.Background(), database, task, db.TaskPlacement{}, name, "", false); err != nil {
				t.Fatalf("carryAndPlace(%q): %v", name, err)
			}
			got, _ := database.GetTaskPlacementDecision(task.ID)
			if !got.Decided || got.Target != "" {
				t.Errorf("%q gave target %q decided=%v, want local", name, got.Target, got.Decided)
			}
		})
	}
}

// Work that cannot be carried blocks the move. This used to be a refusal to
// move a task that HAD work, which was backwards: carrying it is the point. What
// must never happen is a move that reports success while the work stays behind,
// so an uncarryable worktree stops the placement and says how to proceed anyway.
func TestPlaceTaskWillNotMoveWorkItCannotCarry(t *testing.T) {
	database := placeTestDB(t)
	task := placeTestTask(t, database)
	task.WorktreePath = t.TempDir() // a directory, but not a git repo

	err := carryAndPlace(context.Background(), database, task, db.TaskPlacement{}, "local", "", false)
	if err == nil {
		t.Fatal("moved a task whose work could not be carried")
	}
	if !strings.Contains(err.Error(), "NOT been moved") {
		t.Errorf("error does not say the task stayed put: %v", err)
	}

	got, _ := database.GetTaskPlacementDecision(task.ID)
	if got.Decided {
		t.Error("a failed carry still wrote a placement, leaving the task half-moved")
	}
}

// --force is the escape hatch, and it means one thing: move without the work.
func TestPlaceTaskForceMovesWithoutTheWork(t *testing.T) {
	database := placeTestDB(t)
	task := placeTestTask(t, database)
	task.WorktreePath = t.TempDir()

	if err := carryAndPlace(context.Background(), database, task, db.TaskPlacement{}, "local", "", true); err != nil {
		t.Fatalf("--force did not move the task: %v", err)
	}
	got, _ := database.GetTaskPlacementDecision(task.ID)
	if !got.Decided {
		t.Error("--force did not record the placement")
	}
}

// A move away from a host must drop that host's checkout, or the task arrives
// pointing at a directory that belongs to the machine it just left.
func TestPlaceTaskForgetsTheOldHostsWorktree(t *testing.T) {
	database := placeTestDB(t)
	task := placeTestTask(t, database)
	if err := database.SetTaskPlacementDecision(task.ID, "old-host", "resolver", "/srv/repo"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskRemoteWorktree(task.ID, "/srv/repo/.task-worktrees/x", "task/x"); err != nil {
		t.Fatal(err)
	}
	current, _ := database.GetTaskPlacementDecision(task.ID)

	if err := carryAndPlace(context.Background(), database, task, current, "local", "", true); err != nil {
		t.Fatalf("carryAndPlace: %v", err)
	}

	var path, branch string
	row := database.QueryRow(
		`SELECT COALESCE(remote_worktree_path,''), COALESCE(remote_branch,'') FROM tasks WHERE id = ?`, task.ID)
	if err := row.Scan(&path, &branch); err != nil {
		t.Fatal(err)
	}
	if path != "" || branch != "" {
		t.Errorf("moved task still points at old-host's checkout: %q %q", path, branch)
	}
}

// Naming a host with no directory is a question ty cannot answer for you: the
// resolver's inventory is the plugin's, not core's.
func TestPlaceTaskRequiresADirectoryForANewHost(t *testing.T) {
	database := placeTestDB(t)
	task := placeTestTask(t, database)

	err := carryAndPlace(context.Background(), database, task, db.TaskPlacement{}, "far-host", "", false)
	if err == nil || !strings.Contains(err.Error(), "--dir") {
		t.Fatalf("error = %v, want a request for --dir", err)
	}
}

// Re-placing on the host a task is already on keeps the directory that was
// resolved for it, rather than demanding it again.
func TestPlaceTaskIsANoOpOnTheSameHost(t *testing.T) {
	database := placeTestDB(t)
	task := placeTestTask(t, database)
	if err := database.SetTaskPlacementDecision(task.ID, "", "resolver said here", ""); err != nil {
		t.Fatal(err)
	}
	current, _ := database.GetTaskPlacementDecision(task.ID)

	if err := carryAndPlace(context.Background(), database, task, current, "local", "", false); err != nil {
		t.Fatalf("carryAndPlace: %v", err)
	}
	got, _ := database.GetTaskPlacementDecision(task.ID)
	if got.Reason != "resolver said here" {
		t.Errorf("a no-op move overwrote the reason: %q", got.Reason)
	}
}

// A running task is the one you most want to move — "it is on mona and I want to
// browser-test it here" — so this must not be refused.
func TestPlaceTaskMovesARunningTask(t *testing.T) {
	database := placeTestDB(t)
	task := placeTestTask(t, database)
	task.Status = db.StatusProcessing

	if err := carryAndPlace(context.Background(), database, task, db.TaskPlacement{}, "local", "", false); err != nil {
		t.Fatalf("refused to move a running task: %v", err)
	}
	got, _ := database.GetTaskPlacementDecision(task.ID)
	if !got.Decided {
		t.Error("a running task's move did not record the placement")
	}
}

func TestDescribePlacementSaysWhatHappensNext(t *testing.T) {
	worktree := t.TempDir()
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	// Undecided but with local work: the guard will pin it, and the output says so.
	out := describePlacement(&db.Task{ID: 5206, Title: "t", WorktreePath: worktree}, db.TaskPlacement{})
	if !strings.Contains(out, "pinned here on its next run") {
		t.Errorf("undecided-with-work output does not explain what happens next:\n%s", out)
	}

	// Undecided with nothing here: the resolver decides.
	out = describePlacement(&db.Task{ID: 1, Title: "t"}, db.TaskPlacement{})
	if !strings.Contains(out, "resolver will be asked") {
		t.Errorf("undecided output does not explain what happens next:\n%s", out)
	}

	// Decided remote: host, dir and reason.
	out = describePlacement(&db.Task{ID: 1, Title: "t"},
		db.TaskPlacement{Decided: true, Target: "ol-agents", WorkDir: "/srv/repo", Reason: "most free memory"})
	for _, want := range []string{"ol-agents", "/srv/repo", "most free memory"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
