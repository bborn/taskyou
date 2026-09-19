package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// remoteOpenTask is a task placed on another host, with a worktree there and a
// stale local worktree path of its own — the state a moved task is left in, and
// the one where opening the local path silently shows pre-move code.
func remoteOpenTask(t *testing.T, app *AppModel, localPath string) *db.Task {
	t.Helper()
	task := &db.Task{Title: "placed work", Status: db.StatusProcessing, WorktreePath: localPath}
	if err := app.db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := app.db.SetTaskPlacement(task.ID, "ol-agents", "placement plugin"); err != nil {
		t.Fatal(err)
	}
	if err := app.db.SetTaskRemoteWorktree(task.ID, "/home/olgm/app/.task-worktrees/9-placed", "task/9-placed"); err != nil {
		t.Fatal(err)
	}
	task.PlacementTarget = "ol-agents"
	app.executor = executor.New(app.db, config.New(app.db))
	return task
}

// "o" on a placed task opens the worktree on its host, which is what it means
// for a local task too. It must never open this machine's directory of the same
// name: that is the coordinator's own checkout, or the worktree the task had
// before it was moved.
func TestOpenWorktreeFollowsAPlacedTaskToItsHost(t *testing.T) {
	app, _ := refreshTestModel(t)
	local := t.TempDir()
	task := remoteOpenTask(t, app, local)

	// A stub "code" records the argv it was started with.
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	editor := filepath.Join(bin, "code")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+calls+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editor)
	t.Setenv("EDITOR", "")

	msg, ok := app.openWorktreeInEditor(task)().(worktreeOpenedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("open failed: %+v", msg)
	}
	if !strings.Contains(msg.message, "ol-agents") {
		t.Errorf("the confirmation does not say where it opened: %q", msg.message)
	}
	// Give the editor a moment to be exec'd and write its line.
	waitFor(t, func() bool { b, _ := os.ReadFile(calls); return len(b) > 0 }, 5*time.Second)
	recorded, _ := os.ReadFile(calls)
	if !strings.Contains(string(recorded), "--remote ssh-remote+ol-agents /home/olgm/app/.task-worktrees/9-placed") {
		t.Errorf("editor was not asked for the worktree on the host: %q", recorded)
	}
	if strings.Contains(string(recorded), local) {
		t.Errorf("editor was pointed at this machine's stale worktree: %q", recorded)
	}
}

// An editor that cannot reach another machine is told nothing to open. The path
// exists here often enough that pointing it at the local copy is the worst
// available outcome, so the answer is where the code is and how to get there.
func TestOpenWorktreeTellsYouWhereTheCodeIsWhenTheEditorCannotReachIt(t *testing.T) {
	app, _ := refreshTestModel(t)
	task := remoteOpenTask(t, app, t.TempDir())
	t.Setenv("VISUAL", "vim")
	t.Setenv("EDITOR", "")

	msg, ok := app.openWorktreeInEditor(task)().(worktreeOpenedMsg)
	if !ok || msg.err == nil {
		t.Fatalf("a task on another host was reported as opened: %+v", msg)
	}
	for _, want := range []string{"ol-agents", "/home/olgm/app/.task-worktrees/9-placed", "ssh ol-agents"} {
		if !strings.Contains(msg.err.Error(), want) {
			t.Errorf("the answer never mentions %q: %v", want, msg.err)
		}
	}
}

// The port check follows the task to its host, which costs a round trip, so it
// is asked less often than a local lsof — and not at all while the host has a
// known transport problem, where it would cost every refresh its turn.
func TestServerCheckIsCheaperForATaskOnAnotherHost(t *testing.T) {
	local := &db.Task{ID: 1, Port: 3010}
	placed := &db.Task{ID: 2, Port: 3010, PlacementTarget: "ol-agents"}
	if serverCheckInterval(placed) <= serverCheckInterval(local) {
		t.Errorf("a host round trip is polled as often as a local lsof: %v vs %v",
			serverCheckInterval(placed), serverCheckInterval(local))
	}

	app, _ := refreshTestModel(t)
	if err := app.db.RecordHostHealth("ol-agents", "ssh: connect: no route to host", false); err != nil {
		t.Fatal(err)
	}
	m := &DetailModel{task: placed, database: app.db, serverListening: true, serverURL: "http://ol-agents:3010"}
	m.checkServerListening()
	if m.serverListening || m.serverURL != "" {
		t.Errorf("an unreachable host was still credited with a server: %v %q", m.serverListening, m.serverURL)
	}
}
