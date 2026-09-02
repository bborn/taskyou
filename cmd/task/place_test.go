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

	if err := placeTask(context.Background(), database, task, db.TaskPlacement{}, "local", "", false); err != nil {
		t.Fatalf("placeTask: %v", err)
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
			if err := placeTask(context.Background(), database, task, db.TaskPlacement{}, name, "", false); err != nil {
				t.Fatalf("placeTask(%q): %v", name, err)
			}
			got, _ := database.GetTaskPlacementDecision(task.ID)
			if !got.Decided || got.Target != "" {
				t.Errorf("%q gave target %q decided=%v, want local", name, got.Target, got.Decided)
			}
		})
	}
}

// The whole point of the force gate: a task with work on this machine does not
// silently move, because the branch and session cannot follow it.
func TestPlaceTaskRefusesToStrandLocalWork(t *testing.T) {
	database := placeTestDB(t)
	task := placeTestTask(t, database)
	task.WorktreePath = t.TempDir()

	err := placeTask(context.Background(), database, task, db.TaskPlacement{}, "far-host", "/srv/repo", false)
	if err == nil {
		t.Fatal("placeTask moved a task away from its own worktree without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error does not say how to proceed: %v", err)
	}

	got, _ := database.GetTaskPlacementDecision(task.ID)
	if got.Decided {
		t.Error("a refused move still wrote a placement")
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

	if err := placeTask(context.Background(), database, task, current, "local", "", true); err != nil {
		t.Fatalf("placeTask: %v", err)
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

	err := placeTask(context.Background(), database, task, db.TaskPlacement{}, "far-host", "", false)
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

	if err := placeTask(context.Background(), database, task, current, "local", "", false); err != nil {
		t.Fatalf("placeTask: %v", err)
	}
	got, _ := database.GetTaskPlacementDecision(task.ID)
	if got.Reason != "resolver said here" {
		t.Errorf("a no-op move overwrote the reason: %q", got.Reason)
	}
}

func TestPlaceTaskRefusesToMoveARunningTask(t *testing.T) {
	database := placeTestDB(t)
	task := placeTestTask(t, database)
	task.Status = db.StatusProcessing

	err := placeTask(context.Background(), database, task, db.TaskPlacement{}, "local", "", false)
	if err == nil || !strings.Contains(err.Error(), "running right now") {
		t.Fatalf("error = %v, want a refusal to move a running task", err)
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
