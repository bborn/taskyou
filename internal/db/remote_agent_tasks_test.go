package db

import (
	"path/filepath"
	"testing"
)

// ListRemoteAgentTasks is the input to `ty sessions list`'s remote half: the
// hosts it has to go and ask. It must name every task whose agent window lives
// on another machine, and nothing whose window could only be here.
func TestListRemoteAgentTasksNamesOnlyLivePlacedTasks(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	newTask := func(title, status, host, session string) int64 {
		task := &Task{Title: title, Status: StatusQueued, Type: TypeCode, Executor: "codex"}
		if err := database.CreateTask(task); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(
			`UPDATE tasks SET status = ?, placement_target = ?, daemon_session = ? WHERE id = ?`,
			status, host, session, task.ID); err != nil {
			t.Fatal(err)
		}
		return task.ID
	}

	processing := newTask("placed and running", StatusProcessing, "ol-agents", "task-daemon-remote-a")
	blocked := newTask("placed and waiting on a human", StatusBlocked, "ik-agents", "task-daemon-remote-b")
	// A local run's window is on this machine's tmux server, which the local
	// listing already enumerates.
	newTask("running here", StatusProcessing, "", "task-daemon-123")
	// Placement is decided before the remote session exists; there is no window
	// to ask about yet.
	newTask("placed, not started", StatusProcessing, "ol-agents", "")
	// A finished task's session is over.
	newTask("placed and done", StatusDone, "ol-agents", "task-daemon-remote-c")

	trashed := newTask("placed and trashed", StatusProcessing, "ol-agents", "task-daemon-remote-d")
	if err := database.SoftDeleteTask(trashed); err != nil {
		t.Fatal(err)
	}

	got, err := database.ListRemoteAgentTasks()
	if err != nil {
		t.Fatalf("ListRemoteAgentTasks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want 2: %+v", len(got), got)
	}

	byID := map[int64]RemoteAgentTask{}
	for _, task := range got {
		byID[task.ID] = task
	}
	p, ok := byID[processing]
	if !ok {
		t.Fatalf("processing placed task missing: %+v", got)
	}
	if p.Host != "ol-agents" || p.DaemonSession != "task-daemon-remote-a" {
		t.Errorf("task = %+v, want ol-agents/task-daemon-remote-a", p)
	}
	if p.Title != "placed and running" || p.Executor != "codex" {
		t.Errorf("task = %+v, want the title and executor needed to render a row", p)
	}
	if _, ok := byID[blocked]; !ok {
		t.Errorf("a blocked placed task has a live agent waiting on input; it must be listed: %+v", got)
	}
}
