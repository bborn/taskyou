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
//   - Its side processes are ended in the same call if it has also been idle for
//     reap_blocked_idle, the threshold `ty sessions cleanup` uses for them; with
//     that setting disabled they are left running. Once they are ended, the run
//     is forgotten, which is what stops the sweep asking again. A run whose
//     windows are gone but whose side processes are not yet due stays, and is
//     picked up when they are.
//
// Only processes are ended. The worktree on the host, and everything in it, is
// left exactly as it is.

// idleRemoteRun is a blocked, placed task the sweep is due to act on.
type idleRemoteRun struct {
	db.RemoteRun
	Idle time.Duration
	// Windows: the task still has a session on its host to end.
	Windows bool
	// SideProcesses: its side processes are due to be ended too.
	SideProcesses bool
	// Token is what names the task's worktree on a command line: its directory
	// ("13-fix-login") when ty recorded it, else "<id>-". Exact means the token
	// is a whole directory name, so "13-fix" must not match "13-fixed".
	Token string
	Exact bool
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
		task, err := e.db.GetTask(run.TaskID)
		if err != nil || task == nil {
			continue
		}
		idle, ok := blockedIdleDuration(task, now)
		if !ok || idle < timeout {
			continue
		}
		r := idleRemoteRun{
			RemoteRun:     run,
			Idle:          idle,
			Windows:       task.DaemonSession != "",
			SideProcesses: reap && idle >= reapAfter,
		}
		if !r.Windows && !r.SideProcesses {
			continue // already suspended; side processes not due (or never)
		}
		wt, _, _ := e.db.GetTaskRemoteWorktree(task.ID)
		r.Token, r.Exact = worktreeToken(task.ID, wt)
		due = append(due, r)
	}
	return due
}

// worktreeDirRe is a task worktree directory name as setupRemoteWorktree makes
// it, and nothing a shell or awk would read specially.
var worktreeDirRe = regexp.MustCompile(`^[0-9]+-[A-Za-z0-9._-]+$`)

// worktreeToken returns what names a task's worktree on a command line. The
// recorded directory is preferred: it is this task's alone, where "<id>-" is
// shared with any other coordinator's task of the same ID on the same host.
func worktreeToken(taskID int64, worktreePath string) (token string, exact bool) {
	prefix := fmt.Sprintf("%d-", taskID)
	if dir := path.Base(worktreePath); worktreePath != "" && strings.HasPrefix(dir, prefix) && worktreeDirRe.MatchString(dir) {
		return dir, true
	}
	return prefix, false
}

// sideProcessScript returns the shell that ends runs' side processes: anything
// whose command line runs out of the task's worktree ("/.task-worktrees/<dir>")
// or carries a puma/sidekiq title naming it ("[<dir>"). The anchoring is the
// one `ty sessions cleanup` uses (reaper.TaskIDFor): the ID is preceded by "/"
// or "[" and followed by "-", so task 13 never matches task 1319, or 113.
//
// The patterns are assembled inside awk rather than written into the script, so
// the script's own command line — and the sh running it — never matches. Each
// process gets SIGTERM, then SIGKILL two seconds later if it is still there,
// and is reported as "P <task> <pid>".
func sideProcessScript(runs []idleRemoteRun) string {
	specs := make([]string, 0, len(runs))
	for _, r := range runs {
		exact := 0
		if r.Exact {
			exact = 1
		}
		specs = append(specs, fmt.Sprintf("%d:%s:%d", r.TaskID, r.Token, exact))
	}
	match := strings.Join([]string{
		`function hit(s, pat, exact,   i, c) {`,
		`i = index(s, pat); if (!i) return 0;`,
		`if (!exact) return 1;`,
		`c = substr(s, i + length(pat), 1);`,
		`return c == "" || c !~ /[A-Za-z0-9_.-]/ }`,
		`BEGIN { n = split(specs, a, " "); for (i = 1; i <= n; i++) { split(a[i], f, ":"); id[i] = f[1]; tok[i] = f[2]; ex[i] = f[3] } }`,
		`$1 != me { for (i = 1; i <= n; i++) if (hit($0, "/.task-worktrees/" tok[i], ex[i]) || hit($0, "[" tok[i], ex[i])) { print id[i], $1; next } }`,
	}, " ")
	return strings.Join([]string{
		`procs=$(ps -eo pid=,args= 2>/dev/null | awk -v me=$$ -v specs=` + shellQuote(strings.Join(specs, " ")) + ` ` + shellQuote(match) + `)`,
		`if [ -n "$procs" ]; then` +
			` printf '%s\n' "$procs" | while read -r t p; do kill -TERM "$p" 2>/dev/null && echo "P $t $p"; done;` +
			` sleep 2;` +
			` printf '%s\n' "$procs" | while read -r t p; do kill -KILL "$p" 2>/dev/null; done;` +
			` fi`,
	}, "; ")
}

// recordRemoteSuspend records what the host ended for an idle run: the same
// state and log line a local suspend leaves, so every surface shows the task as
// suspended rather than running.
func (e *Executor) recordRemoteSuspend(host string, run idleRemoteRun, ended remoteEnded) {
	if run.Windows {
		if err := e.db.SuspendRemoteRun(run.TaskID, run.RunID); err != nil {
			e.logger.Warn("failed to clear remote session placement", "task", run.TaskID, "error", err)
		}
	}
	if run.SideProcesses {
		if err := e.db.EndRemoteRun(run.TaskID, run.RunID); err != nil {
			e.logger.Warn("Failed to forget suspended remote run", "task", run.TaskID, "error", err)
		}
	}
	agent, procs := ended.windows[run.TaskID], ended.procs[run.TaskID]
	if !agent && procs == 0 {
		return
	}
	e.logger.Info("Suspended idle blocked task on host", "task", run.TaskID, "host", host,
		"idle", run.Idle.Round(time.Second), "agent", agent, "side_processes", procs)
	var msg string
	switch {
	case agent && procs > 0:
		msg = fmt.Sprintf("Agent suspended on %s (idle timeout), with %d side process(es) it left running; session preserved for resume", host, procs)
	case agent:
		msg = fmt.Sprintf("Agent suspended on %s (idle timeout); session preserved for resume", host)
	default:
		msg = fmt.Sprintf("Ended %d side process(es) left running on %s by this suspended task", procs, host)
	}
	e.logLine(run.TaskID, "system", msg+". The worktree there was left as it is.")
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
