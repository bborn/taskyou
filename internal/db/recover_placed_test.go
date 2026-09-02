package db

import (
	"path/filepath"
	"testing"
)

// The stale-reference sweep reads the LOCAL tmux server. A task placed on
// another host records the session it was given THERE, which is never in a local
// listing — so the sweep used to erase the only pointer ty had to a running
// remote agent. Task 5271 lost its session that way, mid-run, on a daemon
// restart.
func TestRecoverStaleTmuxRefsLeavesPlacedTasksAlone(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.CreateProject(&Project{Name: "test", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}

	placed := &Task{Title: "runs on a host", Type: "task", Project: "test"}
	local := &Task{Title: "runs here", Type: "task", Project: "test"}
	for _, task := range []*Task{placed, local} {
		if err := database.CreateTask(task); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(
			`UPDATE tasks SET daemon_session = 'task-daemon-23335', tmux_window_id = '@25' WHERE id = ?`,
			task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SetTaskPlacementDecision(placed.ID, "ik-agents", "only host", "/srv/repo"); err != nil {
		t.Fatal(err)
	}

	// Neither reference is in the local tmux server's listing.
	if _, _, err := database.RecoverStaleTmuxRefs(
		map[string]bool{"task-daemon-999": true}, map[string]bool{"@1": true}); err != nil {
		t.Fatal(err)
	}

	if session, window := tmuxRefs(t, database, placed.ID); session == "" || window == "" {
		t.Errorf("swept a placed task's refs (session=%q window=%q); its agent is on another host",
			session, window)
	}
	if session, window := tmuxRefs(t, database, local.ID); session != "" || window != "" {
		t.Errorf("left a local task's stale refs (session=%q window=%q)", session, window)
	}
}

// The no-active-sessions branch is the one a fresh daemon actually takes, and it
// used to clear every row unconditionally.
func TestRecoverStaleTmuxRefsWithNoLocalSessionsSpareaPlacedTasks(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.CreateProject(&Project{Name: "test", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}

	placed := &Task{Title: "runs on a host", Type: "task", Project: "test"}
	if err := database.CreateTask(placed); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`UPDATE tasks SET daemon_session = 'task-daemon-23335', tmux_window_id = '@25' WHERE id = ?`,
		placed.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskPlacementDecision(placed.ID, "ik-agents", "only host", "/srv/repo"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := database.RecoverStaleTmuxRefs(nil, nil); err != nil {
		t.Fatal(err)
	}

	if session, window := tmuxRefs(t, database, placed.ID); session == "" || window == "" {
		t.Errorf("a fresh daemon erased a placed task's refs (session=%q window=%q)", session, window)
	}
}

func tmuxRefs(t *testing.T, database *DB, taskID int64) (session, window string) {
	t.Helper()
	row := database.QueryRow(
		`SELECT COALESCE(daemon_session, ''), COALESCE(tmux_window_id, '') FROM tasks WHERE id = ?`, taskID)
	if err := row.Scan(&session, &window); err != nil {
		t.Fatal(err)
	}
	return session, window
}
