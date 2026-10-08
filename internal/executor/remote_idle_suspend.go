package executor

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/reaper"
)

// Suspending a placed task that has sat blocked too long.
//
// Locally, suspendIdleBlockedTasks ends a parked agent once its task has been
// blocked for idle_suspend_timeout, keeping the Claude session for a resume. It
// finds the agent through this machine's tmux and ps, so a task placed on
// another host — whose window lives in that host's task-daemon-remote-<coordinator>
// session — was never found, and never suspended. Neither were the dev servers
// it had started there, which outlive the window.
//
// So the remote sweep (remote_session_end.go) does it, over the same one ssh
// call per host and with the same backoff:
//
//   - Once a placed task has been blocked longer than idle_suspend_timeout, its
//     task-<id> and task-<id>-shell windows in this coordinator's session on its
//     host are ended, and its tmux placement is cleared, as SuspendTaskSession
//     does. The task stays blocked; replying to it resumes the conversation on
//     the same host (see remoteClaudeSession).
//   - Not if someone is using it. Blocked is the database's word; a human can
//     be typing in the pane. A task whose window saw activity within the
//     timeout is left alone, and not asked about again until it could be idle.
//   - Not if the window belongs to a newer run of the task (a reply that
//     resumed it between the sweep's read and its ssh call): the agent window's
//     start command names its run.
//   - Its side processes are ended in the same call if it has also been idle for
//     reap_blocked_idle, the threshold `ty sessions cleanup` uses for them; with
//     that setting disabled they are left running. They are only ever looked
//     for in the worktree ty recorded for the task; a task with none recorded
//     has its windows ended and nothing else, and the log says so.
//
// The host is shared. On a fleet host ty usually logs in as the same user that
// runs the host's own TaskYou and other coordinators' tasks, so the side-process
// pass is deliberately narrow (see hostSweepProcs): only this user's processes,
// never an agent or tmux, never anything under a live tmux pane, and on Linux
// matched by working directory rather than by text on a command line.
//
// Only processes are ended. The worktree on the host, and everything in it, is
// left exactly as it is.

// idleRemoteRun is a blocked, placed task the sweep is due to act on.
type idleRemoteRun struct {
	db.RemoteRun
	Idle    time.Duration
	Timeout time.Duration
	// HadSession: the task still has a session on its host, as far as the
	// database knows. Its windows are looked for either way.
	HadSession bool
	// Reap: side processes are ended for blocked tasks at all
	// (reap_blocked_idle is not disabled). Due: and this one's are due now.
	Reap, Due bool
	// Worktree is the task's worktree on the host, as ty recorded it, or "" when
	// there is no usable record. Side processes are matched to it and nothing
	// else.
	Worktree string
}

// idleRemoteRuns lists the blocked, placed tasks the sweep should act on now.
func (e *Executor) idleRemoteRuns(now time.Time) []idleRemoteRun {
	runs, err := e.db.BlockedRemoteRuns()
	if err != nil {
		e.logger.Debug("Failed to list blocked remote runs", "error", err)
		return nil
	}
	if len(runs) == 0 {
		return nil
	}
	timeout := e.getSuspendIdleTimeout()
	reapAfter, reap := e.getReapBlockedIdle()

	var due []idleRemoteRun
	for _, run := range runs {
		if !e.sessionEnd.awake(run.TaskID, now) {
			continue
		}
		task, err := e.db.GetTask(run.TaskID)
		if err != nil || task == nil {
			continue
		}
		idle, ok := blockedIdleDuration(task, now)
		if !ok || idle < timeout {
			continue
		}
		wt, _, _ := e.db.GetTaskRemoteWorktree(task.ID)
		r := idleRemoteRun{
			RemoteRun:  run,
			Idle:       idle,
			Timeout:    timeout,
			HadSession: task.DaemonSession != "",
			Reap:       reap,
			Due:        reap && idle >= reapAfter,
			Worktree:   recordedWorktree(task.ID, wt),
		}
		if !r.HadSession && !r.sideProcesses() {
			continue // already suspended; side processes not due (or never)
		}
		due = append(due, r)
	}
	return due
}

// sideProcesses reports whether this pass ends the run's side processes.
func (r idleRemoteRun) sideProcesses() bool { return r.Due && r.Worktree != "" }

