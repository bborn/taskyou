package main

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// artifactTestEnv points the CLI at a temp DB (DefaultPath honors
// WORKTREE_DB_PATH) holding one workflow task, and returns that task's ID.
func artifactTestEnv(t *testing.T, task *db.Task) int64 {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	t.Setenv("WORKTREE_DB_PATH", dbPath)

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task.ID
}

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}

func runArtifact(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newArtifactCmd()
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return cmd.Execute()
}

func TestArtifactSetGetRoundTrip(t *testing.T) {
	// A workflow step carries its shared branch as SourceBranch.
	id := artifactTestEnv(t, &db.Task{
		Title:        "[design] something",
		Project:      "personal",
		Tags:         "pipeline",
		SourceBranch: "pipeline/100-shared",
	})
	t.Setenv("WORKTREE_TASK_ID", "")

	body := "# Design\n\nThe full document.\n"
	if err := runArtifact(t, "set", "design", "--task-id", itoa(id), "--content", body); err != nil {
		t.Fatalf("artifact set: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runArtifact(t, "get", "design", "--task-id", itoa(id)); err != nil {
			t.Fatalf("artifact get: %v", err)
		}
	})
	if !strings.Contains(out, "The full document.") {
		t.Errorf("get output missing content, got: %q", out)
	}
}

func TestArtifactResolvesTaskIDFromEnv(t *testing.T) {
	id := artifactTestEnv(t, &db.Task{
		Title:        "[plan] something",
		Project:      "personal",
		Tags:         "pipeline",
		SourceBranch: "pipeline/200-shared",
	})
	// No --task-id: the worktree's WORKTREE_TASK_ID must be picked up, which is
	// what lets an agent call `ty artifact ...` with no arguments in its worktree.
	t.Setenv("WORKTREE_TASK_ID", itoa(id))

	if err := runArtifact(t, "set", "plan", "--content", "planned"); err != nil {
		t.Fatalf("artifact set via env: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runArtifact(t, "get", "plan"); err != nil {
			t.Fatalf("artifact get via env: %v", err)
		}
	})
	if !strings.Contains(out, "planned") {
		t.Errorf("expected content via env task id, got %q", out)
	}
}

func TestArtifactSetOverwrites(t *testing.T) {
	id := artifactTestEnv(t, &db.Task{
		Title: "[design] x", Project: "personal", Tags: "pipeline",
		SourceBranch: "pipeline/300-shared",
	})
	t.Setenv("WORKTREE_TASK_ID", itoa(id))

	if err := runArtifact(t, "set", "design", "--content", "first"); err != nil {
		t.Fatal(err)
	}
	if err := runArtifact(t, "set", "design", "--content", "second"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runArtifact(t, "get", "design"); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("expected overwrite to 'second', got %q", out)
	}
}

func TestArtifactSetFromFile(t *testing.T) {
	id := artifactTestEnv(t, &db.Task{
		Title: "[research] x", Project: "personal", Tags: "pipeline",
		SourceBranch: "pipeline/400-shared",
	})
	t.Setenv("WORKTREE_TASK_ID", itoa(id))

	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("from a file"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runArtifact(t, "set", "research", "--file", path); err != nil {
		t.Fatalf("set --file: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runArtifact(t, "get", "research"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "from a file") {
		t.Errorf("expected file content, got %q", out)
	}
}

func TestArtifactRejectsNonWorkflowTask(t *testing.T) {
	// No SourceBranch/BranchName => not part of a workflow. Must fail loudly
	// rather than silently writing to an empty branch key.
	id := artifactTestEnv(t, &db.Task{Title: "plain task", Project: "personal"})
	t.Setenv("WORKTREE_TASK_ID", itoa(id))

	err := runArtifact(t, "set", "design", "--content", "x")
	if err == nil || !strings.Contains(err.Error(), "not part of a workflow") {
		t.Fatalf("expected not-a-workflow error, got %v", err)
	}
}

func TestArtifactRequiresTaskID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	t.Setenv("WORKTREE_DB_PATH", dbPath)
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	t.Setenv("WORKTREE_TASK_ID", "")

	err = runArtifact(t, "get", "design")
	if err == nil || !strings.Contains(err.Error(), "task-id is required") {
		t.Fatalf("expected task-id required error, got %v", err)
	}
}

func TestArtifactGetMissingIsAnError(t *testing.T) {
	id := artifactTestEnv(t, &db.Task{
		Title: "[plan] x", Project: "personal", Tags: "pipeline",
		SourceBranch: "pipeline/500-shared",
	})
	t.Setenv("WORKTREE_TASK_ID", itoa(id))

	err := runArtifact(t, "get", "nope")
	if err == nil || !strings.Contains(err.Error(), "no artifact named") {
		t.Fatalf("expected missing-artifact error, got %v", err)
	}
}

func TestArtifactIsScopedToItsOwnWorkflow(t *testing.T) {
	// Two tasks on different pipeline branches must not see each other's docs.
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	t.Setenv("WORKTREE_DB_PATH", dbPath)
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &db.Task{Title: "a", Project: "personal", Tags: "pipeline", SourceBranch: "pipeline/aaa"}
	b := &db.Task{Title: "b", Project: "personal", Tags: "pipeline", SourceBranch: "pipeline/bbb"}
	if err := database.CreateTask(a); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateTask(b); err != nil {
		t.Fatal(err)
	}
	database.Close()

	t.Setenv("WORKTREE_TASK_ID", itoa(a.ID))
	if err := runArtifact(t, "set", "design", "--content", "A-only"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("WORKTREE_TASK_ID", itoa(b.ID))
	if err := runArtifact(t, "get", "design"); err == nil {
		t.Fatal("workflow B must not read workflow A's artifact")
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
