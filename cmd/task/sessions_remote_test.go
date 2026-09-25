package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tmuxtest"
)

// fakeListWindowsSSH puts a fake ssh first on PATH that answers
// `tmux list-windows` with the given lines, or fails the way the given exit code
// says. Exit 255 is ssh itself failing (unreachable); exit 1 is tmux on the far
// side saying there is no server.
func fakeListWindowsSSH(t *testing.T, exitCode int, lines ...string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if exitCode != 0 {
		script += "exit " + strconv.Itoa(exitCode) + "\n"
	} else {
		for _, l := range lines {
			script += "printf '%s\\n' " + sqQuote(l) + "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func sqQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// remoteSessionDB makes a task placed on host with a recorded daemon session,
// the way a live remote run records one.
func remoteSessionDB(t *testing.T, host, session, status string) (*db.DB, int64) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	task := &db.Task{Title: "remote work", Status: db.StatusQueued, Type: db.TypeCode, Executor: "claude"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	// Set the status directly: this is fixture setup, and the gated transition
	// API would refuse a hand-made task's jump straight to processing.
	if _, err := database.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, status, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskPlacement(task.ID, host, "only host serving this project"); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTaskDaemonSession(task.ID, session); err != nil {
		t.Fatal(err)
	}
	return database, task.ID
}

// The bug: `ty sessions list` enumerated this machine's tmux only, so an agent
// working on a placed host was absent from the listing entirely.
func TestRemoteAgentSessionsListsAPlacedHostsLiveWindow(t *testing.T) {
	database, taskID := remoteSessionDB(t, "ol-agents", "task-daemon-remote-abc123", db.StatusProcessing)
	fakeListWindowsSSH(t, 0,
		"1758000000:task-"+strconv.FormatInt(taskID, 10)+":task-daemon-remote-abc123",
		// Noise the scan must ignore: the task's shell window, and another
		// coordinator's task id in a session we never recorded.
		"1758000000:task-"+strconv.FormatInt(taskID, 10)+"-shell:task-daemon-remote-abc123",
		"1758000000:task-99999:task-daemon-remote-other",
	)

	sessions, problems := remoteAgentSessions(context.Background(), database)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(sessions), sessions)
	}
	s := sessions[0]
	if s.taskID != int(taskID) || s.host != "ol-agents" {
		t.Errorf("session = %+v, want task %d on ol-agents", s, taskID)
	}
	if !strings.Contains(s.info, "ol-agents") || !strings.Contains(s.info, "task-daemon-remote-abc123") {
		t.Errorf("info %q does not say where the session is", s.info)
	}
	if !strings.Contains(s.info, "last activity") {
		t.Errorf("info %q dropped the window's activity time", s.info)
	}
}

// A window that is NOT there means the task is not running there — that is an
// answer, not a problem, and tmux says it with exit 1.
func TestRemoteAgentSessionsReportsNothingWhenTheRemoteTmuxIsGone(t *testing.T) {
	database, _ := remoteSessionDB(t, "ol-agents", "task-daemon-remote-abc123", db.StatusProcessing)
	fakeListWindowsSSH(t, 1)

	sessions, problems := remoteAgentSessions(context.Background(), database)
	if len(sessions) != 0 || len(problems) != 0 {
		t.Fatalf("got %d sessions and %d problems, want none of either", len(sessions), len(problems))
	}
}

// A host ty cannot reach must be NAMED. Dropping it silently is how a live agent
// gets reported as gone, which is the whole bug this change is about.
func TestRemoteAgentSessionsNamesAnUnreachableHost(t *testing.T) {
	database, _ := remoteSessionDB(t, "ol-agents", "task-daemon-remote-abc123", db.StatusProcessing)
	fakeListWindowsSSH(t, 255) // ssh itself failed

	sessions, problems := remoteAgentSessions(context.Background(), database)
	if len(sessions) != 0 {
		t.Fatalf("got %d sessions from an unreachable host", len(sessions))
	}
	if len(problems) != 1 || problems[0].host != "ol-agents" || problems[0].tasks != 1 {
		t.Fatalf("problems = %+v, want one naming ol-agents and its 1 placed task", problems)
	}
}

// Placement is decided long before the remote session exists, and a done task's
// session is over. Neither is worth an ssh round trip that can only come back
// empty.
func TestRemoteAgentSessionsSkipsTasksWithNoLiveRemoteSession(t *testing.T) {
	database, taskID := remoteSessionDB(t, "ol-agents", "task-daemon-remote-abc123", db.StatusProcessing)
	fakeListWindowsSSH(t, 255) // any ssh at all would be a failure here

	if err := database.UpdateTaskDaemonSession(taskID, ""); err != nil {
		t.Fatal(err)
	}
	if _, problems := remoteAgentSessions(context.Background(), database); len(problems) != 0 {
		t.Errorf("asked about a task with no recorded remote session: %+v", problems)
	}

	if err := database.UpdateTaskDaemonSession(taskID, "task-daemon-remote-abc123"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, db.StatusDone, taskID); err != nil {
		t.Fatal(err)
	}
	if _, problems := remoteAgentSessions(context.Background(), database); len(problems) != 0 {
		t.Errorf("asked about a finished task: %+v", problems)
	}
}

// The wording is the thing that was wrong: "No agent sessions running" was an
// assertion the command had not checked.
func TestRenderSessionsDoesNotClaimNothingIsRunningWhenAHostWasUnreachable(t *testing.T) {
	var out bytes.Buffer
	renderSessions(&out, nil, []remoteHostProblem{{host: "ol-agents", tasks: 2, err: errors.New("exit status 255")}})

	got := out.String()
	if strings.Contains(got, "No agent sessions running") {
		t.Errorf("claimed nothing is running without having looked: %q", got)
	}
	for _, want := range []string{"ol-agents", "2 placed task(s) unaccounted for", "exit status 255"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not mention %q", got, want)
		}
	}
}