// recordedWorktreeRe is an absolute worktree path as setupRemoteWorktree makes
// it, with nothing a shell or awk would read specially.
var recordedWorktreeRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// recordedWorktree returns the task's recorded remote worktree when it is one
// side processes can safely be matched against: an absolute path to a
// .task-worktrees/<id>-<slug> directory. Anything else is "", and the task's
// side processes are then left alone: "<id>-" alone names every coordinator's
// task of that ID on the host, and much else besides.
func recordedWorktree(taskID int64, worktree string) string {
	if !recordedWorktreeRe.MatchString(worktree) || strings.Contains(worktree, "/../") {
		return ""
	}
	dir := path.Base(worktree)
	if path.Base(path.Dir(worktree)) != ".task-worktrees" || !strings.HasPrefix(dir, fmt.Sprintf("%d-", taskID)) || len(dir) <= len(fmt.Sprintf("%d-", taskID)) {
		return ""
	}
	return path.Clean(worktree)
}

// hostSweepWindows is the awk program that plans the window half of a sweep. It
// reads `tmux list-windows -F '#{window_id} #{window_name} #{window_activity}
// #{pane_start_command}'` for this coordinator's session and, for each task in
// specs ("<task>:<run>:<timeout seconds>", a timeout of 0 for a finished task),
// prints:
//
//	K <window id> <window name>  end this window
//	A <task> <seconds>           in use: a window saw activity within the
//	                             timeout; the task could be idle in <seconds>
//	F <task>                     its agent window belongs to another run
//
// A task that is A or F has none of its windows ended. An agent window whose
// start command names no run at all predates run IDs and counts as this run's.
//
//nolint:gosec // G101: an awk program, not a credential
const hostSweepWindows = `
BEGIN {
  n = split(specs, a, " ")
  for (i = 1; i <= n; i++) {
    split(a[i], f, ":"); t = f[1]; run[t] = f[2]; to[t] = f[3] + 0
    want["task-" t] = t; want["task-" t "-shell"] = t
  }
}
NF >= 2 && ($2 in want) {
  t = want[$2]; k++; wid[k] = $1; wname[k] = $2; wtask[k] = t
  if ($2 == "task-" t && index($0, "WORKTREE_RUN_ID") && !index($0, run[t])) foreign[t] = 1
  act = $3 + 0
  if (to[t] > 0 && act + to[t] > now) { left = act + to[t] - now; if (left > wait[t]) wait[t] = left }
}
END {
  for (t in run) { if (foreign[t]) print "F", t; else if (wait[t] > 0) print "A", t, wait[t] }
  for (i = 1; i <= k; i++) { t = wtask[i]; if (!foreign[t] && !(wait[t] > 0)) print "K", wid[i], wname[i] }
}`

