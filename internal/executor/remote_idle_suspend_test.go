package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// sideProcess starts a real, harmless process on "host" whose command line
// names path, the way a dev server started in a task's worktree names it. It
// reports whether the process is still running.
func (f *fakeRemoteTmux) sideProcess(t *testing.T, host, path string) func() bool {
	t.Helper()
	path = filepath.Join(f.procs, host, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("tail", "-f", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
	return func() bool {
		select {
		case <-exited:
			return false
		case <-time.After(100 * time.Millisecond):
			return true
		}
	}
}

// parkedFor makes a blocked task look parked since d ago.
func parkedFor(t *testing.T, database *db.DB, taskID int64, d time.Duration) {
	t.Helper()
	at := time.Now().UTC().Add(-d).Format("2006-01-02 15:04:05")
	if _, err := database.Exec(`UPDATE tasks SET completed_at = ? WHERE id = ?`, at, taskID); err != nil {
		t.Fatal(err)
	}
}

// A blocked task placed on another host that has sat idle past the idle-suspend
// timeout is suspended there just as a local one is here: its agent windows are
// ended, and so are the dev servers and watchers it left running out of its
// worktree. That is what kept a staging host's memory full for twenty days.
// Everything that is not this coordinator's idle task — a recently blocked one,
// a running one, the same task ID in another session or another coordinator's
// worktree, a task whose ID merely starts with the same digits — is left alone,
// no file is touched, and an unreachable host backs off.
func TestIdleBlockedRemoteTasksAreSuspended(t *testing.T) {
	e, database := placementExecutor(t, t.TempDir())
	if err := database.SetSetting(config.SettingIdleSuspendTimeout, "24h"); err != nil {
		t.Fatal(err)
	}
	coordinator, err := database.CoordinatorID()
	if err != nil {
		t.Fatal(err)
	}
	session := remoteDaemonSessionName(coordinator)
	fake := newFakeRemoteTmux(t)

	placed := func(host, status string, idle time.Duration) *db.Task {
		task := placedTask(t, database, host, status)
		if err := database.UpdateTaskDaemonSession(task.ID, session); err != nil {
			t.Fatal(err)
		}
		if status == db.StatusBlocked {
			parkedFor(t, database, task.ID, idle)
		}
		return task
	}
	idle := placed("mona", db.StatusBlocked, 30*time.Hour)       // worktree recorded
	idleNoPath := placed("mona", db.StatusBlocked, 48*time.Hour) // no worktree recorded
	recent := placed("mona", db.StatusBlocked, time.Hour)
	running := placed("mona", db.StatusProcessing, 0)
	asleep := placed("asleep", db.StatusBlocked, 30*time.Hour)

	idleDir := fmt.Sprintf("%d-idle-task", idle.ID)
	if err := database.SetTaskRemoteWorktree(idle.ID,
		filepath.Join(fake.procs, "mona", "repo", ".task-worktrees", idleDir), "task/"+idleDir); err != nil {
		t.Fatal(err)
	}

	w := func(host, sess, id string, taskID int64, suffix string) string {
		return fmt.Sprintf("%s %s %s task-%d%s", host, sess, id, taskID, suffix)
	}
	keep := []string{
		w("mona", session, "@5", recent.ID, ""),
		w("mona", session, "@6", running.ID, ""),
		w("mona", "task-daemon-remote-someoneelse", "@7", idle.ID, ""),
		w("mona", "task-daemon-4242", "@8", idle.ID, "-shell"),
		w("mona", session, "@9", 10*idle.ID+3, ""), // task-13 when idle is task-1
		w("asleep", session, "@1", asleep.ID, ""),
	}
	sort.Strings(keep)
	if err := os.WriteFile(fake.state, []byte(strings.Join(append([]string{
		w("mona", session, "@1", idle.ID, ""),
		w("mona", session, "@2", idle.ID, "-shell"),
		w("mona", session, "@3", idleNoPath.ID, ""),
	}, keep...), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wt := func(dir string) string { return filepath.Join("repo", ".task-worktrees", dir, "log") }
	ended := map[string]func() bool{
		"idle task's dev server":               fake.sideProcess(t, "mona", wt(idleDir)),
		"idle task's puma, by title":           fake.sideProcess(t, "mona", "puma 6.4.2 ["+idleDir+"]"),
		"idle task's watcher, found by its ID": fake.sideProcess(t, "mona", wt(fmt.Sprintf("%d-no-record", idleNoPath.ID))),
	}
	kept := map[string]func() bool{
		"recently blocked task's server": fake.sideProcess(t, "mona", wt(fmt.Sprintf("%d-recent", recent.ID))),
		"running task's server":          fake.sideProcess(t, "mona", wt(fmt.Sprintf("%d-running", running.ID))),
		"another coordinator's same ID":  fake.sideProcess(t, "mona", filepath.Join("other", ".task-worktrees", fmt.Sprintf("%d-theirs", idle.ID), "log")),
		"a task whose ID starts the same": fake.sideProcess(t, "mona", wt(fmt.Sprintf("%d3-longer-id", idle.ID))),
		"its puma, by title":              fake.sideProcess(t, "mona", fmt.Sprintf("puma [%d3-longer-id]", idleNoPath.ID)),
		"unreachable host's server":       fake.sideProcess(t, "asleep", wt(fmt.Sprintf("%d-asleep", asleep.ID))),
	}
	files := func() []string {
		var all []string
		_ = filepath.Walk(fake.procs, func(path string, _ os.FileInfo, _ error) error {
			all = append(all, path)
			return nil
		})
		return all
	}
	filesBefore := files()

	now := time.Now()
	e.sweepRemoteSessions(context.Background(), now)

	if got := fake.windows(t); strings.Join(got, "\n") != strings.Join(keep, "\n") {
		t.Fatalf("windows after sweep:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(keep, "\n"))
	}
	for name, alive := range ended {
		if alive() {
			t.Errorf("%s is still running", name)
		}
	}
	for name, alive := range kept {
		if !alive() {
			t.Errorf("%s was killed", name)
		}
	}
	if got := files(); strings.Join(got, "\n") != strings.Join(filesBefore, "\n") {
		t.Errorf("files changed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(filesBefore, "\n"))
	}
	for _, call := range fake.calls(t) {
		if strings.Contains(call, "rm ") || strings.Contains(call, "git ") {
			t.Errorf("suspending must only end processes, never touch files: %s", call)
		}
	}

	// The suspended tasks read as suspended, as a local one does: no session,
	// a log line saying so, and still blocked, waiting for their reply.
	for _, task := range []*db.Task{idle, idleNoPath} {
		got, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != db.StatusBlocked || got.DaemonSession != "" {
			t.Errorf("task #%d: status %q, daemon session %q; want blocked with no session", task.ID, got.Status, got.DaemonSession)
		}
		logs, _ := database.GetTaskLogs(task.ID, 50)
		var logged bool
		for _, l := range logs {
			logged = logged || (strings.Contains(l.Content, "suspended") && strings.Contains(l.Content, "mona"))
		}
		if !logged {
			t.Errorf("task #%d's log does not say it was suspended on mona", task.ID)
		}
	}
	for _, task := range []*db.Task{recent, running, asleep} {
		if got, _ := database.GetTask(task.ID); got.DaemonSession != session {
			t.Errorf("task #%d lost its session %q", task.ID, got.DaemonSession)
		}
	}

	// A suspended task is not asked about again; the unreachable host backs off...
	before := len(fake.calls(t))
	e.sweepRemoteSessions(context.Background(), now.Add(time.Second))
	if after := fake.calls(t); len(after) != before {
		t.Fatalf("second sweep made ssh calls: %v", after[before:])
	}

	// ...until its backoff has passed, when it is tried again.
	e.sweepRemoteSessions(context.Background(), now.Add(remoteSessionEndMaxBackoff))
	after := fake.calls(t)
	if len(after) != before+1 || !strings.HasPrefix(after[len(after)-1], "asleep ") {
		t.Fatalf("unreachable host was not retried after its backoff: %v", after[before:])
	}
}

// With reap_blocked_idle disabled, the sweep still ends an idle task's agent,
// but leaves its side processes running, as `ty sessions cleanup` would.
func TestIdleRemoteSuspendHonoursReapBlockedIdle(t *testing.T) {
	e, database := placementExecutor(t, t.TempDir())
	if err := database.SetSetting(config.SettingReapBlockedIdle, "disabled"); err != nil {
		t.Fatal(err)
	}
	coordinator, err := database.CoordinatorID()
	if err != nil {
		t.Fatal(err)
	}
	session := remoteDaemonSessionName(coordinator)
	task := placedTask(t, database, "mona", db.StatusBlocked)
	if err := database.UpdateTaskDaemonSession(task.ID, session); err != nil {
		t.Fatal(err)
	}
	parkedFor(t, database, task.ID, 30*time.Hour)
	fake := newFakeRemoteTmux(t, fmt.Sprintf("mona %s @1 task-%d", session, task.ID))
	server := fake.sideProcess(t, "mona", filepath.Join("repo", ".task-worktrees", fmt.Sprintf("%d-x", task.ID), "log"))

	e.sweepRemoteSessions(context.Background(), time.Now())

	if got := fake.windows(t); len(got) != 0 {
		t.Errorf("windows after sweep = %v, want none", got)
	}
	if !server() {
		t.Error("side process was killed with reap_blocked_idle disabled")
	}
}
