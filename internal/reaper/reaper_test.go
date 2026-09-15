package reaper

import (
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var now = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

// wtRoot is a fake project root used to build worktree paths in test fixtures.
const wtRoot = "/Users/b/Projects/ik/.task-worktrees/"

func basePolicy() Policy {
	return Policy{
		Now:                  now,
		DoneGrace:            DefaultDoneGrace,
		BlockedIdle:          DefaultBlockedIdle,
		OrphanMinAge:         DefaultOrphanMinAge,
		ReapOrphanDevServers: true,
	}
}

// devServer builds a process rooted in a task worktree, the way a leaked
// webpack-dev-server actually looks in ps.
func devServer(pid, ppid, taskID int, age time.Duration) Process {
	return Process{
		PID:  pid,
		PPID: ppid,
		TTY:  "??",
		Age:  age,
		Command: "node " + wtRoot + strconv.Itoa(taskID) +
			"-creator-referral-v2/node_modules/.bin/webpack-dev-server",
	}
}

func find(t *testing.T, decisions []Decision, pid int) Decision {
	t.Helper()
	for _, d := range decisions {
		if d.Process.PID == pid {
			return d
		}
	}
	t.Fatalf("no decision for pid %d (got %d decisions)", pid, len(decisions))
	return Decision{}
}

func TestTaskIDFor(t *testing.T) {
	cases := map[string]int{
		"node " + wtRoot + "5119-creator-referral/node_modules/.bin/webpack-dev-server": 5119,
		"/bin/zsh -c cd " + wtRoot + "42-fix-thing && bin/dev":                          42,
		"node /private/tmp/ik-oauth-visibility/node_modules/.bin/webpack-dev-server":    0,
		"claude --resume":                 0,
		wtRoot + "sessions/task-99.jsonl": 0,
	}
	for cmd, want := range cases {
		if got := TaskIDFor(cmd); got != want {
			t.Errorf("TaskIDFor(%q) = %d, want %d", cmd, got, want)
		}
	}
}

// A dev server reparented to launchd (PPID 1) whose task is long done is the
// canonical orphan: no window, no parent, nothing left to SIGHUP it.
func TestPlan_ReparentedOrphanForDoneTask(t *testing.T) {
	procs := []Process{devServer(900, 1, 5119, 30*time.Hour)}
	tasks := map[int]TaskState{5119: {
		Exists: true, Status: "done", LastActivity: now.Add(-30 * time.Hour),
		WorktreeDir: "5119-creator-referral-v2",
	}}

	d := find(t, Plan(procs, tasks, nil, basePolicy()), 900)
	if !d.Reap {
		t.Fatalf("expected reap, got keep: %s", d)
	}
	if d.Rule != RuleTaskDone {
		t.Errorf("rule = %q, want %q", d.Rule, RuleTaskDone)
	}
}

// Trashed tasks are dead work: reaped on the done-task rules.
func TestPlan_TrashedTaskIsReapedLikeDone(t *testing.T) {
	procs := []Process{devServer(900, 1, 5119, 30*time.Hour)}
	tasks := map[int]TaskState{5119: {Exists: true, Status: "trashed", LastActivity: now.Add(-5 * time.Hour)}}

	if d := find(t, Plan(procs, tasks, nil, basePolicy()), 900); !d.Reap || d.Rule != RuleTaskDone {
		t.Fatalf("trashed task past the grace should be reaped as done: %s", d)
	}
}

// A task absent from this database may belong to another machine that placed
// it here. Absence is not deletion: its processes are left alone unless they
// independently pass the detached-dev-server test.
func TestPlan_TaskNotInDatabaseIsNotOurs(t *testing.T) {
	young := []Process{devServer(900, 1, 5119, 3*time.Hour)}
	d := find(t, Plan(young, map[int]TaskState{}, nil, basePolicy()), 900)
	if d.Reap {
		t.Fatalf("process for a task not in this database must not be reaped on absence alone: %s", d)
	}
	if d.Rule != RuleUnknownTask {
		t.Errorf("rule = %q, want %q", d.Rule, RuleUnknownTask)
	}

	old := []Process{devServer(901, 1, 5119, 30*time.Hour)}
	d = find(t, Plan(old, map[int]TaskState{}, nil, basePolicy()), 901)
	if !d.Reap || d.Rule != RuleOrphanDetach {
		t.Errorf("an old detached dev server is still reaped on the task-agnostic rule: %s", d)
	}
	if d.TaskID != 5119 {
		t.Errorf("taskID = %d, want 5119 kept for the log line", d.TaskID)
	}
}

// A local task that shares an ID with a foreign one has a different recorded
// worktree. Its status says nothing about the foreign task's processes.
func TestPlan_TaskIDCollisionWithDifferentWorktreeIsNotOurs(t *testing.T) {
	procs := []Process{devServer(900, 1, 5119, 3*time.Hour)}
	tasks := map[int]TaskState{5119: {
		Exists: true, Status: "done", LastActivity: now.Add(-30 * time.Hour),
		WorktreeDir: "5119-some-other-local-task",
	}}

	d := find(t, Plan(procs, tasks, nil, basePolicy()), 900)
	if d.Reap {
		t.Fatalf("a colliding task ID must not get another worktree's process reaped: %s", d)
	}
	if d.Rule != RuleUnknownTask {
		t.Errorf("rule = %q, want %q", d.Rule, RuleUnknownTask)
	}
}

// A process still living under a live tmux pane is by definition not orphaned,
// no matter what the task's status says.
func TestPlan_ProcessStillInPaneIsSpared(t *testing.T) {
	// pane shell (700) -> npm (800) -> dev server (900)
	procs := []Process{
		{PID: 700, PPID: 500, TTY: "s001", Age: 40 * time.Hour, Command: "-zsh"},
		{PID: 800, PPID: 700, TTY: "s001", Age: 40 * time.Hour, Command: "npm run dev " + wtRoot + "5119-x"},
		devServer(900, 800, 5119, 40*time.Hour),
	}
	tasks := map[int]TaskState{5119: {Exists: true, Status: "blocked", LastActivity: now.Add(-40 * time.Hour)}}

	decisions := Plan(procs, tasks, map[int]bool{700: true}, basePolicy())

	d := find(t, decisions, 900)
	if d.Reap {
		t.Fatalf("in-pane process must not be reaped: %s", d)
	}
	if d.Rule != RuleLivePane {
		t.Errorf("rule = %q, want %q", d.Rule, RuleLivePane)
	}
}

// blocked usually means "waiting for a human". Under the idle threshold the
// side process is left completely alone.
func TestPlan_BlockedTaskUnderThresholdIsLeftAlone(t *testing.T) {
	procs := []Process{devServer(900, 1, 5119, 30*time.Hour)}
	tasks := map[int]TaskState{5119: {Exists: true, Status: "blocked", LastActivity: now.Add(-3 * time.Hour)}}

	d := find(t, Plan(procs, tasks, nil, basePolicy()), 900)
	if d.Reap {
		t.Fatalf("blocked task active 3h ago must not be reaped: %s", d)
	}
	if d.Rule != RuleIdleGrace {
		t.Errorf("rule = %q, want %q", d.Rule, RuleIdleGrace)
	}
}

// Staleness is measured from last activity, not from when the task entered
// blocked: a long-blocked task that was touched an hour ago is still live.
func TestPlan_BlockedTaskStalenessUsesLastActivityNotStatusAge(t *testing.T) {
	procs := []Process{devServer(900, 1, 5119, 20*24*time.Hour)}
	tasks := map[int]TaskState{5119: {Exists: true, Status: "blocked", LastActivity: now.Add(-time.Hour)}}

	if d := find(t, Plan(procs, tasks, nil, basePolicy()), 900); d.Reap {
		t.Fatalf("recent activity must protect a long-blocked task: %s", d)
	}
}

// Past the idle threshold the side process is reaped.
func TestPlan_BlockedTaskOverThresholdIsReaped(t *testing.T) {
	procs := []Process{devServer(900, 1, 5119, 4*24*time.Hour)}
	tasks := map[int]TaskState{5119: {Exists: true, Status: "blocked", LastActivity: now.Add(-30 * time.Hour)}}

	d := find(t, Plan(procs, tasks, nil, basePolicy()), 900)
	if !d.Reap {
		t.Fatalf("blocked task idle 30h should be reaped: %s", d)
	}
	if d.Rule != RuleStaleIdle {
		t.Errorf("rule = %q, want %q", d.Rule, RuleStaleIdle)
	}
}

// The done-task grace (2h) must never be applied to a blocked task.
func TestPlan_BlockedTaskNotReapedOnDoneGrace(t *testing.T) {
	procs := []Process{devServer(900, 1, 5119, 10*time.Hour)}
	tasks := map[int]TaskState{5119: {Exists: true, Status: "blocked", LastActivity: now.Add(-3 * time.Hour)}}

	p := basePolicy()
	if p.BlockedIdle <= p.DoneGrace {
		t.Fatalf("blocked idle threshold (%s) must be well above the done grace (%s)", p.BlockedIdle, p.DoneGrace)
	}
	if d := find(t, Plan(procs, tasks, nil, p), 900); d.Reap {
		t.Fatalf("blocked task idle 3h must survive the 2h done grace: %s", d)
	}
}

// The agent itself is never reaped by staleness — only side processes are.
func TestPlan_AgentProcessForStaleBlockedTaskIsSpared(t *testing.T) {
	procs := []Process{{
		PID: 901, PPID: 1, TTY: "??", Age: 5 * 24 * time.Hour,
		Command: "claude --resume abc " + wtRoot + "5119-creator-referral-v2",
	}}
	tasks := map[int]TaskState{5119: {Exists: true, Status: "blocked", LastActivity: now.Add(-10 * 24 * time.Hour)}}

	d := find(t, Plan(procs, tasks, nil, basePolicy()), 901)
	if d.Reap {
		t.Fatalf("agent process must not be reaped for a stale blocked task: %s", d)
	}
	if d.Rule != RuleAgentSpared {
		t.Errorf("rule = %q, want %q", d.Rule, RuleAgentSpared)
	}
	if !d.IsAgent {
		t.Error("expected the decision to be flagged as an agent process")
	}
}

// A long-done task has nobody to return to it, so even its agent goes.
func TestPlan_AgentProcessForDoneTaskIsReaped(t *testing.T) {
	procs := []Process{{
		PID: 901, PPID: 1, TTY: "??", Age: 5 * time.Hour,
		Command: "claude --resume abc " + wtRoot + "5119-gone",
	}}
	tasks := map[int]TaskState{5119: {Exists: true, Status: "done", LastActivity: now.Add(-5 * time.Hour)}}
	if d := find(t, Plan(procs, tasks, nil, basePolicy()), 901); !d.Reap {
		t.Fatalf("agent for a long-done task should be reaped: %s", d)
	}
}

// An agent for a task not in this database is never reaped: it is most likely
// a live agent placed here from another machine.
func TestPlan_AgentProcessForUnknownTaskIsSpared(t *testing.T) {
	procs := []Process{{
		PID: 901, PPID: 1, TTY: "??", Age: 50 * time.Hour,
		Command: "claude --resume abc " + wtRoot + "5119-placed",
	}}
	if d := find(t, Plan(procs, map[int]TaskState{}, nil, basePolicy()), 901); d.Reap {
		t.Fatalf("agent for a task not in this database must never be reaped: %s", d)
	}
}

func TestPlan_DoneTaskGrace(t *testing.T) {
	procs := []Process{devServer(900, 1, 5119, 10*time.Hour)}

	fresh := map[int]TaskState{5119: {Exists: true, Status: "done", LastActivity: now.Add(-30 * time.Minute)}}
	if d := find(t, Plan(procs, fresh, nil, basePolicy()), 900); d.Reap {
		t.Errorf("done 30m ago is inside the grace: %s", d)
	}

	old := map[int]TaskState{5119: {Exists: true, Status: "done", LastActivity: now.Add(-5 * time.Hour)}}
	d := find(t, Plan(procs, old, nil, basePolicy()), 900)
	if !d.Reap {
		t.Errorf("done 5h ago is past the grace: %s", d)
	}
	if d.Rule != RuleTaskDone {
		t.Errorf("rule = %q, want %q", d.Rule, RuleTaskDone)
	}
}

func TestPlan_ActiveTaskIsNeverReaped(t *testing.T) {
	for _, status := range []string{"processing", "queued"} {
		procs := []Process{devServer(900, 1, 5119, 40*time.Hour)}
		tasks := map[int]TaskState{5119: {Exists: true, Status: status, LastActivity: now.Add(-40 * time.Hour)}}
		if d := find(t, Plan(procs, tasks, nil, basePolicy()), 900); d.Reap {
			t.Errorf("%s task must never be reaped: %s", status, d)
		}
	}
}

// The /private/tmp case from the incident: a dev server with no worktree on its
// command line at all, reparented to launchd, no terminal, days old.
func TestPlan_NoWorktreeTempDirOrphan(t *testing.T) {
	proc := Process{
		PID: 910, PPID: 1, TTY: "??", Age: 4*24*time.Hour + 20*time.Hour,
		Command: "node /private/tmp/ik-oauth-visibility/node_modules/.bin/webpack-dev-server",
	}
	d := find(t, Plan([]Process{proc}, nil, nil, basePolicy()), 910)
	if !d.Reap {
		t.Fatalf("detached temp-dir dev server should be reaped: %s", d)
	}
	if d.Rule != RuleOrphanDetach {
		t.Errorf("rule = %q, want %q", d.Rule, RuleOrphanDetach)
	}
	if d.TaskID != 0 {
		t.Errorf("taskID = %d, want 0 (no worktree to map back to)", d.TaskID)
	}
}

func TestPlan_NoWorktreeOrphanGuards(t *testing.T) {
	cmd := "node /private/tmp/ik/node_modules/.bin/vite"

	young := Process{PID: 911, PPID: 1, TTY: "??", Age: 2 * time.Hour, Command: cmd}
	if d := find(t, Plan([]Process{young}, nil, nil, basePolicy()), 911); d.Reap {
		t.Errorf("a 2h-old detached dev server is too young to reap: %s", d)
	}

	attached := Process{PID: 912, PPID: 4321, TTY: "s004", Age: 40 * time.Hour, Command: cmd}
	if d := find(t, Plan([]Process{attached}, nil, nil, basePolicy()), 912); d.Reap {
		t.Errorf("a dev server with a parent and a terminal is somebody's: %s", d)
	}

	p := basePolicy()
	p.ReapOrphanDevServers = false
	old := Process{PID: 913, PPID: 1, TTY: "??", Age: 40 * time.Hour, Command: cmd}
	if d := find(t, Plan([]Process{old}, nil, nil, p), 913); d.Reap {
		t.Errorf("detached sweep is disabled, nothing should be reaped: %s", d)
	}
}

// Unrelated detached processes are not dev servers and must never be considered.
func TestPlan_UnrelatedProcessesAreNotConsidered(t *testing.T) {
	procs := []Process{
		{PID: 920, PPID: 1, TTY: "??", Age: 300 * time.Hour, Command: "/usr/libexec/secd"},
		{PID: 921, PPID: 1, TTY: "??", Age: 300 * time.Hour, Command: "/Applications/Docker.app/Contents/MacOS/Docker"},
	}
	if got := Plan(procs, nil, nil, basePolicy()); len(got) != 0 {
		t.Fatalf("expected no decisions for unrelated processes, got %v", got)
	}
}

// `ty sessions cleanup` is routinely run by an agent from inside its own task
// worktree. It must not be able to kill the process running it.
func TestPlan_SelfAndAncestorsAreProtected(t *testing.T) {
	procs := []Process{
		{PID: 800, PPID: 1, TTY: "??", Age: 40 * time.Hour, Command: "claude " + wtRoot + "5119-x"},
		{PID: 801, PPID: 800, TTY: "??", Age: 40 * time.Hour, Command: "node " + wtRoot + "5119-x/node_modules/.bin/vite"},
	}
	p := basePolicy()
	p.Protected = map[int]bool{800: true, 801: true}

	for _, pid := range []int{800, 801} {
		d := find(t, Plan(procs, map[int]TaskState{}, nil, p), pid)
		if d.Reap {
			t.Errorf("pid %d is protected but was reaped: %s", pid, d)
		}
		if d.Rule != RuleProtected {
			t.Errorf("pid %d rule = %q, want %q", pid, d.Rule, RuleProtected)
		}
	}
}

// A task with no activity record at all falls back to the process's own age, so
// a freshly spawned side process is never caught by the first sweep.
func TestPlan_NoActivityRecordFallsBackToProcessAge(t *testing.T) {
	tasks := map[int]TaskState{5119: {Exists: true, Status: "backlog"}}

	young := []Process{devServer(900, 1, 5119, time.Hour)}
	if d := find(t, Plan(young, tasks, nil, basePolicy()), 900); d.Reap {
		t.Errorf("1h-old process for a task with no activity record should survive: %s", d)
	}

	old := []Process{devServer(900, 1, 5119, 40*time.Hour)}
	if d := find(t, Plan(old, tasks, nil, basePolicy()), 900); !d.Reap {
		t.Errorf("40h-old process for a task with no activity record should be reaped: %s", d)
	}
}

// A scoped sweep (ty sessions suspend) only judges the tasks it was given.
func TestPlan_OnlyTasksScopesTheSweep(t *testing.T) {
	procs := []Process{
		devServer(900, 1, 5119, 40*time.Hour),
		devServer(901, 1, 5120, 40*time.Hour),
		{PID: 902, PPID: 1, TTY: "??", Age: 100 * time.Hour, Command: "node /private/tmp/x/node_modules/.bin/vite"},
	}
	p := basePolicy()
	p.OnlyTasks = map[int]bool{5119: true}

	decisions := Plan(procs, map[int]TaskState{}, nil, p)
	if len(decisions) != 1 || decisions[0].Process.PID != 900 {
		t.Fatalf("expected only pid 900 to be considered, got %v", decisions)
	}
}

// `ty sessions suspend 42` is an explicit teardown: the caution that protects
// live-looking tasks from a guessing sweep doesn't apply, because nobody is
// guessing. It still only touches the named tasks.
func TestPlan_ExplicitTeardownIgnoresStatusCaution(t *testing.T) {
	procs := []Process{
		devServer(900, 1, 5119, time.Minute),
		{PID: 901, PPID: 1, TTY: "??", Age: time.Minute, Command: "claude --resume abc " + wtRoot + "5119-x"},
		devServer(902, 1, 5120, 40*time.Hour),
	}
	tasks := map[int]TaskState{
		5119: {Exists: true, Status: "processing", LastActivity: now},
		5120: {Exists: true, Status: "blocked", LastActivity: now.Add(-40 * time.Hour)},
	}
	p := basePolicy()
	p.OnlyTasks = map[int]bool{5119: true}
	p.Explicit = true
	p.DoneGrace, p.BlockedIdle = 0, 0

	decisions := Plan(procs, tasks, nil, p)
	if len(decisions) != 2 {
		t.Fatalf("expected only task 5119's processes to be considered, got %v", decisions)
	}
	for _, pid := range []int{900, 901} {
		d := find(t, decisions, pid)
		if !d.Reap {
			t.Errorf("pid %d should be reaped by an explicit teardown: %s", pid, d)
		}
		if d.Rule != RuleExplicit {
			t.Errorf("pid %d rule = %q, want %q", pid, d.Rule, RuleExplicit)
		}
	}
}

// Explicit teardown never overrides the two hard invariants.
func TestPlan_ExplicitTeardownStillRespectsProtectedAndLivePane(t *testing.T) {
	procs := []Process{
		{PID: 700, PPID: 500, TTY: "s001", Age: time.Hour, Command: "-zsh"},
		devServer(900, 700, 5119, time.Hour), // under a live pane
		devServer(901, 1, 5119, time.Hour),   // the sweeping process itself
	}
	p := basePolicy()
	p.OnlyTasks = map[int]bool{5119: true}
	p.Explicit = true
	p.Protected = map[int]bool{901: true}

	decisions := Plan(procs, map[int]TaskState{}, map[int]bool{700: true}, p)
	if d := find(t, decisions, 900); d.Reap || d.Rule != RuleLivePane {
		t.Errorf("in-pane process must survive an explicit teardown: %s", d)
	}
	if d := find(t, decisions, 901); d.Reap || d.Rule != RuleProtected {
		t.Errorf("protected process must survive an explicit teardown: %s", d)
	}
}

func TestParseElapsed(t *testing.T) {
	cases := map[string]time.Duration{
		"01:23":      time.Minute + 23*time.Second,
		"10:01:23":   10*time.Hour + time.Minute + 23*time.Second,
		"4-20:15:30": 4*24*time.Hour + 20*time.Hour + 15*time.Minute + 30*time.Second,
		"  02:00  ":  2 * time.Minute,
	}
	for in, want := range cases {
		got, ok := ParseElapsed(in)
		if !ok || got != want {
			t.Errorf("ParseElapsed(%q) = %v,%v want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "1:2:3:4", "12"} {
		if _, ok := ParseElapsed(bad); ok {
			t.Errorf("ParseElapsed(%q) should have failed", bad)
		}
	}
}

func TestParsePS(t *testing.T) {
	out := "  900     1 ??       4-20:15:30 node " + wtRoot + "5119-x/node_modules/.bin/webpack-dev-server --port 3035\n" +
		"  901   900 s001        01:02 /bin/zsh -c echo hi\n" +
		" junk line\n" +
		"  902     1 ??      malformed some command\n"

	procs := ParsePS(out)
	if len(procs) != 2 {
		t.Fatalf("expected 2 parsed rows, got %d: %+v", len(procs), procs)
	}
	if procs[0].PID != 900 || procs[0].PPID != 1 || procs[0].HasTTY() {
		t.Errorf("row 0 parsed wrong: %+v", procs[0])
	}
	if procs[0].Age != 4*24*time.Hour+20*time.Hour+15*time.Minute+30*time.Second {
		t.Errorf("row 0 age = %v", procs[0].Age)
	}
	if !strings.HasSuffix(procs[0].Command, "--port 3035") {
		t.Errorf("row 0 lost part of the command: %q", procs[0].Command)
	}
	if !procs[1].HasTTY() {
		t.Errorf("row 1 should have a tty: %+v", procs[1])
	}
}

// Reap escalates to SIGKILL only for processes that outlive the grace period.
func TestReap_TermThenKill(t *testing.T) {
	stubborn := 900
	polite := 901
	alive := map[int]bool{stubborn: true, polite: true}
	var sent []string

	sig := func(pid int, s syscall.Signal) error {
		switch s {
		case syscall.Signal(0):
			if !alive[pid] {
				return errors.New("no such process")
			}
			return nil
		case syscall.SIGTERM:
			sent = append(sent, "TERM")
			if pid == polite {
				alive[pid] = false
			}
			return nil
		case syscall.SIGKILL:
			sent = append(sent, "KILL")
			alive[pid] = false
			return nil
		}
		return nil
	}

	decisions := []Decision{
		{Process: Process{PID: stubborn}, Reap: true},
		{Process: Process{PID: polite}, Reap: true},
		{Process: Process{PID: 902}, Reap: false},
	}
	results := Reap(decisions, 300*time.Millisecond, sig)

	if len(results) != 2 {
		t.Fatalf("expected 2 results (the non-reaped decision is skipped), got %d", len(results))
	}
	if results[0].Signal != "KILL" {
		t.Errorf("stubborn process should have been SIGKILLed, got %q", results[0].Signal)
	}
	if results[1].Signal != "TERM" {
		t.Errorf("polite process should have exited on SIGTERM, got %q", results[1].Signal)
	}
	if alive[stubborn] || alive[polite] {
		t.Error("both processes should be gone")
	}
	if len(sent) != 3 {
		t.Errorf("expected TERM,TERM,KILL — got %v", sent)
	}
}