// hostSweepProcs is the awk program that picks the side processes to end. It
// reads `ps -o pid=,ppid=,args=` for this user only, and prints "<task> <pid>"
// for each process that belongs to a task in specs ("<task>:<worktree>"), unless
// that task is in skip.
//
// A process belongs to a task when, on Linux, its working directory is in the
// task's worktree (which also finds sidekiq, foreman and bin/dev, whose command
// lines do not name it); elsewhere, when its command line names the worktree
// path, or carries a puma title "[<dir>]".
//
// Never chosen, whatever matches: a coding agent or tmux (an agent of another
// task can have this path in its prompt, a tmux server in its command), anything
// with a live tmux pane — panes — as itself or an ancestor (a process someone
// is running in another window), and this script, its ancestors and its
// children, whose command lines carry the specs.
const hostSweepProcs = `
function base(w) { sub(/.*\//, "", w); return w }
function agentish(s,   n, w, i, b) {
  n = split(s, w, " ")
  for (i = 1; i <= n; i++) {
    b = base(w[i])
    if (b in never || index(w[i], "claude-code") || index(w[i], "@openai/codex")) return 1
  }
  return 0
}
function named(s, pat,   i, c) {
  i = index(s, pat); if (!i) return 0
  c = substr(s, i + length(pat), 1)
  return c == "" || c !~ /[A-Za-z0-9_.-]/
}
function within(dir, wt) { return dir == wt || index(dir, wt "/") == 1 }
function resolve(p,   r) { r = ""; ("cd '" p "' 2>/dev/null && pwd -P") | getline r; close("cd '" p "' 2>/dev/null && pwd -P"); return r }
BEGIN {
  split("claude codex gemini opencode amp aider cursor-agent tmux tmux:", nv, " "); for (i in nv) never[nv[i]] = 1
  n = split(panes, pp, " "); for (i = 1; i <= n; i++) pane[pp[i]] = 1
  m = split(skip, sk, " "); for (i = 1; i <= m; i++) skipped[sk[i]] = 1
  s = split(specs, a, " ")
  for (i = 1; i <= s; i++) {
    j = index(a[i], ":"); tid[i] = substr(a[i], 1, j - 1); wt[i] = substr(a[i], j + 1)
    rwt[i] = resolve(wt[i]); dir[i] = base(wt[i])
  }
}
{ pid = $1; ppid[pid] = $2; line = $0; sub(/^[ \t]*[0-9]+[ \t]+[0-9]+[ \t]*/, "", line); args[pid] = line }
END {
  for (p = me; p > 1 && !(p in self); p = ppid[p]) self[p] = 1
  for (pid in args) {
    if (pid in self) continue
    hold = 0; seen = ""
    for (q = pid; q > 1 && !index(seen, " " q " "); q = ppid[q]) {
      if ((q in pane) || q == me) { hold = 1; break }
      seen = seen " " q " "
      if (!(q in ppid)) break
    }
    if (hold || agentish(args[pid])) continue
    cwd = ""
    if (linux) { c = "readlink /proc/" pid "/cwd 2>/dev/null"; c | getline cwd; close(c); if (cwd == "") continue }
    for (i = 1; i <= s; i++) {
      if (tid[i] in skipped) continue
      if (linux) hit = within(cwd, wt[i]) || (rwt[i] != "" && within(cwd, rwt[i]))
      else hit = named(args[pid], wt[i]) || named(args[pid], "[" dir[i] "]")
      if (hit) { print tid[i], pid; break }
    }
  }
}`

// hostSweepScript is the one shell script a sweep runs on a host: it plans and
// ends the windows (hostSweepWindows), then the side processes of the runs that
// are due for it and were not left alone (hostSweepProcs). Its output is the
// plan's A and F lines, "W <window name>" per window ended and "P <task> <pid>"
// per side process signalled. A missing awk or ps is an error, never "nothing
// to do": the run is then retried rather than forgotten with its processes
// still running.
func hostSweepScript(session string, windowSpecs, procSpecs []string) string {
	lines := []string{
		`command -v awk >/dev/null 2>&1 || { echo "awk is not installed" >&2; exit 3; }`,
		`now=$(date +%s)`,
		`wins=$(tmux list-windows -t ` + shellQuote("="+session) + ` -F '#{window_id} #{window_name} #{window_activity} #{pane_start_command}' 2>/dev/null)`,
		`plan=$(printf '%s\n' "$wins" | awk -v now="$now" -v specs=` + shellQuote(strings.Join(windowSpecs, " ")) + ` ` + shellQuote(hostSweepWindows) + `) || exit 3`,
		`printf '%s\n' "$plan" | while read -r verb a b; do case "$verb" in`,
		`  K) tmux kill-window -t "$a" 2>/dev/null && echo "W $b" ;;`,
		`  A) echo "A $a $b" ;;`,
		`  F) echo "F $a" ;;`,
		`esac; done`,
	}
	if len(procSpecs) > 0 {
		lines = append(lines,
			`skip=$(printf '%s\n' "$plan" | awk '$1 == "A" || $1 == "F" { printf "%s ", $2 }')`,
			`command -v ps >/dev/null 2>&1 || { echo "ps is not installed" >&2; exit 3; }`,
			`uid=$(id -u) || exit 3`,
			`panes=$( { tmux list-panes -a -F '#{pane_pid}' 2>/dev/null; for s in "${TMUX_TMPDIR:-/tmp}/tmux-$uid"/*; do [ -S "$s" ] && tmux -S "$s" list-panes -a -F '#{pane_pid}' 2>/dev/null; done; } | tr '\n' ' ')`,
			`linux=0; [ -e /proc/self/cwd ] && linux=1`,
			`table=$(ps -u "$uid" -ww -o pid=,ppid=,args=) || { echo "ps failed" >&2; exit 3; }`,
			`procs=$(printf '%s\n' "$table" | awk -v me=$$ -v linux="$linux" -v panes="$panes" -v skip="$skip" -v specs=`+shellQuote(strings.Join(procSpecs, " "))+` `+shellQuote(hostSweepProcs)+`) || exit 3`,
			`if [ -n "$procs" ]; then`,
			`  printf '%s\n' "$procs" | while read -r t p; do kill -TERM "$p" 2>/dev/null && echo "P $t $p"; done`,
			`  sleep 2`,
			`  printf '%s\n' "$procs" | while read -r t p; do kill -KILL "$p" 2>/dev/null; done`,
			`fi`,
		)
	}
	return strings.Join(append(lines, "exit 0"), "\n")
}

