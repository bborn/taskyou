package executor

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// A local Claude profile is a directory on THIS machine. Task 5289 was routed
// onto /Users/bruno/.claude-ik and then placed on a Linux host, where that path
// does not exist — and because the placement is recorded, every retry reused it.
func TestRemotePlacementDropsALocalClaudeProfile(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "test", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	e := New(database, config.New(database))
	t.Cleanup(func() { e.hostChans.Close() })

	task := &db.Task{Title: "a task", Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskClaudeConfigDir(task.ID, "/Users/bruno/.claude-ik"); err != nil {
		t.Fatal(err)
	}
	task.ClaudeConfigDir = "/Users/bruno/.claude-ik"

	e.recordPlacement(task, "mona", "only host serving taskyou", "/home/bruno/x")

	if task.ClaudeConfigDir != "" {
		t.Errorf("in-memory profile survived placement on a remote host: %q", task.ClaudeConfigDir)
	}
	stored, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ClaudeConfigDir != "" {
		t.Errorf("stored profile survived placement on a remote host: %q", stored.ClaudeConfigDir)
	}
}

// A local placement must keep its profile — that is the whole point of routing.
func TestLocalPlacementKeepsItsClaudeProfile(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "test", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	e := New(database, config.New(database))
	t.Cleanup(func() { e.hostChans.Close() })

	task := &db.Task{Title: "a task", Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	const profile = "/Users/bruno/.claude-ik"
	if err := database.UpdateTaskClaudeConfigDir(task.ID, profile); err != nil {
		t.Fatal(err)
	}
	task.ClaudeConfigDir = profile

	e.recordPlacement(task, "local", "runs here", "")

	if task.ClaudeConfigDir != profile {
		t.Errorf("a local task lost its routed profile: %q", task.ClaudeConfigDir)
	}
	stored, _ := database.GetTask(task.ID)
	if stored.ClaudeConfigDir != profile {
		t.Errorf("a local task's stored profile was cleared: %q", stored.ClaudeConfigDir)
	}
}

func TestIsRemotePlacement(t *testing.T) {
	for _, tt := range []struct {
		target string
		want   bool
	}{
		{"", false}, {"local", false}, {" local ", false},
		{"mona", true}, {"ol-agents", true},
	} {
		if got := isRemotePlacement(tt.target); got != tt.want {
			t.Errorf("isRemotePlacement(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
}
