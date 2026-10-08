package executor

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// Ending a placed task's agent once the task is finished.
//
// A task placed on another host runs in a window of that host's
// task-daemon-remote-<coordinator> session, and nothing on this machine owns
// that process: the local janitors (cleanupInactiveDoneTasks, the TUI's close
// and archive) only reach the local tmux server. So a remote task that was
// closed or archived kept its agent running for days — on mona, thirteen of
// them holding 3.5 GB, and on a pooled cloud server, a machine its reaper
// counted as busy.
//
// The rule: once a task is done or archived, the daemon ends its task-<id> and
// task-<id>-shell windows on the host its last run was placed on. Blocked tasks
// are not touched — their agent is waiting for a reply, and ending it would
// lose the conversation. Only processes are ended; the remote worktree is left
// exactly as it is.
//
// It runs from the daemon loop rather than at each place a status changes,
// because those places are many and in different processes (`ty close`, the
// TUI, the web API, workflow steps): the database is where they all meet. The
// status change itself never waits for a host. The remote_runs row is the
// to-do list — it is removed only once the host has answered, so a sleeping
// laptop or a deleted server is retried, with backoff, instead of forgotten.
//
// The same sweep suspends blocked tasks that have sat idle on their host past
// idle_suspend_timeout, which is what suspendIdleBlockedTasks does for local
// ones: that sweep finds an agent by its local tmux pane, and a placed task has
// none here, so placed agents and the dev servers they started were never
// suspended (forty of them filled a staging host's memory and swap for twenty
// days). See remote_idle_suspend.go.

// remoteSessionEndTimeout bounds one host's ssh round trip.
const remoteSessionEndTimeout = 30 * time.Second

// Backoff for a host that could not be reached: from the first to the max,
// doubling. The max is also how long a laptop that wakes up may wait.
const (
	remoteSessionEndMinBackoff = time.Minute
	remoteSessionEndMaxBackoff = 15 * time.Minute
)

// remoteSessionEnder is the daemon's state for ending remote sessions.
type remoteSessionEnder struct {
	mu      sync.Mutex
	running bool
	retry   map[string]hostRetry
	// quiet holds blocked tasks found in use on their host, until they could
	// next be idle, so a pane someone is typing in costs no ssh call per sweep.
	quiet map[int64]time.Time
}

type hostRetry struct {
	at   time.Time
	wait time.Duration
}

func (s *remoteSessionEnder) due(host string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !now.Before(s.retry[host].at)
}

func (s *remoteSessionEnder) failed(host string, now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retry == nil {
		s.retry = make(map[string]hostRetry)
	}
	wait := min(max(s.retry[host].wait*2, remoteSessionEndMinBackoff), remoteSessionEndMaxBackoff)
	s.retry[host] = hostRetry{at: now.Add(wait), wait: wait}
	return wait
}

// awake reports whether a blocked task may be asked about again.
func (s *remoteSessionEnder) awake(taskID int64, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !now.Before(s.quiet[taskID])
}

// sleep leaves a blocked task alone until at.
func (s *remoteSessionEnder) sleep(taskID int64, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quiet == nil {
		s.quiet = make(map[int64]time.Time)
	}
	s.quiet[taskID] = at
}

func (s *remoteSessionEnder) succeeded(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.retry, host)
}

// startSweepingRemoteSessions runs one sweep in the background, unless one is
// still running: an unreachable host costs a connect timeout, and the worker
// loop must not wait on it.
func (e *Executor) startSweepingRemoteSessions(ctx context.Context) {
	e.sessionEnd.mu.Lock()
	if e.sessionEnd.running {
		e.sessionEnd.mu.Unlock()
		return
	}
	e.sessionEnd.running = true
	e.sessionEnd.mu.Unlock()

	go func() {
		defer func() {
			e.sessionEnd.mu.Lock()
			e.sessionEnd.running = false
			e.sessionEnd.mu.Unlock()
		}()
		e.sweepRemoteSessions(ctx, time.Now())
	}()
}

// hostWork is what one sweep asks of one host.
type hostWork struct {
	finished []db.RemoteRun // done or archived: end the windows, forget the run
	idle     []idleRemoteRun
}

func (w hostWork) empty() bool { return len(w.finished) == 0 && len(w.idle) == 0 }