// recordRemoteSuspend records what the host did for an idle run: the same state
// and log line a local suspend leaves, so every surface shows the task as
// suspended rather than running.
func (e *Executor) recordRemoteSuspend(host string, run idleRemoteRun, ended remoteEnded, now time.Time) {
	if wait, ok := ended.inUse[run.TaskID]; ok {
		// Someone is using it, so it is not idle whatever the database says. Not
		// asked about again before it could be.
		e.sessionEnd.sleep(run.TaskID, now.Add(wait))
		e.logger.Debug("Blocked remote task is in use; not suspending", "task", run.TaskID, "host", host, "recheck_in", wait)
		return
	}
	if ended.otherRun[run.TaskID] {
		e.sessionEnd.sleep(run.TaskID, now.Add(remoteSessionEndMaxBackoff))
		e.logger.Info("Blocked remote task's window belongs to a newer run; not suspending", "task", run.TaskID, "host", host)
		return
	}
	agent, procs := ended.windows[run.TaskID], ended.procs[run.TaskID]
	if run.HadSession || agent {
		if err := e.db.SuspendRemoteRun(run.TaskID, run.RunID); err != nil {
			e.logger.Warn("failed to clear remote session placement", "task", run.TaskID, "error", err)
		}
	}
	// The run is forgotten once nothing more will be done for it: its side
	// processes were ended, or never will be. A run whose side processes are
	// merely not due yet is kept, and picked up when they are.
	skipped := run.Due && run.Worktree == ""
	if run.sideProcesses() || !run.Reap || run.Worktree == "" {
		if err := e.db.EndRemoteRun(run.TaskID, run.RunID); err != nil {
			e.logger.Warn("Failed to forget suspended remote run", "task", run.TaskID, "error", err)
		}
	}
	if !agent && procs == 0 && !skipped {
		return
	}
	e.logger.Info("Suspended idle blocked task on host", "task", run.TaskID, "host", host,
		"idle", run.Idle.Round(time.Second), "agent", agent, "side_processes", procs, "side_processes_skipped", skipped)
	var msg []string
	switch {
	case agent:
		msg = append(msg, fmt.Sprintf("Agent suspended on %s (idle timeout); session preserved for resume.", host))
	default:
		msg = append(msg, fmt.Sprintf("Task is suspended on %s.", host))
	}
	if procs > 0 {
		msg = append(msg, fmt.Sprintf("Ended %d side process(es) it left running there.", procs))
	}
	if skipped {
		msg = append(msg, fmt.Sprintf("Its side processes on %s were left running: ty has no record of its worktree there, so they cannot be told apart from other tasks'.", host))
	}
	msg = append(msg, "The worktree there was left as it is.")
	e.logLine(run.TaskID, "system", strings.Join(msg, " "))
}

// getReapBlockedIdle returns reap_blocked_idle, the idle time after which a
// blocked task's side processes may be ended, and false when it is disabled
// ("0" or "disabled"). It reads the setting as `ty sessions cleanup` does.
func (e *Executor) getReapBlockedIdle() (time.Duration, bool) {
	val, err := e.db.GetSetting(config.SettingReapBlockedIdle)
	if err != nil || val == "" {
		return reaper.DefaultBlockedIdle, true
	}
	if val == "0" || val == "disabled" {
		return 0, false
	}
	if d, err := time.ParseDuration(val); err == nil && d > 0 {
		return d, true
	}
	return reaper.DefaultBlockedIdle, true
}
