package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// The regression from task 5206: a task that already ran on this machine had no
// recorded placement (it predates the feature), so its next retry consulted the
// resolver and shipped it — and its worktree, branch and session all stayed here.
func TestResolvePlacementKeepsATaskThatAlreadyRanHere(t *testing.T) {
	plugins := t.TempDir()
	installPlacementPlugin(t, plugins, "ty-on",
		"#!/bin/sh\necho '{\"target\":\"far-host\",\"workdir\":\"~/projects\",\"reason\":\"most free memory\"}'\n")
	e, database := placementExecutor(t, plugins)
	task := placementTestTask(t, database)

	// The evidence of a local first attempt: a worktree on this disk.
	worktree := filepath.Join(t.TempDir(), "5206-worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	task.WorktreePath = worktree

	runner, placement, err := e.resolvePlacement(context.Background(), task)
	if err != nil {
		t.Fatalf("resolvePlacement: %v", err)
	}
	if _, ok := runner.(LocalRunner); !ok {
		t.Fatalf("runner = %T, want LocalRunner — the task was shipped away from its own worktree", runner)
	}
	if !placement.IsLocal() {
		t.Errorf("placement target = %q, want local", placement.Target)
	}

	// And the decision is written, so the resolver is not asked a second time.
	recorded, err := database.GetTaskPlacementDecision(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !recorded.Decided {
		t.Error("staying local was not recorded, so the next retry would ask again")
	}
	if recorded.Target != "" {
		t.Errorf("recorded target = %q, want local", recorded.Target)
	}
}

// A session file lives only on the machine that wrote it, so it is local state
// even after the worktree has been cleaned up.
func TestResolvePlacementKeepsATaskWithALocalSession(t *testing.T) {
	plugins := t.TempDir()
	installPlacementPlugin(t, plugins, "ty-on",
		"#!/bin/sh\necho '{\"target\":\"far-host\",\"reason\":\"most free memory\"}'\n")
	e, database := placementExecutor(t, plugins)
	task := placementTestTask(t, database)
	task.ClaudeSessionID = "efab8128-8132-4229-9493-1ef41a71c917"

	runner, _, err := e.resolvePlacement(context.Background(), task)
	if err != nil {
		t.Fatalf("resolvePlacement: %v", err)
	}
	if _, ok := runner.(LocalRunner); !ok {
		t.Fatalf("runner = %T, want LocalRunner — the task was shipped away from its own session", runner)
	}
	_ = database
}

// A worktree path recorded for a directory that no longer exists is not state:
// there is nothing here to strand, so a fresh task is still free to be placed.
func TestResolvePlacementStillPlacesWhenTheWorktreeIsGone(t *testing.T) {
	plugins := t.TempDir()
	installPlacementPlugin(t, plugins, "ty-on",
		"#!/bin/sh\necho '{\"target\":\"far-host\",\"workdir\":\"/srv/repo\",\"reason\":\"most free memory\"}'\n")
	stubSSH(t, "#!/bin/sh\necho /srv/repo\n")
	e, database := placementExecutor(t, plugins)
	task := placementTestTask(t, database)
	task.WorktreePath = filepath.Join(t.TempDir(), "never-created")

	runner, placement, err := e.resolvePlacement(context.Background(), task)
	if err != nil {
		t.Fatalf("resolvePlacement: %v", err)
	}
	if _, ok := runner.(RemoteRunner); !ok {
		t.Fatalf("runner = %T, want RemoteRunner", runner)
	}
	if placement.Target != "far-host" {
		t.Errorf("target = %q, want far-host", placement.Target)
	}
	_ = database
}

func TestHasLocalState(t *testing.T) {
	existing := t.TempDir()

	cases := []struct {
		name string
		task *db.Task
		want bool
	}{
		{"nil task", nil, false},
		{"fresh task", &db.Task{}, false},
		{"worktree on disk", &db.Task{WorktreePath: existing}, true},
		{"worktree gone", &db.Task{WorktreePath: filepath.Join(existing, "nope")}, false},
		{"session recorded", &db.Task{ClaudeSessionID: "abc"}, true},
		{"already placed away", &db.Task{WorktreePath: existing, PlacementTarget: "far-host"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := HasLocalState(tc.task); got != tc.want {
				t.Errorf("HasLocalState = %v, want %v", got, tc.want)
			}
		})
	}
}
