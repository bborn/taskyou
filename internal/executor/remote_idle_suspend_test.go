package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// fakeProc is a real, harmless process started for a test (tail -f on a file),
// standing in for a dev server, an agent, a tmux server.
type fakeProc struct {
	pid    int
	exited chan struct{}
}

func (p fakeProc) alive() bool {
	select {
	case <-p.exited:
		return false
	case <-time.After(100 * time.Millisecond):
		return true
	}
}

// startProc runs argv in dir and stops it when the test ends. Every path a test
// gives it is under f.procs/<host>/, which is all the fake ps lists.
func startProc(t *testing.T, dir string, argv ...string) fakeProc {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := fakeProc{pid: cmd.Process.Pid, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-p.exited })
	return p
}

// tailIn starts `tail -f <file>` (its command line naming file) with dir as
// its working directory.
func tailIn(t *testing.T, dir, file string) fakeProc {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return startProc(t, dir, "tail", "-f", file)
}

// tailAs is tailIn run under another name: a symlink to tail called name, the
// way an agent or tmux shows up in ps.
func tailAs(t *testing.T, name, dir, file string) fakeProc {
	t.Helper()
	bin := filepath.Join(filepath.Dir(dir), "bin-"+name)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	tail, err := exec.LookPath("tail")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tail, filepath.Join(bin, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return startProc(t, dir, filepath.Join(bin, name), "-f", file)
}

// parkedFor makes a blocked task look parked since d ago.
func parkedFor(t *testing.T, database *db.DB, taskID int64, d time.Duration) {
	t.Helper()
	at := time.Now().UTC().Add(-d).Format("2006-01-02 15:04:05")
	if _, err := database.Exec(`UPDATE tasks SET completed_at = ? WHERE id = ?`, at, taskID); err != nil {
		t.Fatal(err)
	}
}

func runOf(t *testing.T, database *db.DB, taskID int64) string {
	t.Helper()
	var run string
	if err := database.QueryRow(`SELECT run_id FROM remote_runs WHERE task_id = ?`, taskID).Scan(&run); err != nil {
		t.Fatalf("run of #%d: %v", taskID, err)
	}
	return run
}

func hasRun(t *testing.T, database *db.DB, taskID int64) bool {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM remote_runs WHERE task_id = ?`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// A blocked task placed on another host that has sat idle past the idle-suspend
// timeout is suspended there just as a local one is here: its agent windows are
// ended, and so are the dev servers and watchers it left running in its
// worktree. That is what kept a staging host's memory full for twenty days.
//
// The host is shared — on ik-agents ty logs in as the user that runs the host's
// own TaskYou — so everything else must survive: a recently blocked or running
// task; a task someone is typing in; the
// same task ID in another session or another coordinator's worktree; a task
// whose ID only starts the same; another task's agent whose prompt names the
// worktree; a tmux server whose command names it; anything under a live pane;
// text like "[1-Oct-2026"; and, without a recorded worktree, every side process.
// No file is touched, and an unreachable host backs off.
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
	mona := filepath.Join(fake.procs, "mona")
	worktree := func(dir string) string { return filepath.Join(mona, "repo", ".task-worktrees", dir) }

	placed := func(host, status string, idle time.Duration, dir string) *db.Task {
		task := placedTask(t, database, host, status)
		if err := database.UpdateTaskDaemonSession(task.ID, session); err != nil {
			t.Fatal(err)
		}
		if status == db.StatusBlocked {
			parkedFor(t, database, task.ID, idle)
		}
		if dir != "" {
			if err := database.SetTaskRemoteWorktree(task.ID, worktree(fmt.Sprintf("%d-%s", task.ID, dir)), "b"); err != nil {
				t.Fatal(err)
			}
		}
		return task
	}
	idle := placed("mona", db.StatusBlocked, 30*time.Hour, "fix-login")
	noRecord := placed("mona", db.StatusBlocked, 48*time.Hour, "")
	recent := placed("mona", db.StatusBlocked, time.Hour, "recent")
	running := placed("mona", db.StatusProcessing, 0, "running")
	typing := placed("mona", db.StatusBlocked, 30*time.Hour, "typing")
	asleep := placed("asleep", db.StatusBlocked, 30*time.Hour, "asleep")
	wt := func(task *db.Task) string {
		p, _, _ := database.GetTaskRemoteWorktree(task.ID)
		return p
	}

	agentCmd := func(task *db.Task) string {
		return fmt.Sprintf("sh -lc export WORKTREE_COORDINATOR_ID='%s' WORKTREE_RUN_ID='%s'; claude", coordinator, runOf(t, database, task.ID))
	}
	now := time.Now()
	w := func(host, sess, id string, taskID int64, suffix string, activity int64, cmd string) string {
		return strings.TrimSpace(fmt.Sprintf("%s %s %s task-%d%s %d %s", host, sess, id, taskID, suffix, activity, cmd))
	}
	old := now.Add(-30 * time.Hour).Unix()
	keep := []string{
		w("mona", session, "@5", recent.ID, "", old, agentCmd(recent)),
		w("mona", session, "@6", running.ID, "", old, agentCmd(running)),
		w("mona", session, "@7", typing.ID, "", old, agentCmd(typing)),
		w("mona", session, "@8", typing.ID, "-shell", now.Add(-time.Minute).Unix(), ""), // someone is typing here
		w("mona", "task-daemon-remote-someoneelse", "@10", idle.ID, "", old, ""),
		w("mona", "task-daemon-4242", "@11", idle.ID, "-shell", old, ""),
		w("mona", session, "@12", 10*idle.ID+3, "", old, ""), // task-13 when idle is task-1
		w("asleep", session, "@1", asleep.ID, "", old, ""),
	}
	sort.Strings(keep)
	if err := os.WriteFile(fake.state, []byte(strings.Join(append([]string{
		w("mona", session, "@1", idle.ID, "", old, agentCmd(idle)),
		w("mona", session, "@2", idle.ID, "-shell", old, ""),
		w("mona", session, "@3", noRecord.ID, "", old, "sh -lc claude"), // from before run IDs
	}, keep...), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := filepath.Join(mona, "run")
	ended := map[string]fakeProc{
		"idle task's dev server": tailIn(t, wt(idle), filepath.Join(wt(idle), "log", "dev.log")),
		"idle task's puma":       tailIn(t, wt(idle), filepath.Join(run, fmt.Sprintf("puma 6.4.2 [%d-fix-login]", idle.ID))),
	}
	// sidekiq, foreman, bin/dev: run in the worktree without naming it. Linux
	// finds them by working directory; elsewhere there is no /proc to ask.
	sidekiq := tailIn(t, wt(idle), filepath.Join(run, "sidekiq 7.2.0 app [0 of 5 busy]"))
	if runtime.GOOS == "linux" {
		ended["idle task's sidekiq"] = sidekiq
	}
	if err := os.WriteFile(filepath.Join(wt(idle), "pane.log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	paneShell := startProc(t, wt(idle), "sh", "-c", "tail -f "+shellQuote(filepath.Join(wt(idle), "pane.log"))+" & wait")
	if err := os.WriteFile(fake.panes, []byte(strconv.Itoa(paneShell.pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(mona, "other", ".task-worktrees", fmt.Sprintf("%d-theirs", idle.ID))
	kept := map[string]fakeProc{
		"another task's claude, its prompt naming the worktree": tailAs(t, "claude", run, filepath.Join(wt(idle), "prompt.txt")),
		"a tmux server whose command names the worktree":        tailAs(t, "tmux", wt(idle), filepath.Join(wt(idle), "tmux.conf")),
		"a process under a live tmux pane":                      paneShell,
		"another coordinator's task with the same ID":           tailIn(t, theirs, filepath.Join(theirs, "dev.log")),
		"a task whose ID only starts the same":                  tailIn(t, worktree(fmt.Sprintf("%d3-longer", idle.ID)), filepath.Join(worktree(fmt.Sprintf("%d3-longer", idle.ID)), "dev.log")),
		"[1-Oct-2026 text":                                      tailIn(t, run, filepath.Join(run, fmt.Sprintf("log [%d-Oct-2026] started", idle.ID))),
		"a task with no recorded worktree":                      tailIn(t, worktree(fmt.Sprintf("%d-no-record", noRecord.ID)), filepath.Join(worktree(fmt.Sprintf("%d-no-record", noRecord.ID)), "dev.log")),
		"recently blocked task's server":                        tailIn(t, wt(recent), filepath.Join(wt(recent), "dev.log")),
		"running task's server":                                 tailIn(t, wt(running), filepath.Join(wt(running), "dev.log")),
		"server of a task someone is typing in":                 tailIn(t, wt(typing), filepath.Join(wt(typing), "dev.log")),
		"unreachable host's server":                             tailIn(t, filepath.Join(fake.procs, "asleep"), filepath.Join(fake.procs, "asleep", "dev.log")),
	}
	if runtime.GOOS != "linux" {
		kept["sidekiq, which names nothing"] = sidekiq
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

	e.sweepRemoteSessions(context.Background(), now)

	if got := fake.windows(t); strings.Join(got, "\n") != strings.Join(keep, "\n") {
		t.Fatalf("windows after sweep:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(keep, "\n"))
	}
	for name, p := range ended {
		if p.alive() {
			t.Errorf("%s is still running", name)
		}
	}
	for name, p := range kept {
		if !p.alive() {
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
	logOf := func(task *db.Task) string {
		logs, _ := database.GetTaskLogs(task.ID, 50)
		var all []string
		for _, l := range logs {
			all = append(all, l.Content)
		}
		return strings.Join(all, "\n")
	}
	for _, task := range []*db.Task{idle, noRecord} {
		got, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != db.StatusBlocked || got.DaemonSession != "" {
			t.Errorf("task #%d: status %q, daemon session %q; want blocked with no session", task.ID, got.Status, got.DaemonSession)
		}
		if l := logOf(task); !strings.Contains(l, "suspended on mona") {
			t.Errorf("task #%d's log does not say it was suspended on mona:\n%s", task.ID, l)
		}
		if hasRun(t, database, task.ID) {
			t.Errorf("task #%d's run was kept after its suspend was finished", task.ID)
		}
	}
	if l := logOf(noRecord); !strings.Contains(l, "side processes on mona were left running") {
		t.Errorf("the task with no recorded worktree does not say its side processes were skipped:\n%s", l)
	}
	for _, task := range []*db.Task{recent, running, typing, asleep} {
		if got, _ := database.GetTask(task.ID); got.DaemonSession != session {
			t.Errorf("task #%d lost its session %q", task.ID, got.DaemonSession)
		}
		if strings.Contains(logOf(task), "suspended") {
			t.Errorf("task #%d's log says it was suspended", task.ID)
		}
	}

	// Nothing left to ask mona: the task in use is not asked about again until
	// it could be idle; the unreachable host backs off...
	before := len(fake.calls(t))
	e.sweepRemoteSessions(context.Background(), now.Add(time.Second))
	if after := fake.calls(t); len(after) != before {
		t.Fatalf("second sweep made ssh calls: %v", after[before:])
	}

	// ...until its backoff has passed, when it is tried again.
	e.sweepRemoteSessions(context.Background(), now.Add(remoteSessionEndMaxBackoff))
	var retried bool
	for _, call := range fake.calls(t)[before:] {
		retried = retried || strings.HasPrefix(call, "asleep ")
	}
	if !retried {
		t.Fatalf("unreachable host was not retried after its backoff: %v", fake.calls(t)[before:])
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
	wt := filepath.Join(fake.procs, "mona", "repo", ".task-worktrees", fmt.Sprintf("%d-x", task.ID))
	if err := database.SetTaskRemoteWorktree(task.ID, wt, "b"); err != nil {
		t.Fatal(err)
	}
	server := tailIn(t, wt, filepath.Join(wt, "dev.log"))

	e.sweepRemoteSessions(context.Background(), time.Now())

	if got := fake.windows(t); len(got) != 0 {
		t.Errorf("windows after sweep = %v, want none", got)
	}
	if !server.alive() {
		t.Error("side process was killed with reap_blocked_idle disabled")
	}
}

// A host where the sweep cannot look at processes must not be reported as one
// with nothing to end: the runs are kept, and retried.
func TestHostSweepFailsWithoutPs(t *testing.T) {
	bin := t.TempDir()
	for _, tool := range []string{"awk", "date", "id", "tr"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skip(tool + " not found")
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	script := hostSweepScript("task-daemon-remote-x", []string{"1:run:60"}, []string{"1:/srv/repo/.task-worktrees/1-x"})
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = []string{"PATH=" + bin}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("sweep without ps succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "ps is not installed") {
		t.Errorf("output = %q, want it to say ps is missing", out)
	}
}

// Replying to a task the idle sweep suspended on its host resumes the Claude
// conversation it had there, as a local retry does: the session ty recorded at
// launch when the host still has it, else the newest in the task's worktree.
func TestRetryOfSuspendedRemoteTaskResumesItsSession(t *testing.T) {
	newFakeRemoteTmux(t) // ssh runs the command here
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	worktree := filepath.Join(t.TempDir(), "repo", ".task-worktrees", "7-fix_login")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithRunner(context.Background(), RemoteRunner{Host: "mona"})

	if id, err := remoteClaudeSession(ctx, worktree, ""); id != "" || err != nil {
		t.Fatalf("session = %q, %v with none on the host, want none", id, err)
	}

	projects := filepath.Join(home, ".claude", "projects")
	escaped := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, real)
	older, newer := "11111111-2222-3333-4444-555555555555", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for i, name := range []string{older + ".jsonl", "agent-x.jsonl", newer + ".jsonl"} {
		file := filepath.Join(projects, escaped, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(time.Duration(i-3) * time.Hour)
		if name == "agent-x.jsonl" {
			at = time.Now() // newest, but a subagent's transcript, not a session
		}
		if err := os.Chtimes(file, at, at); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ stored, want string }{
		{"", newer},
		{older, older}, // the recorded one, though not the newest
		{"99999999-2222-3333-4444-555555555555", newer}, // recorded, but gone
	} {
		if id, err := remoteClaudeSession(ctx, worktree, c.stored); id != c.want || err != nil {
			t.Errorf("stored %q: session = %q, %v; want %q", c.stored, id, err, c.want)
		}
	}

	task := &db.Task{ID: 7, Title: "fix login"}
	script, err := remoteLaunchScriptWith(task, "claude", worktree, "the reply", "", claudeSession{ID: newer, Resume: true}, "run1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "--resume '"+newer+"' \"$(cat ") {
		t.Errorf("launch line does not resume the session with the reply:\n%s", script)
	}
	script, err = remoteLaunchScriptWith(task, "claude", worktree, "the task", "", claudeSession{ID: older}, "run1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "--session-id '"+older+"' ") || strings.Contains(script, "--resume") {
		t.Errorf("fresh launch line does not start the recorded session:\n%s", script)
	}
	if id, err := newSessionUUID(); err != nil || !remoteSessionIDRe.MatchString(id) {
		t.Errorf("newSessionUUID = %q, %v", id, err)
	}
}

// If ty cannot find out whether the host has the session — the host is not
// answering — a retry must fail and say so, not start a new conversation that
// has forgotten what the reply was about.
func TestRetryOfRemoteTaskBlocksWhenTheSessionCannotBeLookedUp(t *testing.T) {
	e, database := placementExecutor(t, t.TempDir())
	fake := newFakeRemoteTmux(t)
	task := placedTask(t, database, "asleep", db.StatusBlocked)

	for _, c := range []struct{ host, workDir string }{
		{"asleep", "/srv/repo/.task-worktrees/1-x"},     // unreachable
		{"mona", filepath.Join(t.TempDir(), "missing")}, // no worktree there
	} {
		res := e.runRemoteSession(context.Background(), task, RemoteRunner{Host: c.host, WorkDir: c.workDir}, "claude", "the task", "the reply")
		if res.Success || !strings.Contains(res.Message, "Could not reach "+c.host+" to resume") {
			t.Errorf("%s: result = %+v, want a failure saying the session could not be looked up", c.host, res)
		}
	}
	for _, call := range fake.calls(t) {
		if strings.Contains(call, "new-window") || strings.Contains(call, "claude ") {
			t.Errorf("a fresh agent was started: %s", call)
		}
	}
}

// `ty retry --replace` forgets a placement so the resolver is asked again. A
// remote run recorded its Claude session ID, and that session is on the old
// host: left in the row, HasLocalState read it as a local first attempt and
// pinned the task to this machine, so a remote Claude task "replaced" onto the
// Mac. A local placement's session is here, and is kept.
func TestReplacingARemoteTaskForgetsItsRemoteSession(t *testing.T) {
	database := hostsTestDB(t)
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	remote := newTask(t, database, "claude")
	local := newTask(t, database, "claude")
	for _, c := range []struct {
		task   *db.Task
		target string
	}{{remote, "mona"}, {local, ""}} {
		if err := database.SetTaskPlacementDecision(c.task.ID, c.target, "test", "/srv/repo"); err != nil {
			t.Fatal(err)
		}
		if err := database.UpdateTaskClaudeSessionID(c.task.ID, sid); err != nil {
			t.Fatal(err)
		}
		if err := database.ClearTaskPlacement(c.task.ID); err != nil {
			t.Fatal(err)
		}
	}

	got, err := database.GetTask(remote.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClaudeSessionID != "" {
		t.Errorf("remote task kept its host's session %q after --replace", got.ClaudeSessionID)
	}
	if why, pinned := HasLocalState(got); pinned {
		t.Errorf("remote task is pinned to this machine after --replace (%s); the resolver would never be asked", why)
	}
	if got, _ := database.GetTask(local.ID); got.ClaudeSessionID != sid {
		t.Errorf("local task's session = %q after --replace, want %q kept", got.ClaudeSessionID, sid)
	}
}

// The agent window's start command names its run. One naming a different run
// is spared only while that could be a newer run's — one a reply started after
// the sweep read the database. Re-read afterwards: if the database still has
// the sweep's run, the window can only be left from an older one, and the next
// sweep ends it and suspends the task. If the run has moved on, the window is
// the new run's, and is left alone.
func TestRemoteIdleSuspendIsScopedToTheRun(t *testing.T) {
	e, database := placementExecutor(t, t.TempDir())
	coordinator, err := database.CoordinatorID()
	if err != nil {
		t.Fatal(err)
	}
	session := remoteDaemonSessionName(coordinator)
	idleTask := func() *db.Task {
		task := placedTask(t, database, "mona", db.StatusBlocked)
		if err := database.UpdateTaskDaemonSession(task.ID, session); err != nil {
			t.Fatal(err)
		}
		parkedFor(t, database, task.ID, 30*time.Hour)
		return task
	}

	// The window names a run the database does not have, and the database
	// still has the sweep's run: an older run's window.
	task := idleTask()
	window := fmt.Sprintf("mona %s @1 task-%d 0 sh -lc export WORKTREE_RUN_ID='an-older-run'; claude", session, task.ID)
	fake := newFakeRemoteTmux(t, window)
	now := time.Now()

	e.sweepRemoteSessions(context.Background(), now)
	if got := fake.windows(t); len(got) != 1 {
		t.Fatalf("first sweep ended %v; the window could have been a newer run's", got)
	}
	if !e.sessionEnd.awake(task.ID, now.Add(time.Second)) {
		t.Fatal("an older run's window put the task to sleep instead of being ended next sweep")
	}
	e.sweepRemoteSessions(context.Background(), now.Add(time.Second))
	if got := fake.windows(t); len(got) != 0 {
		t.Fatalf("windows after the second sweep = %v; the older run's window should be ended", got)
	}
	if got, _ := database.GetTask(task.ID); got.DaemonSession != "" {
		t.Errorf("task #%d was not suspended after its older window was ended", task.ID)
	}

	// The run moved on between the sweep's read and the host's answer: the
	// window is the new run's, and is not ended, now or later.
	resumed := idleTask()
	stale := idleRemoteRun{RemoteRun: db.RemoteRun{TaskID: resumed.ID, RunID: runOf(t, database, resumed.ID), Host: "mona"}, HadSession: true}
	if _, err := database.BeginRemoteRun(resumed.ID, "mona"); err != nil {
		t.Fatal(err)
	}
	e.recordRemoteSuspend("mona", stale, remoteEnded{otherRun: map[int64]bool{resumed.ID: true}}, now)
	if e.sessionEnd.isOlder(resumed.ID, stale.RunID) {
		t.Error("a newer run's window was taken for an older run's, and would be ended")
	}
	if e.sessionEnd.awake(resumed.ID, now.Add(time.Second)) {
		t.Error("a task whose window is a newer run's is asked about again at once")
	}
	if got, _ := database.GetTask(resumed.ID); got.DaemonSession != session {
		t.Errorf("task #%d lost its session %q to a sweep that left its window alone", resumed.ID, got.DaemonSession)
	}
}