// sweepRemoteSessions ends the remote sessions of done and archived tasks, and
// suspends blocked tasks that have been idle too long, one ssh call per host.
func (e *Executor) sweepRemoteSessions(ctx context.Context, now time.Time) {
	work := e.remoteSessionWork(now)
	if len(work) == 0 {
		return
	}
	coordinator, err := e.db.CoordinatorID()
	if err != nil {
		e.logger.Debug("Failed to read coordinator ID", "error", err)
		return
	}
	session := remoteDaemonSessionName(coordinator)

	hosts := make([]string, 0, len(work))
	for host := range work {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		if ctx.Err() != nil {
			return
		}
		if !e.sessionEnd.due(host, now) {
			continue
		}
		// Re-read just before acting: an earlier host may have taken a connect
		// timeout, and a task reopened and placed again since the first read
		// must not lose its new window.
		pending := e.remoteSessionWork(now)[host]
		if pending.empty() {
			continue
		}
		ended, err := endRemoteTaskSessions(ctx, host, session, pending)
		if err != nil {
			wait := e.sessionEnd.failed(host, now)
			e.logger.Warn("Could not end tasks' sessions on host; will retry",
				"host", host, "finished", len(pending.finished), "idle", len(pending.idle), "retry_in", wait, "error", err)
			continue
		}
		e.sessionEnd.succeeded(host)
		for _, run := range pending.finished {
			if err := e.db.EndRemoteRun(run.TaskID, run.RunID); err != nil {
				e.logger.Warn("Failed to forget ended remote run", "task", run.TaskID, "error", err)
			}
			if ended.windows[run.TaskID] {
				e.logger.Info("Ended finished task's remote session", "task", run.TaskID, "host", host, "status", run.Status)
				e.logLine(run.TaskID, "system", fmt.Sprintf(
					"Task is %s, so its agent session on %s was ended. The worktree there was left as it is.", run.Status, host))
			}
		}
		for _, run := range pending.idle {
			e.recordRemoteSuspend(host, run, ended, now)
		}
	}
}

// remoteSessionWork is the sweep's to-do list, by host.
func (e *Executor) remoteSessionWork(now time.Time) map[string]hostWork {
	work := make(map[string]hostWork)
	finished, err := e.db.FinishedRemoteRuns()
	if err != nil {
		e.logger.Debug("Failed to list finished remote runs", "error", err)
	}
	for _, run := range finished {
		w := work[run.Host]
		w.finished = append(w.finished, run)
		work[run.Host] = w
	}
	for _, run := range e.idleRemoteRuns(now) {
		w := work[run.Host]
		w.idle = append(w.idle, run)
		work[run.Host] = w
	}
	return work
}

// remoteEnded is what a host reports having done.
type remoteEnded struct {
	windows  map[int64]bool          // tasks that had a window ended
	procs    map[int64]int           // side processes signalled, by task
	inUse    map[int64]time.Duration // idle tasks left alone: in use, idle again after this
	otherRun map[int64]bool          // idle tasks left alone: window of another run
}

// endRemoteTaskSessions runs one sweep's work on host in a single ssh call (see
// hostSweepScript): it ends the task-<id> and task-<id>-shell windows of the
// finished runs, and of the idle runs nobody is using, in this coordinator's
// session; then the side processes of the idle runs due for it.
//
// Windows are matched by exact name inside the one session, and killed by ID,
// so another coordinator's task with the same ID — or the host's own ty — is
// never reached. A host with no such session or no tmux server has nothing to
// end, which is success; failing to reach the host, or a host without the
// tools to look, is an error. Nothing here touches a file.
func endRemoteTaskSessions(ctx context.Context, host, session string, work hostWork) (remoteEnded, error) {
	names := make(map[string]int64)
	var windowSpecs, procSpecs []string
	for _, run := range work.finished {
		names[TmuxWindowName(run.TaskID)] = run.TaskID
		names[TmuxWindowName(run.TaskID)+"-shell"] = run.TaskID
		windowSpecs = append(windowSpecs, fmt.Sprintf("%d:%s:0", run.TaskID, run.RunID))
	}
	for _, run := range work.idle {
		names[TmuxWindowName(run.TaskID)] = run.TaskID
		names[TmuxWindowName(run.TaskID)+"-shell"] = run.TaskID
		windowSpecs = append(windowSpecs, fmt.Sprintf("%d:%s:%d", run.TaskID, run.RunID, max(int64(run.Timeout/time.Second), 1)))
		if run.sideProcesses() {
			procSpecs = append(procSpecs, fmt.Sprintf("%d:%s", run.TaskID, run.Worktree))
		}
	}

	ctx, cancel := context.WithTimeout(WithRunner(ctx, RemoteRunner{Host: host}), remoteSessionEndTimeout)
	defer cancel()
	out, err := command(ctx, "", "sh", "-c", hostSweepScript(session, windowSpecs, procSpecs)).Output()
	if err != nil {
		return remoteEnded{}, err
	}
	ended := remoteEnded{
		windows:  make(map[int64]bool),
		procs:    make(map[int64]int),
		inUse:    make(map[int64]time.Duration),
		otherRun: make(map[int64]bool),
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if f[0] == "W" {
			if id, ok := names[f[1]]; ok {
				ended.windows[id] = true
			}
			continue
		}
		id, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch {
		case f[0] == "P" && len(f) == 3:
			ended.procs[id]++
		case f[0] == "A" && len(f) == 3:
			if secs, err := strconv.Atoi(f[2]); err == nil {
				ended.inUse[id] = time.Duration(secs) * time.Second
			}
		case f[0] == "F":
			ended.otherRun[id] = true
		}
	}
	return ended, nil
}