// With nothing anywhere, the old wording is still the right wording.
func TestRenderSessionsSaysNothingIsRunningWhenItReallyLooked(t *testing.T) {
	var out bytes.Buffer
	renderSessions(&out, nil, nil)
	if !strings.Contains(out.String(), "No agent sessions running") {
		t.Errorf("output = %q", out.String())
	}
}

// Memory is measured by inspecting processes here, so the total must not be
// presented as covering a remote agent's.
func TestRenderSessionsScopesTheMemoryTotalToThisMachine(t *testing.T) {
	var out bytes.Buffer
	renderSessions(&out, []agentSession{
		{taskID: 1, taskTitle: "local", executor: "claude", memoryMB: 900, info: "task-daemon-1"},
		{taskID: 2, taskTitle: "placed", executor: "claude", host: "ol-agents", info: "ol-agents: task-daemon-remote-abc123"},
	}, nil)

	got := out.String()
	for _, want := range []string{"2 total", "1 remote", "900MB memory on this machine", "task-1", "task-2", "ol-agents"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not mention %q", got, want)
		}
	}
}

// `ty logs 4013` reads as "task 4013's logs" and used to silently tail every
// session file on the machine instead. The refusal has to point at the commands
// that do take a task id, or the user is left guessing.
func TestLogsRefusesATaskIDAndSaysWhatToRunInstead(t *testing.T) {
	cmd := &cobra.Command{Use: "logs"}
	(&cobra.Command{Use: "ty"}).AddCommand(cmd) // so CommandPath() is "ty logs"

	err := rejectTaskIDArg(cmd, []string{"4013"})
	if err == nil {
		t.Fatal("ty logs 4013 was accepted")
	}
	for _, want := range []string{"ty show 4013 --logs", "ty output 4013"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not point at %q", err, want)
		}
	}
	if err := rejectTaskIDArg(cmd, nil); err != nil {
		t.Errorf("bare `ty logs` was refused: %v", err)
	}
	if err := rejectTaskIDArg(cmd, []string{"nonsense"}); err == nil {
		t.Error("a non-numeric argument was accepted")
	}
}

// The user-facing claim of this change: ONE listing covers both machines. Before
// it, a task working on a placed host was simply absent, and on a coordinator
// running nothing locally the command said "No agent sessions running".
func TestSessionsListingCoversBothThisMachineAndAPlacedHost(t *testing.T) {
	requireTmux(t)
	tmuxtest.Isolate(t)

	database, remoteID := remoteSessionDB(t, "ol-agents", "task-daemon-remote-abc123", db.StatusProcessing)
	local := &db.Task{Title: "running right here", Status: db.StatusQueued, Type: db.TypeCode, Executor: "claude"}
	if err := database.CreateTask(local); err != nil {
		t.Fatal(err)
	}
	makeDaemonSessionWithName(t, "task-daemon-listing-test", int(local.ID))
	fakeListWindowsSSH(t, 0, "1758000000:task-"+strconv.FormatInt(remoteID, 10)+":task-daemon-remote-abc123")

	remote, problems := remoteAgentSessions(context.Background(), database)
	var out bytes.Buffer
	renderSessions(&out, append(getSessions(database), remote...), problems)

	got := out.String()
	for _, want := range []string{
		"task-" + strconv.FormatInt(local.ID, 10), "running right here",
		"task-" + strconv.FormatInt(remoteID, 10), "remote work", "ol-agents",
		"1 remote",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("listing %q does not mention %q", got, want)
		}
	}
}
